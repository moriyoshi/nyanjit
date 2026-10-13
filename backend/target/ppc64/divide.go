package ppc64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// divide expands OpDivide. POWER divides never trap, but a zero divisor or a
// signed MIN / -1 leaves the result undefined, so checked semantics guard
// those inputs with a CR0 compare and isel. Every form reads B and C before
// it writes A, so A may alias either input; R11/R12 hold intermediates.
func (p *expansion) divide(ins machine.Instruction) error {
	w := ins.Bits
	if w != 8 && w != 16 && w != 32 && w != 64 || ins.Imm > 0xff || !op.DivisionSemantics(ins.Imm).Valid() {
		return machine.ErrUnsupportedOperation
	}
	var signed, remainder bool
	switch ins.Arithmetic {
	case op.OpDiv:
	case op.OpDivSigned:
		signed = true
	case op.OpRem:
		remainder = true
	case op.OpRemSigned:
		signed, remainder = true, true
	default:
		return machine.ErrUnsupportedOperation
	}
	for _, r := range []backend.Register{ins.A, ins.B, ins.C} {
		if r > 31 {
			return machine.ErrUnsupportedOperation
		}
		if r == 11 || r == 12 {
			return machine.ErrInvalidInstruction
		}
	}
	semantics := op.DivisionSemantics(ins.Imm)
	checked := semantics != op.DivisionUnchecked
	power9 := p.emitter.cpu >= abi.PPC64Power9
	dst, x, y := ins.A, ins.B, ins.C
	compare := func(value int64) { p.emit(Instruction{Op: Cmpi, B: y, Imm: value, Bits: 64}) }
	// isel reads RA=0 as literal zero, so set=0 selects zero.
	selectEqual := func(d, set, clear backend.Register) {
		p.emit(Instruction{Op: Isel, A: d, B: set, C: clear, BI: 2})
	}
	// x - (x/y)*y is x for a zero divisor whatever the undefined quotient
	// is, which already meets x%0 = x without a guard.
	formula := func(d, q, x, y backend.Register, divide Opcode) {
		p.emit(Instruction{Op: divide, A: q, B: x, C: y})
		p.emit(Instruction{Op: Mulld, A: q, B: q, C: y})
		p.emit(Instruction{Op: Subf, A: d, B: x, C: q})
	}
	if signed && w < 64 {
		if !checked && w == 32 {
			// divw and modsw read the low words as signed; their upper
			// result words are undefined in 64-bit mode.
			if !remainder {
				p.emit(Instruction{Op: Divw, A: dst, B: x, C: y})
			} else if power9 {
				p.emit(Instruction{Op: Modsw, A: dst, B: x, C: y})
			} else {
				formula(dst, 11, x, y, Divw)
			}
			return p.narrow(dst, dst, 32)
		}
		// Sign-extended operands divide exactly in 64 bits, where the
		// narrow MIN / -1 does not overflow: masking its quotient gives MIN
		// back and its remainder is 0, so only a zero divisor needs a guard.
		guard := checked && (!remainder || power9)
		if guard {
			compare(0)
		}
		if err := p.signExtend(11, x, w); err != nil {
			return err
		}
		if err := p.signExtend(12, y, w); err != nil {
			return err
		}
		switch {
		case !remainder && checked:
			p.emit(Instruction{Op: Divd, A: 11, B: 11, C: 12})
			set := backend.Register(0)
			if semantics == op.DivisionAllOnes {
				p.emit(Instruction{Op: Addi, A: 12, Imm: -1})
				set = 12
			}
			selectEqual(11, set, 11)
			return p.narrow(dst, 11, w)
		case !remainder:
			p.emit(Instruction{Op: Divd, A: dst, B: 11, C: 12})
		case power9:
			p.emit(Instruction{Op: Modsd, A: dst, B: 11, C: 12})
			if guard {
				selectEqual(dst, 11, dst)
			}
		default:
			formula(dst, dst, 11, 12, Divd)
		}
		return p.narrow(dst, dst, w)
	}
	// Unsigned operands are zero-extended, so the 64-bit instructions give
	// zero-extended narrow results directly.
	divide, modulo := Divdu, Modud
	if signed {
		divide, modulo = Divd, Modsd
	}
	if !checked {
		if !remainder {
			p.emit(Instruction{Op: divide, A: dst, B: x, C: y})
		} else if power9 {
			p.emit(Instruction{Op: modulo, A: dst, B: x, C: y})
		} else {
			formula(dst, 11, x, y, divide)
		}
		return nil
	}
	if remainder {
		// x % -1 is 0 for every x, which covers MIN % -1.
		if power9 && x != 0 {
			compare(0)
			p.emit(Instruction{Op: modulo, A: 11, B: x, C: y})
			if !signed {
				selectEqual(dst, x, 11)
				return nil
			}
			selectEqual(11, x, 11)
			compare(-1)
			selectEqual(dst, 0, 11)
			return nil
		}
		// isel cannot select r0 as x, so r0 also takes the formula.
		if !signed {
			formula(dst, 11, x, y, divide)
			return nil
		}
		compare(-1)
		formula(11, 11, x, y, divide)
		selectEqual(dst, 0, 11)
		return nil
	}
	compare(0)
	p.emit(Instruction{Op: divide, A: 11, B: x, C: y})
	set := backend.Register(0)
	if semantics == op.DivisionAllOnes {
		p.emit(Instruction{Op: Addi, A: 12, Imm: -1})
		set = 12
	}
	if !signed {
		// A narrow all-ones result is masked back to Bits.
		if w == 64 || set == 0 {
			selectEqual(dst, set, 11)
			return nil
		}
		selectEqual(11, set, 11)
		return p.narrow(dst, 11, w)
	}
	// x / -1 is -x for every x, and -MIN wraps to MIN as required.
	selectEqual(11, set, 11)
	compare(-1)
	p.emit(Instruction{Op: Neg, A: 12, B: x})
	selectEqual(dst, 12, 11)
	return nil
}

// intToFloat converts GP B to FPR A in the current rounding mode. The
// integer moves straight into A, so no other FPR is written. A 32-bit
// source is sign- or zero-extended by the move; a zero-extended word is
// non-negative, so the signed conversion is exact for it.
func (p *expansion) intToFloat(ins machine.Instruction) error {
	if !backend.IsVectorRegister(ins.A) || ins.B > 31 || ins.Bits != 32 && ins.Bits != 64 || ins.Imm != 32 && ins.Imm != 64 {
		return machine.ErrUnsupportedOperation
	}
	if ins.Arithmetic != op.OpFFromSigned && ins.Arithmetic != op.OpFFromUnsigned {
		return machine.ErrUnsupportedOperation
	}
	d := backend.Register(backend.VectorNumber(ins.A))
	move, convert := Mtvsrd, Fcfid
	switch {
	case ins.Imm == 32 && ins.Arithmetic == op.OpFFromSigned:
		move = Mtvsrwa
	case ins.Imm == 32:
		move = Mtvsrwz
	case ins.Arithmetic == op.OpFFromUnsigned:
		convert = Fcfidu
	}
	p.emit(Instruction{Op: move, A: d, B: ins.B})
	p.emit(Instruction{Op: convert, A: d, B: d, Bits: ins.Bits})
	return nil
}

// floatToInt truncates FPR B to GP A. The fcti*z conversions write an FPR,
// so the result goes through f0, the expansion FP temporary, which
// machine.FloatWrites reports for this operation. FPRs hold binary32 values
// in double format, so one conversion serves both source widths.
//
// The conversions already saturate, and the unsigned ones give 0 for NaN.
// The signed ones give MIN for NaN, which ConversionSaturate replaces with 0
// after an unordered self-compare. The compare reads B before f0 is written,
// so B may be f0. A 32-bit result occupies the low word, which mfvsrwz
// zero-extends.
func (p *expansion) floatToInt(ins machine.Instruction) error {
	if ins.A > 31 || !backend.IsVectorRegister(ins.B) || ins.Bits != 32 && ins.Bits != 64 || ins.Disp != 32 && ins.Disp != 64 ||
		ins.Imm > 0xff || !op.ConversionSemantics(ins.Imm).Valid() {
		return machine.ErrUnsupportedOperation
	}
	signed := ins.Arithmetic == op.OpFToSigned
	if !signed && ins.Arithmetic != op.OpFToUnsigned {
		return machine.ErrUnsupportedOperation
	}
	convert := map[[2]bool]Opcode{{true, true}: Fctidz, {false, true}: Fctiduz, {true, false}: Fctiwz, {false, false}: Fctiwuz}[[2]bool{signed, ins.Bits == 64}]
	f := backend.Register(backend.VectorNumber(ins.B))
	guard := signed && op.ConversionSemantics(ins.Imm) == op.ConversionSaturate
	if guard {
		p.emit(Instruction{Op: Fcmpu, B: f, C: f})
	}
	p.emit(Instruction{Op: convert, A: 0, B: f})
	if ins.Bits == 64 {
		p.emit(Instruction{Op: Mfvsrd, A: ins.A, B: 0})
	} else {
		p.emit(Instruction{Op: Mfvsrwz, A: ins.A, B: 0})
	}
	if guard {
		// isel reads RA=0 as literal zero; CR0 bit 3 is FU after fcmpu.
		p.emit(Instruction{Op: Isel, A: ins.A, B: 0, C: ins.A, BI: 3})
	}
	return nil
}
