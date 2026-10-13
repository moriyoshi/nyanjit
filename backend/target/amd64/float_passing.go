package amd64

import "github.com/moriyoshi/nyanjit/backend/machine"

// callCSize is the size of an OpCallC: AL is set by a 2-byte XOR for no FP
// arguments, otherwise by a 5-byte MOV EAX, imm32.
func callCSize(i machine.Instruction) int {
	switch {
	case i.Imm == 0:
		return 5
	case i.Imm <= 8:
		return 8
	}
	return 0
}

// CallCInstruction implements OpCallC. A System V variadic callee reads AL as
// an upper bound on the XMM argument registers used; Imm (at most 8) is that
// count. Then CALL R11.
func (e *ByteAssembler) CallCInstruction(i machine.Instruction) {
	if !e.require(i.Imm <= 8) {
		return
	}
	if i.Imm == 0 {
		e.CallC()
		return
	}
	e.code = append(e.code, 0xB8, byte(i.Imm), 0, 0, 0) // MOV EAX, imm32
	e.code = append(e.code, 0x41, 0xFF, 0xD3)           // CALL R11
}

func (e *TextAssembler) CallCInstruction(i machine.Instruction) {
	if !e.valid(func(enc *ByteAssembler) { enc.CallCInstruction(i) }) {
		return
	}
	if i.Imm == 0 {
		e.line(2, "xor EAX, EAX")
	} else {
		e.line(5, "mov EAX, %d", i.Imm)
	}
	e.line(3, "call R11")
}
