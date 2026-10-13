package compile_test

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestProfileInstructionSelection(t *testing.T) {
	for _, tc := range []struct {
		arch           string
		extensions     []string
		name, mnemonic string
	}{
		{"loong64", nil, "shift", "srl.d "}, {"loong64", nil, "mulhigh", "mulh.du "}, {"loong64", nil, "rotateby", "rotr.d "}, {"loong64", nil, "leading", "clz.d "}, {"loong64", nil, "address", "alsl.d "},
		{"amd64", []string{"bmi2"}, "shift", "shrx "}, {"amd64", []string{"bmi2"}, "mulhigh", "mulx "}, {"amd64", []string{"bmi2"}, "rotate", "rorx "},
		{"amd64", []string{"lzcnt"}, "leading", "lzcnt "}, {"amd64", []string{"bmi1"}, "trailing", "tzcnt "},
		{"riscv64", []string{"zbb"}, "rotateby", "rol "}, {"riscv64", []string{"zbb"}, "leading", "clz "}, {"riscv64", []string{"zbb"}, "andnot", "andn "}, {"riscv64", []string{"zba"}, "address", "sh3add "},
	} {
		t.Run(tc.arch+"/"+tc.name, func(t *testing.T) {
			b := nyanjit.NewBuilder()
			x, y := b.ParamBits(64), b.ParamBits(64)
			var v nyanjit.Value
			switch tc.name {
			case "shift":
				v = b.ShiftRightBy(x, y)
			case "mulhigh":
				v = b.Const(0)
			case "rotate":
				v = b.RotateRight(x, 7)
			case "rotateby":
				v = b.RotateLeftBy(x, y)
			case "leading":
				v = b.LeadingZeros(x)
			case "trailing":
				v = b.TrailingZeros(x)
			case "andnot":
				v = x.And(y.Xor(b.Const(^uint64(0))))
			case "address":
				v = x.Add(y.Mul(b.Const(8)))
			}
			b.Return(v)
			p := finishCrossAssembly(t, b)
			if tc.name == "mulhigh" {
				p = &ir.Program{Arity: 2, Result: 2, Instructions: []ir.Instruction{{Op: ir.OpParam, Bits: 64}, {Op: ir.OpParam, Bits: 64}, {Op: ir.OpMulHigh, Bits: 64, A: 0, B: 1}}}
			}
			for _, enabled := range []bool{false, true} {
				spec := compile.TargetSpec{Architecture: tc.arch}
				if enabled {
					spec.CPU.Extensions = tc.extensions
				}
				asm, err := lowerListing(p, spec, compile.Func{})
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(asm, tc.mnemonic) != (enabled || tc.arch == "loong64") {
					t.Fatalf("enabled%v expected %s:\n%s", enabled, tc.mnemonic, asm)
				}
			}
		})
	}
}
