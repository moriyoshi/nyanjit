package amd64

import "github.com/moriyoshi/nyanjit/backend/machine"

// OpTailCall leaves the frame and jumps through R11, the C call target. As
// CallC does, it clears AL, the vector-register count of a variadic callee.

// tailCallSize is the byte size of a valid OpTailCall that leaves frame
// bytes.
func tailCallSize(frame uint64) int {
	if frame == 0 {
		return 5
	}
	return 12
}

func (e *ByteAssembler) TailCall(i machine.Instruction) error {
	if !e.require(validInstructionRegisters(&i) && i.Imm <= 1<<20) {
		return e.err
	}
	if i.Imm != 0 {
		e.FrameLeave(int(i.Imm))
	}
	e.code = append(e.code, 0x31, 0xC0, 0x41, 0xFF, 0xE3) // xor EAX, EAX; jmp R11
	return nil
}

func (e *TextAssembler) TailCall(i machine.Instruction) error {
	if err := e.validErr(func(enc *ByteAssembler) error { return enc.TailCall(i) }); err != nil {
		return err
	}
	if i.Imm != 0 {
		e.line(7, "add RSP, %d", i.Imm)
	}
	e.line(2, "xor EAX, EAX")
	e.line(3, "jmp R11")
	return nil
}
