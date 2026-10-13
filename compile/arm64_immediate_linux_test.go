//go:build linux && arm64

package compile_test

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/target/arm64"
)

func TestARM64AddSubImmediateSelectionAndExecution(t *testing.T) {
	type binaryOp func(*builder.Builder, builder.Value, builder.Value) (builder.Value, error)
	for _, test := range []struct {
		name, mnemonic string
		operation      binaryOp
		constant       uint64
		immediate      bool
		want           func(uint64, uint64) uint64
	}{
		{"add positive boundary", "add", (*builder.Builder).Add, 4095, true, func(x, y uint64) uint64 { return x + y }},
		{"add negative becomes subtract", "sub", (*builder.Builder).Add, ^uint64(4094), true, func(x, y uint64) uint64 { return x + y }},
		{"add shifted form", "add", (*builder.Builder).Add, 4095 << 12, true, func(x, y uint64) uint64 { return x + y }},
		{"subtract positive", "sub", (*builder.Builder).Sub, 17, true, func(x, y uint64) uint64 { return x - y }},
		{"subtract negative becomes add", "add", (*builder.Builder).Sub, ^uint64(4095), true, func(x, y uint64) uint64 { return x - y }},
		{"add unencodable", "", (*builder.Builder).Add, 4097, false, func(x, y uint64) uint64 { return x + y }},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := builder.NewBuilder()
			input, err := b.Param()
			if err != nil {
				t.Fatal(err)
			}
			constant, err := b.Const(test.constant)
			if err != nil {
				t.Fatal(err)
			}
			result, err := test.operation(b, input, constant)
			if err != nil {
				t.Fatal(err)
			}
			if err := b.Return(result); err != nil {
				t.Fatal(err)
			}
			program, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			selected, err := machine.SelectProgram(program.Instructions, program.ScopeExits, program.Arity,
				program.Result, nil, abi.MaxArguments(), arm64.NewPolicy(abi.C, abi.StackNoUse))
			if err != nil {
				t.Fatal(err)
			}
			var immediates int
			for _, ins := range selected.Instructions {
				if ins.Op == machine.SelectedBinaryImmediate {
					immediates++
				}
			}
			if (immediates == 1) != test.immediate {
				t.Fatalf("selected %d immediate operations; want immediate=%t", immediates, test.immediate)
			}
			options := abi.CompileOptions{ABI: abi.C, Stack: abi.StackNoUse}
			if test.immediate {
				assembly, err := compile.EmitAssembly(program, options)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(assembly, test.mnemonic+"s X0, X0, #") {
					t.Fatalf("immediate instruction missing:\n%s", assembly)
				}
			}
			code, err := compile.Compile(program, options)
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			for _, value := range []uint64{0, 1, 0x123456789abcdef0, ^uint64(0)} {
				got, err := code.Call(uintptr(value))
				want := test.want(value, test.constant)
				if err != nil || uint64(got) != want {
					t.Fatalf("Call(%#x) = %#x, %v; want %#x", value, got, err, want)
				}
			}
		})
	}
}
