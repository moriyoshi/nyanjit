package arm64

import (
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// GoFrame describes a laid-out arm64 function to the Go runtime unwinder.
// code holds the final instructions with their PCs, codeBytes is the encoded
// size and frameBytes the size given to OpFrameEnter and OpFrameLeave. The
// result has CodeBytes, FrameBytes and SP set; the caller sets Name, ArgBytes
// and PointerFree before goregister.Register. A small prologue changes SP in its first instruction; a large one
// builds the frame through X17 and changes SP after both links are saved. GoFrame returns
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
			changePC := pc + 4
			if frameBytes > 240 {
				changePC = pc + FrameEnterSize(frameBytes) - 4
			}
			if err := setSP(changePC, frameBytes); err != nil {
				return goregister.Frame{}, err
			}
		case machine.OpFrameLeave:
			if int(ins.Imm) != frameBytes || delta != frameBytes {
				return goregister.Frame{}, goregister.ErrInvalidFrame
			}
			if frameBytes <= 240 {
				if err := setSP(pc+8, 0); err != nil {
					return goregister.Frame{}, err
				}
				continue
			}
			at := pc + 8 // FP and LR loads precede the ADD SP instructions.
			for remaining := frameBytes; remaining > 0; {
				step := min(remaining, 4080)
				remaining -= step
				at += 4
				if err := setSP(at, remaining); err != nil {
					return goregister.Frame{}, err
				}
			}
		case machine.OpReturn:
			// A later label can be reached by a branch with the frame still
			// open, even though linear layout follows the return.
			if frameBytes != 0 && index+1 < len(code) {
				if err := setSP(pc+4, frameBytes); err != nil {
					return goregister.Frame{}, err
				}
			}
		}
	}
	return frame, nil
}
