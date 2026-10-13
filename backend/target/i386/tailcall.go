package i386

import "github.com/moriyoshi/nyanjit/backend/machine"

// OpTailCall jumps through EAX, the C call target. C arguments are stack
// words, so it first copies the Disp words of the outgoing area at [ESP] to
// the incoming argument words above the return address, through ECX; the
// caller's words are then the callee's, and the caller pops them as its own.
// Then it leaves the frame of Imm bytes, which includes the callee-saved
// pushes.

func validTailCall(i machine.Instruction) bool {
	return validInstructionRegisters(i) && i.Disp <= 64 &&
		(i.Imm == 0 && i.Disp == 0 || i.Imm >= 16 && i.Imm <= 1<<20)
}

func (e *ByteAssembler) TailCall(i machine.Instruction) error {
	if !e.require(validTailCall(i)) {
		return e.err
	}
	for k := 0; k < int(i.Disp); k++ {
		e.code = appendBase(e.code, 0x8b, 1, 4, uint64(4*k))
		e.code = appendBase(e.code, 0x89, 1, 4, i.Imm+4+uint64(4*k))
	}
	if i.Imm != 0 {
		e.FrameLeave(int(i.Imm))
	}
	e.code = append(e.code, 0xff, 0xe0) // jmp EAX
	return nil
}

func (e *TextAssembler) TailCall(i machine.Instruction) error {
	if !e.require(validTailCall(i)) {
		return e.err
	}
	for k := 0; k < int(i.Disp); k++ {
		e.record("mov ECX, dword ptr [ESP + %d]; mov dword ptr [ESP + %d], ECX", 4*k, i.Imm+4+uint64(4*k))
	}
	if i.Imm != 0 {
		e.record("add ESP, %d; pop EBP; pop EDI; pop ESI; pop EBX", i.Imm-16)
	}
	e.record("jmp EAX")
	return e.ByteAssembler.TailCall(i)
}
