package i386

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// validX87Result checks an OpX87Result: an XMM register A, a 32- or 64-bit
// width, a direction in Imm and a non-negative spill slot.
func validX87Result(i machine.Instruction) bool {
	return backend.IsVectorRegister(i.A) && backend.VectorNumber(i.A) < 8 &&
		(i.Bits == 32 || i.Bits == 64) && i.Imm <= 1 && i.Slot >= 0
}

// X87Result implements OpX87Result. A cdecl float result travels in x87
// ST(0); generated code keeps floats in XMM registers. Imm 0 stores XMM A
// to the slot and loads it with FLD (a C return); Imm 1 pops ST(0) to the
// slot with FSTP and loads XMM A from it (after a C call). FLD quiets a
// signaling NaN.
func (e *ByteAssembler) X87Result(i machine.Instruction) error {
	if !e.require(validX87Result(i)) {
		return e.err
	}
	base := backend.Register(5)
	if e.stack == abi.StackPlatform {
		base = 4
	}
	disp := e.callArea + i.Slot*4
	prefix, x87 := byte(0xf2), byte(0xdd)
	if i.Bits == 32 {
		prefix, x87 = 0xf3, 0xd9
	}
	xmm := backend.Register(backend.VectorNumber(i.A))
	if i.Imm == 0 {
		e.code = appendBase32(append(e.code, prefix, 0x0f), 0x11, xmm, base, disp) // MOVSD [slot], XMM
		e.code = appendBase32(e.code, x87, 0, base, disp)                          // FLD [slot]
		return nil
	}
	e.code = appendBase32(e.code, x87, 3, base, disp)                          // FSTP [slot]
	e.code = appendBase32(append(e.code, prefix, 0x0f), 0x10, xmm, base, disp) // MOVSD XMM, [slot]
	return nil
}

func (e *TextAssembler) X87Result(i machine.Instruction) error {
	if !e.require(validX87Result(i)) {
		return e.err
	}
	base := backend.Register(5)
	if e.stack == abi.StackPlatform {
		base = 4
	}
	slot := fmt.Sprintf("[%s + %d]", name(base), e.callArea+i.Slot*4)
	move, size := "movsd", "qword"
	if i.Bits == 32 {
		move, size = "movss", "dword"
	}
	xmm := fmt.Sprintf("xmm%d", backend.VectorNumber(i.A))
	if i.Imm == 0 {
		e.record("%s %s, %s", move, slot, xmm)
		e.record("fld %s %s", size, slot)
	} else {
		e.record("fstp %s %s", size, slot)
		e.record("%s %s, %s", move, xmm, slot)
	}
	return e.ByteAssembler.X87Result(i)
}
