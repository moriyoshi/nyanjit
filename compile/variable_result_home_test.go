package compile_test

import (
	"os"
	"strings"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestARM64MutableParameterUsesResultRegister(t *testing.T) {
	b := n.NewBuilder()
	x := b.ParamBits(64)
	v := b.Var(x)
	b.Set(v, b.Add(b.Get(v), b.ConstBits(64, 7)))
	b.Return(b.Get(v))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
			listing, err := lowerListing(p, compile.TargetSpec{Architecture: "arm64"}, compile.Func{
				ABI: flavor, Stack: compile.StackPolicy{Practice: stack},
			})
			if err != nil {
				t.Fatal(err)
			}
			var rows []string
			for _, line := range strings.Split(listing, "\n") {
				if _, instruction, ok := strings.Cut(line, "  "); ok {
					rows = append(rows, instruction)
				}
			}
			if got := strings.Join(rows, "\n"); got != "add X0, X0, #7\nret" {
				t.Errorf("ABI %v, stack %v: mutable parameter should update its return register directly:\n%s", flavor, stack, listing)
			}
		}
	}
}

func TestReturnedVariableUsesResultRegister(t *testing.T) {
	accumulate := n.NewBuilder()
	params := []n.Value{accumulate.ParamBits(64), accumulate.ParamBits(64), accumulate.ParamBits(64)}
	sum := accumulate.Var(accumulate.ConstBits(64, 0))
	for _, x := range params {
		accumulate.Set(sum, accumulate.Add(accumulate.Get(sum), x))
	}
	accumulate.Set(sum, accumulate.Xor(accumulate.Get(sum), accumulate.ConstBits(64, 0x55)))
	accumulate.Return(accumulate.Get(sum))
	computed := n.NewBuilder()
	product := computed.Var(computed.Mul(computed.ParamBits(64), computed.ParamBits(64)))
	computed.Set(product, computed.Add(computed.Get(product), computed.ConstBits(64, 1)))
	computed.Return(computed.Get(product))
	programs := map[*n.Builder]*ir.SealedProgram{}
	for _, b := range []*n.Builder{accumulate, computed} {
		p, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		programs[b] = p
	}
	type listingCase struct {
		arch string
		b    *n.Builder
		want string
	}
	check := func(what string, cases []listingCase) {
		t.Helper()
		for _, tc := range cases {
			listing, err := lowerListing(programs[tc.b], compile.TargetSpec{Architecture: tc.arch}, compile.Func{ABI: abi.C})
			if err != nil {
				t.Fatal(err)
			}
			var rows []string
			for _, line := range strings.Split(listing, "\n") {
				if _, instruction, ok := strings.Cut(line, "  "); ok {
					rows = append(rows, instruction)
				}
			}
			if got := strings.Join(rows, "\n"); got != tc.want {
				t.Errorf("%s: %s:\n%s", tc.arch, what, listing)
			}
		}
	}
	// Promoted, the Vars are plain SSA values and lower as the same
	// expressions written without Vars.
	if os.Getenv("NYANJIT_INTERNAL_VAR_PROMOTION") != "0" {
		check("promoted Vars should lower as plain values", []listingCase{
			{"amd64", accumulate, "mov RAX, RDI\nadd RAX, RSI\nadd RAX, RDX\nxor RAX, 85\nret"},
			{"amd64", computed, "mov RAX, RDI\nimul RAX, RSI\ninc RAX\nret"},
			{"arm64", computed, "movz X2, #0x1\nmadd X2, X0, X1, X2\nmov X0, X2\nret"},
		})
	}
	// With homes, the returned Var lives in the result register.
	compile.WithoutVarPromotion(t)
	check("returned variable should live in the result register", []listingCase{
		{"amd64", accumulate, "mov EAX, 0x0\nadd RAX, RDI\nadd RAX, RSI\nadd RAX, RDX\nxor RAX, 85\nret"},
		{"amd64", computed, "mov RAX, RDI\nimul RAX, RSI\ninc RAX\nret"},
		{"arm64", computed, "mul X0, X0, X1\nadd X0, X0, #1\nret"},
	})
}
