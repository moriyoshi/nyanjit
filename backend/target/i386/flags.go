package i386

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// FlagFacts reports the flags each form overwrites and what the surviving
// sign, zero and carry describe. INC and DEC keep CF. Widths describe the
// instruction actually encoded: an 8- or 16-bit OpBinary or OpNarrow, and
// an 8- or 16-bit OpShift other than SAR, ends with AND A, mask, whose sign
// is that of the 32-bit register and whose zero is that of the narrow result. A pair form's flags come from its high-word
// instruction alone, so they never give a 64-bit zero: OpBinaryCarry, the
// ADC or SBB after the low word's ADD or SUB, gives the 64-bit sign of the
// pair (bit 31 of A) and the carry or borrow of the 64-bit operation, and
// SAR's sign is that of its input, as the high word of a sign extension
// needs.
// Calls, system calls and tail calls clobber every flag. A PreserveFlags
// form gives no fact: a LEA, a move and MOVZX modify no flag, and the 8- or
// 16-bit operation or shift that MOVZX follows modifies what its 32-bit form
// does, with the sign and zero of the unmasked result.
func FlagFacts(ins machine.Instruction) (s machine.FlagState) {
	if !flagForms[ins.Op] {
		return s // the stream asks for every instruction; most write no flag
	}
	all := machine.FlagSign | machine.FlagZero | machine.FlagCarry
	fact := func(w uint8) machine.FlagFact {
		return machine.FlagFact{Valid: true, Width: w, Value: -1, Left: -1, Right: -1}
	}
	carry := func(w uint8) machine.FlagFact {
		f := fact(w)
		f.Condition, f.ConditionValid = op.BranchBelow, true
		return f
	}
	width := ins.Bits
	if width == 0 {
		width = 32
	}
	if ins.PreserveFlags {
		if modified, ok := preservedModified(ins); ok {
			s.Modified = modified
			return s
		}
	}
	switch ins.Op {
	case machine.OpBinary:
		s.Modified = all
		if width == 8 || width == 16 {
			s.Sign, s.Zero = fact(32), fact(width)
			return s
		}
		if ins.Arithmetic == op.OpMul {
			// IMUL leaves S/Z undefined; CF and OF report signed overflow
			// of the 32-bit product.
			if width == 32 {
				s.Carry = fact(32)
			}
			return s
		}
		if width == 32 {
			s.Sign, s.Zero = fact(32), fact(32)
			if ins.Arithmetic == op.OpAdd || ins.Arithmetic == op.OpSub {
				s.Carry = carry(32)
			}
		}
	case machine.OpBinaryImmediate:
		s.Modified = all
		if width == 32 { // narrower widths are not masked
			s.Sign, s.Zero = fact(32), fact(32)
			if ins.Arithmetic == op.OpAdd || ins.Arithmetic == op.OpSub {
				s.Carry = carry(32)
			}
		}
	case machine.OpInc, machine.OpDec:
		s.Modified = machine.FlagSign | machine.FlagZero
		s.Sign, s.Zero = fact(32), fact(32)
	case machine.OpNarrow:
		if ins.Imm == 8 || ins.Imm == 16 {
			s.Modified = all
			s.Sign, s.Zero = fact(32), fact(uint8(ins.Imm))
		}
	case machine.OpSignExtend:
		if ins.Imm == 8 || ins.Imm == 16 {
			// SHL; SAR: CF is the last bit shifted out.
			s.Modified = all
			s.Sign, s.Zero = fact(32), fact(32)
		}
	case machine.OpBinaryCarry:
		s.Modified = all
		if ins.Arithmetic == op.OpAdd || ins.Arithmetic == op.OpSub {
			s.Sign, s.Carry = fact(64), carry(64)
		}
	case machine.OpWordCarry:
		// ADC/SBB of one 32-bit word; its CF is not tracked.
		s.Modified = all
		s.Sign, s.Zero = fact(32), fact(32)
	case machine.OpCarryBit:
		if ins.CarryMode != machine.CarryFlags {
			s.Modified = all
		} // SETB and MOVZX read CF only.
	case machine.OpShift:
		s.Modified = shiftFlags(ins) // Counts can leave carry undefined.
		if s.Modified != 0 && ins.Arithmetic == op.OpShiftRightArithmetic && (ins.Bits == 0 || ins.Bits == 32) {
			s.Sign = fact(32)
		}
		if (width == 8 || width == 16) && ins.Arithmetic != op.OpShiftRightArithmetic {
			s.Sign, s.Zero = fact(32), fact(width)
		}
	case machine.OpIntToFloat, machine.OpFloatToInt:
		if convertWritesFlags(ins) {
			s.Modified = all
		}
	case machine.OpShiftBy:
		s.Modified = all
		if (ins.Arithmetic == op.OpRotateLeft || ins.Arithmetic == op.OpRotateRight) && ins.Bits == 32 {
			s.Modified = machine.FlagCarry // ROL/ROR r32, CL writes CF and OF only.
		}
	case machine.OpAtomic:
		if ins.Bits == 64 && (ins.Arithmetic == op.OpAtomicSwap || ins.Arithmetic == op.OpAtomicCompareSwap) {
			s.Modified = machine.FlagZero // CMPXCHG8B writes ZF only.
		} else if atomicWritesFlags(ins) {
			s.Modified = all
		}
	case machine.OpReverseBytes:
		if ins.Bits == 16 {
			s.Modified = machine.FlagCarry // ROL r16, 8 writes CF and OF only.
		}
	case machine.OpMulWide, machine.OpMulHigh, machine.OpMulHighSigned, machine.OpDivide, machine.OpSelect,
		machine.OpCompareImmediate, machine.OpCompareRegister, machine.OpFloatCompare, machine.OpCompareSet, machine.OpJumpTable,
		machine.OpFrameEnter, machine.OpFrameLeave, machine.OpCallC, machine.OpCallGo, machine.OpSyscall, machine.OpTailCall:
		s.Modified = all
	}
	return s
}

// flagForms holds the opcodes FlagFacts can report a modified flag for: its
// switch's cases. preservedFor rewrites only some of them.
var flagForms = [256]bool{
	machine.OpBinary: true, machine.OpBinaryImmediate: true, machine.OpInc: true, machine.OpDec: true,
	machine.OpNarrow: true, machine.OpSignExtend: true, machine.OpBinaryCarry: true, machine.OpWordCarry: true,
	machine.OpCarryBit: true, machine.OpShift: true, machine.OpIntToFloat: true, machine.OpFloatToInt: true,
	machine.OpShiftBy: true, machine.OpAtomic: true, machine.OpReverseBytes: true,
	machine.OpMulWide: true, machine.OpMulHigh: true, machine.OpMulHighSigned: true, machine.OpDivide: true, machine.OpSelect: true,
	machine.OpCompareImmediate: true, machine.OpCompareRegister: true, machine.OpFloatCompare: true, machine.OpCompareSet: true, machine.OpJumpTable: true,
	machine.OpFrameEnter: true, machine.OpFrameLeave: true, machine.OpCallC: true, machine.OpCallGo: true, machine.OpSyscall: true, machine.OpTailCall: true,
}

// preservedModified reports the flags the PreserveFlags form of ins
// modifies, when it has one; it keeps that form out of FlagFacts's frame.
func preservedModified(ins machine.Instruction) (machine.FlagMask, bool) {
	f, ok := preservedFor(ins)
	return f.modified(), ok
}

// shiftFlags reports the flags an immediate shift writes. The CPU masks a
// count to five bits, and a masked count of zero writes no flag; a 32-bit
// rotation writes CF and OF only; the 8- and 16-bit forms mask or
// sign-extend with flag-writing instructions. A pair shift writes none when
// it only moves words: a count of 0, 32, 64, ... other than SAR's.
func shiftFlags(ins machine.Instruction) machine.FlagMask {
	all := machine.FlagSign | machine.FlagZero | machine.FlagCarry
	n := byte(ins.Imm)
	switch {
	case ins.Bits == 64:
		if n == 0 || n&31 == 0 && ins.Arithmetic != op.OpShiftRightArithmetic {
			return 0
		}
	case ins.Bits == 8 || ins.Bits == 16:
	case n&31 == 0:
		return 0
	case ins.Arithmetic == op.OpRotateLeft || ins.Arithmetic == op.OpRotateRight:
		return machine.FlagCarry
	}
	return all
}

// convertWritesFlags reports the conversions that test or compare: all but a
// signed word to float, an unsigned pair to binary64 and a native signed
// truncation to a word.
func convertWritesFlags(ins machine.Instruction) bool {
	if ins.Op == machine.OpIntToFloat {
		if ins.Imm == 32 {
			return ins.Arithmetic != op.OpFFromSigned
		}
		return ins.Arithmetic == op.OpFFromSigned || ins.Bits == 32
	}
	return ins.Arithmetic != op.OpFToSigned || ins.Bits != 32 || op.ConversionSemantics(ins.Imm) == op.ConversionSaturate
}
