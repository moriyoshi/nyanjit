package i386

import (
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// GoFrame describes a laid-out 386 function to the Go runtime unwinder.
// code holds the final instructions with their PCs, codeBytes is the encoded
// size and frameBytes the size given to OpFrameEnter and OpFrameLeave. The
// result has CodeBytes, FrameBytes and SP set; the caller sets Name, ArgBytes
// and PointerFree before goregister.Register. The prologue pushes four preserved registers and then adjusts SP;
// the runtime enters this frame only at a call return PC. GoFrame returns
// goregister.ErrInvalidFrame when the frame operations do not match
// frameBytes or leave SP outside the code.
func GoFrame(code []machine.LocatedInstruction, codeBytes, frameBytes int) (goregister.Frame, error) {
	frame := goregister.Frame{CodeBytes: codeBytes, FrameBytes: frameBytes, SP: []goregister.SPPoint{{PC: 0, Delta: 0}}}
	delta := 0
	setSP := func(pc, value int) error {
		if pc < 0 || pc > codeBytes || value < 0 || value > frameBytes {
			return goregister.ErrInvalidFrame
		}
		if last := &frame.SP[len(frame.SP)-1]; last.PC == pc {
			last.Delta = value
		} else {
			frame.SP = append(frame.SP, goregister.SPPoint{PC: pc, Delta: value})
		}
		delta = value
		return nil
	}
	for index, located := range code {
		ins, pc := located.Instruction, located.PC
		switch ins.Op {
		case machine.OpFrameEnter:
			if int(ins.Imm) != frameBytes || delta != 0 {
				return goregister.Frame{}, goregister.ErrInvalidFrame
			}
			if err := setSP(pc+10, frameBytes); err != nil {
				return goregister.Frame{}, err
			}
		case machine.OpFrameLeave:
			if int(ins.Imm) != frameBytes || delta != frameBytes {
				return goregister.Frame{}, goregister.ErrInvalidFrame
			}
			if err := setSP(pc+10, 0); err != nil {
				return goregister.Frame{}, err
			}
		case machine.OpReturn:
			// A later label can be reached by a branch with the frame still
			// open, even though linear layout follows the return.
			if frameBytes != 0 && index+1 < len(code) {
				if err := setSP(pc+1, frameBytes); err != nil {
					return goregister.Frame{}, err
				}
			}
		}
	}
	return frame, nil
}
