package loong64

import "github.com/moriyoshi/nyanjit/backend/machine"

// OpTailCall leaves the frame, restoring ra, and jumps through r20 (t8), the
// C call target, with jirl r0.

func (e *ByteAssembler) TailCall(i machine.Instruction) error {
	if !e.require(validInstructionRegisters(i) && i.Imm <= 1<<20) {
		return e.err
	}
	if i.Imm != 0 {
		e.FrameLeave(int(i.Imm))
	}
	e.emit(0x4c000280, "jirl r0, r20, 0")
	return nil
}
