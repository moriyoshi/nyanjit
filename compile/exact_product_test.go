package compile_test

import (
	"math"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func productErrorProgram() *ir.Program {
	return &ir.Program{Arity: 2, Result: 3, Instructions: []ir.Instruction{{Op: ir.OpParam, Bits: 64}, {Op: ir.OpParam, Bits: 64}, {Op: ir.OpFMul, Bits: 64, A: 0, B: 1}, {Op: ir.OpFProductError, Bits: 64, A: 0, B: 1, C: 2}}}
}

func TestExactProductPublicIRContract(t *testing.T) {
	// Public IR may contain runtime operands, so validation is structural.
	// Split-overflow values in this review are explicitly outside the numeric
	// contract; compiling them must not promise profile-independent results.
	for _, ab := range [][2]float64{{0x1p1000, 0x1p-990}, {math.MaxFloat64, 0.5}} {
		p := productErrorProgram()
		p.Arity = 0
		p.Instructions[0] = ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: math.Float64bits(ab[0])}
		p.Instructions[1] = ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: math.Float64bits(ab[1])}
		if err := p.Validate(); err != nil {
			t.Fatal(err)
		}
		for _, cpu := range []abi.CPUProfile{{}, {Extensions: []string{"fma"}}} {
			if _, err := lowerListing(p, compile.TargetSpec{Architecture: "amd64", CPU: cpu}, compile.Func{}); err != nil {
				t.Fatal(err)
			}
		}
		// The runtime accuracy oracle covers only the well-scaled domain.
		p.Instructions[3].Bits = 32
		if err := p.Validate(); err == nil {
			t.Fatal("exact-product operation accepted mismatched/non-binary64 operands")
		}
	}
}
func TestExactProductSelectsFMAByCPU(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "riscv64", "loong64", "ppc64le"} {
		p := productErrorProgram()
		if err := p.Validate(); err != nil {
			t.Fatal(err)
		}
		s, err := lowerListing(p, compile.TargetSpec{Architecture: arch}, compile.Func{})
		if err != nil {
			t.Fatal(err)
		}
		if arch == "amd64" {
			if strings.Contains(s, "vfmadd") {
				t.Fatal("baseline emitted FMA")
			}
			s, err = lowerListing(p, compile.TargetSpec{Architecture: arch, CPU: abi.CPUProfile{Extensions: []string{"fma"}}}, compile.Func{})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(s, "vfmadd") {
				t.Fatal("FMA profile retained Dekker")
			}
		} else if !strings.Contains(strings.ToLower(s), "fmadd") {
			t.Fatalf("%s missing FMA:\n%s", arch, s)
		}
	}
	p := productErrorProgram()
	p.Instructions[3].Bits = 32
	if err := p.Validate(); err == nil {
		t.Fatal("binary32 exact product accepted")
	}
}
