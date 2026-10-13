package arm64

import (
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

func (Policy) CanMulHigh(w uint8) bool   { return w == 8 || w == 16 || w == 32 || w == 64 }
func (Policy) CanWordCarry(w uint8) bool { return w == 32 || w == 64 }
func (e *ByteAssembler) MulHigh(w uint8, d, a, b machine.Register) {
	if w == 64 {
		e.words = append(e.words, 0x9bc07c00|uint32(b)<<16|uint32(a)<<5|uint32(d))
	} else {
		e.Binary(ir.OpMul, 64, d, a, b)
		e.Shift(ir.OpShiftRight, 64, d, d, w)
	}
}
func (e *TextAssembler) MulHigh(w uint8, d, a, b machine.Register) {
	if w == 64 {
		e.line("umulh X%d, X%d, X%d", d, a, b)
	} else {
		e.Binary(ir.OpMul, 64, d, a, b)
		e.Shift(ir.OpShiftRight, 64, d, d, w)
	}
}
func (e *ByteAssembler) WordCarry(ins machine.Instruction) {
	width := ins.Bits
	sub := ins.Arithmetic == ir.OpSub
	flags := func(a, b machine.Register) {
		base := uint32(0xab000000)
		if sub {
			base = 0xeb000000
		}
		if width == 32 {
			base &^= 1 << 31
		}
		e.words = append(e.words, base|uint32(b)<<16|uint32(a)<<5|16)
	}
	if ins.Op == machine.OpCarryBit {
		if ins.CarryMode != machine.CarryFlags {
			flags(ins.B, ins.C)
		}
		base := uint32(0x9a9f37e0)
		if sub {
			base = 0x9a9f27e0
		}
		e.words = append(e.words, base|uint32(ins.A))
		return
	}
	switch ins.CarryMode {
	case machine.CarryOperands:
		flags(ins.D, ins.E)
	case machine.CarryValue, machine.CarryBitValue:
		e.BinaryImmediate(ir.OpAnd, 64, 16, ins.D, 1)
		if sub {
			e.words = append(e.words, 0xeb0003ff|16<<16)
		} else {
			e.words = append(e.words, 0xf100001f|1<<10|16<<5)
		}
	}
	base := uint32(0xba000000)
	if sub {
		base = 0xfa000000
	}
	if width == 32 {
		base &^= 1 << 31
	}
	e.words = append(e.words, base|uint32(ins.C)<<16|uint32(ins.B)<<5|uint32(ins.A))
}
func (e *TextAssembler) WordCarry(ins machine.Instruction) {
	r := "X"
	if ins.Bits == 32 {
		r = "W"
	}
	sub := ins.Arithmetic == ir.OpSub
	flags := func(a, b machine.Register) {
		op := "adds"
		if sub {
			op = "subs"
		}
		e.line("%s %s16, %s%d, %s%d", op, r, r, a, r, b)
	}
	if ins.Op == machine.OpCarryBit {
		if ins.CarryMode != machine.CarryFlags {
			flags(ins.B, ins.C)
		}
		cond := "hs"
		if sub {
			cond = "lo"
		}
		e.line("cset X%d, %s", ins.A, cond)
		return
	}
	switch ins.CarryMode {
	case machine.CarryOperands:
		flags(ins.D, ins.E)
	case machine.CarryValue, machine.CarryBitValue:
		e.BinaryImmediate(ir.OpAnd, 64, 16, ins.D, 1)
		if sub {
			e.line("cmp XZR, X16")
		} else {
			e.line("cmp X16, #1")
		}
	}
	op := "adcs"
	if sub {
		op = "sbcs"
	}
	e.line("%s %s%d, %s%d, %s%d", op, r, ins.A, r, ins.B, r, ins.C)
}
func wordCarrySize(ins machine.Instruction) int {
	n := 1
	if ins.Op == machine.OpCarryBit {
		if ins.CarryMode != machine.CarryFlags {
			n++
		}
	} else {
		switch ins.CarryMode {
		case machine.CarryOperands:
			n++
		case machine.CarryValue, machine.CarryBitValue:
			n += 2
		}
	}
	return n * 4
}
