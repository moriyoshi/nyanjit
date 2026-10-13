//go:build linux && (amd64 || 386 || arm64 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// Based on TestReviewZeroBranchOnFPValue; keep the multiply live while the
// preceding FP sum is given an integer zero-branch hint.
func TestFPRegisterHintZeroBranch(t *testing.T) {
	for _, width := range []uint8{32, 64} {
		for _, form := range []string{"zero", "equal", "notEqual"} {
			b := nyanjit.NewBuilder()
			x := b.ParamBits(width)
			v := b.FAdd(x, fpConstant(b, width, 1))
			w := b.FMul(v, fpConstant(b, width, 3))
			pred := b.IsZero(v)
			if form == "equal" {
				pred = b.CompareImmediate(v, ir.BranchEqual, 0)
			}
			if form == "notEqual" {
				pred = b.CompareImmediate(v, ir.BranchNotEqual, 0).Not()
			}
			z, nz := b.NewLabel(), b.NewLabel()
			// w reaches nz by dominance.
			b.Branch(pred, z)
			b.Jump(nz)
			b.Bind(z)
			b.Return(b.Const(7))
			b.Bind(nz)
			b.Return(b.Xor(b.CastUnsigned(w, 64), b.Const(0x55)))
			p := finishCrossAssembly(t, b)
			code, err := nyanjit.Compile(p, compile.TargetSpec{}, compile.Func{})
			if err != nil {
				t.Fatal(err)
			}
			for _, x := range []float64{2, -1, -3} {
				want := fpBits(width, (x+1)*3) ^ 0x55
				if x == -1 {
					want = 7
				}
				got, err := callMathBits(code, width, fpBits(width, x))
				if err != nil || uint64(got) != want {
					t.Fatalf("width%d %s x%v got%#x,%v want%#x", width, form, x, got, err, want)
				}
			}
			code.Close()
		}
	}
}
