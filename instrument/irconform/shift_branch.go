package irconform

import (
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

// shiftBranchCases branch on a narrow shift or rotation result at register
// width, so a target must leave the result zero-extended there.
func shiftBranchCases() []Case {
	var cases []Case
	for _, w := range []uint8{8, 16} {
		for _, op := range []ir.Opcode{ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight} {
			cases = append(cases, Case{Name: name("shift", op, w, 1) + "-branch", Inputs: unary(w), Build: built(func(b *nyanjit.Builder) {
				x := b.ParamBits(w)
				var r nyanjit.Value
				switch op {
				case ir.OpShiftLeft:
					r = b.ShiftLeft(x, 1)
				case ir.OpShiftRight:
					r = b.ShiftRight(x, 1)
				case ir.OpShiftRightArithmetic:
					r = b.ShiftRightArithmetic(x, 1)
				case ir.OpRotateLeft:
					r = b.RotateLeft(x, 1)
				default:
					r = b.RotateRight(x, 1)
				}
				inRange := b.NewLabel()
				b.Branch(b.CompareImmediate(r, ir.BranchBelowEqual, int64(mask(w))), inRange)
				b.Return(b.Const(2))
				b.Bind(inRange)
				b.Return(b.Const(1))
			})})
		}
	}
	return cases
}
