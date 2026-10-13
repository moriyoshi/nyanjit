package arm64

import "github.com/moriyoshi/nyanjit/backend/machine"

// OpTailCall leaves the frame, restoring FP and LR, and branches through
// X16, the C call target, with BR: LR stays the caller's return address.

// tailCallSize is the byte size of OpTailCall, or 0 for an invalid frame.
func tailCallSize(frame uint64) int {
	if frame == 0 {
		return 4
	}
	if frame > 1<<20 || !validFrame(int(frame)) {
		return 0
	}
	return FrameLeaveSize(int(frame)) + 4
}

func (e *ByteAssembler) TailCall(i machine.Instruction) error {
	if !e.require(validVisitFields(&i) && validInstructionRegisters(&i) && tailCallSize(i.Imm) != 0) {
		return e.err
	}
	if i.Imm != 0 {
		e.FrameLeave(int(i.Imm))
	}
	e.words = append(e.words, 0xD61F0200) // BR X16
	return nil
}

func (e *TextAssembler) TailCall(i machine.Instruction) error {
	if err := e.validErr(func(enc *ByteAssembler) error { return enc.TailCall(i) }); err != nil {
		return err
	}
	if i.Imm != 0 {
		e.FrameLeave(int(i.Imm))
	}
	e.line("br X16")
	return nil
}
