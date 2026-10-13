package compile

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/ir"
)

// The baseline reads only the flags of its own compares, never those of a
// mask, so it zero-extends an 8- or 16-bit result with MOVZX, which is 3
// bytes, wherever the register has that width: every register for 16 bits
// and EAX..EBX for 8. The assemblers' PreserveFlags forms encode it. Of the
// byte registers the baseline allocates only EBX, so an 8-bit result bound
// for ESI, EDI or EBP is computed in EAX, its temporary, and moved there by
// MOVZX from AL instead of being masked with the 6-byte AND.

// i386Extends reports whether MOVZX reads a w-bit value from register r.
func i386Extends(r backend.Register, w uint8) bool { return w == 16 || w == 8 && r < 4 }

// i386ByteTemp reports whether a w-bit result for d is computed in EAX and
// zero-extended from AL: w is 8 and d has no byte register.
func i386ByteTemp(d backend.Register, w uint8) bool { return w == 8 && !i386Extends(d, w) }

// narrow zero-extends the low w bits of s into d, through EAX at 8 bits when
// neither has a byte register.
func (x *i386Enc) narrow(d, s backend.Register, w uint8) {
	if i386ByteTemp(d, w) && !i386Extends(s, w) {
		x.a.Move(gEAX, s)
		s = gEAX
	}
	if (w == 8 || w == 16) && (i386Extends(s, w) || i386Extends(d, w)) {
		x.a.BinaryImmediateNoFlags(op.OpAnd, 32, d, s, uint64(1)<<w-1)
		return
	}
	x.a.Narrow(d, s, w)
}

// binary is Binary with MOVZX as the mask of an 8- or 16-bit result, after
// a LEA for an addition into another register. An 8-bit result for ESI, EDI
// or EBP is computed in EAX (after a LEA for an addition) and moved there by
// MOVZX; when right is EAX, a commutative operation reads it as left, and
// another keeps the AND.
func (x *i386Enc) binary(o op.Opcode, w uint8, d, left, right backend.Register) {
	if i386ByteTemp(d, w) && right == gEAX && commutative(ir.Opcode(o)) {
		left, right = right, left
	}
	if i386ByteTemp(d, w) && right != gEAX {
		if i386BinaryLEA(ir.Opcode(o), 32, gEAX, left, right) {
			x.a.BinaryNoFlags(o, 32, gEAX, left, right)
		} else {
			x.a.Binary(o, 32, gEAX, left, right)
		}
		x.narrow(d, gEAX, w)
		return
	}
	if (w == 8 || w == 16) && i386Extends(d, w) {
		x.a.BinaryNoFlags(o, w, d, left, right)
		return
	}
	x.a.Binary(o, w, d, left, right)
}

// i386BinaryLEA reports whether binary computes o with a LEA: an addition
// into another register, at 32 bits or with a MOVZX after it.
func i386BinaryLEA(o ir.Opcode, w uint8, d, left, right backend.Register) bool {
	return o == ir.OpAdd && (w == 32 || i386Extends(d, w)) && d != left && !(left == gESP && right == gESP)
}

// shift is Shift with MOVZX as the mask of an 8- or 16-bit SHL or SHR,
// computed in EAX for an 8-bit SHL or SHR into ESI, EDI or EBP.
func (x *i386Enc) shift(o op.Opcode, w uint8, d, s backend.Register, count uint8) {
	if i386ByteTemp(d, w) && o != op.OpShiftRightArithmetic {
		x.a.Shift(o, 32, gEAX, s, count)
		x.narrow(d, gEAX, w)
		return
	}
	if (w == 8 || w == 16) && o != op.OpShiftRightArithmetic && i386Extends(d, w) {
		x.a.ShiftInstruction(machine.Instruction{Op: machine.OpShift, Arithmetic: o, Bits: w, A: d, B: s, Imm: uint64(count), PreserveFlags: true})
		return
	}
	x.a.Shift(o, w, d, s, count)
}

// i386MaskSize is the size of the mask of a w-bit result in r, as the
// assemblers' forms encode it: none at 32 bits, MOVZX, or AND (EAX always
// has a byte register).
func i386MaskSize(r int8, w uint8) int {
	switch {
	case w >= 32:
		return 0
	case i386Extends(backend.Register(r), w):
		return 3
	}
	return 6
}

// i386BinarySize is the size of binary(o, w, d, left, right) whose
// operation takes code bytes.
func i386BinarySize(o ir.Opcode, w uint8, d, left, right int8, code int) int {
	if i386ByteTemp(backend.Register(d), w) && right == x86EAX && commutative(o) {
		left, right = right, left
	}
	switch {
	case i386ByteTemp(backend.Register(d), w) && right != x86EAX:
		// The operation into EAX, then MOVZX d, AL.
		if i386BinaryLEA(o, 32, gEAX, backend.Register(left), backend.Register(right)) {
			return i386IndexedLEASize(left, right) + 3
		}
		n := code + 3
		if left != x86EAX {
			n += 2
		}
		return n
	case w < 32 && i386BinaryLEA(o, w, backend.Register(d), backend.Register(left), backend.Register(right)):
		return i386IndexedLEASize(left, right) + 3
	}
	n := code + i386MaskSize(d, w)
	if d != left {
		n += 2
	}
	return n
}

// i386IndexedLEASize is the size of LEA [left + right]: an EBP base needs a
// disp8 unless the operands swap.
func i386IndexedLEASize(left, right int8) int {
	if left == x86EBP && right == x86EBP {
		return 4
	}
	return 3
}

// immediateByte is the 8-bit addition or subtraction of an immediate into
// d, which has no byte register: LEA EAX, [s + disp], then MOVZX d, AL.
func (x *i386Enc) immediateByte(o op.Opcode, d, s backend.Register, imm uint64) {
	x.a.BinaryImmediateNoFlags(o, 32, gEAX, s, imm)
	x.narrow(d, gEAX, 8)
}

// i386ImmediateByteSize is the size of immediateByte with source s.
func i386ImmediateByteSize(o ir.Opcode, s int8, imm uint64) int {
	disp := int32(uint32(imm))
	if o == ir.OpSub {
		disp = -disp
	}
	n := 2 + 3 // LEA opcode and ModRM; MOVZX
	if s == x86ESP {
		n++ // SIB
	}
	switch {
	case disp == 0 && s != x86EBP:
	case disp >= -128 && disp <= 127:
		n++
	default:
		n += 4
	}
	return n
}
