package riscv64

import "github.com/moriyoshi/nyanjit/backend/machine"

// OpTailCall leaves the frame, restoring ra, and jumps through t6 (x31), the
// C call target, with jalr x0.

func (e *ByteAssembler) TailCall(i machine.Instruction) error {
	if !e.require(validInstructionRegisters(&i) && i.Imm <= 1<<20) {
		return e.err
	}
	if i.Imm != 0 {
		e.FrameLeave(int(i.Imm))
	}
	e.words = append(e.words, iWord(0, 31, 0, 0, 0x67)) // jalr x0, 0(x31)
	return nil
}

// tailCallSize is the byte size of OpTailCall leaving frame bytes.
func tailCallSize(frame uint64) int {
	if frame == 0 {
		return 4
	}
	return FrameLeaveSize(int(frame)) + 4
}
