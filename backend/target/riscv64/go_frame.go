package riscv64

import (
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// GoFrame describes a laid-out RV64 function to the Go runtime unwinder.
// code holds the final instructions with their PCs, codeBytes is the encoded
// size and frameBytes the size given to OpFrameEnter and OpFrameLeave. The
// result has CodeBytes, FrameBytes and SP set; the caller sets Name, ArgBytes
// and PointerFree before goregister.Register. The return address is stored at 0(SP) after the prologue. GoFrame returns
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
			for remaining := frameBytes; remaining > 0; {
				remaining -= min(remaining, 2032)
				pc += 4
				if err := setSP(pc, frameBytes-remaining); err != nil {
					return goregister.Frame{}, err
				}
			}
		case machine.OpFrameLeave:
			if int(ins.Imm) != frameBytes || delta != frameBytes {
				return goregister.Frame{}, goregister.ErrInvalidFrame
			}
			pc += 4 // Restore the return address before changing SP.
			for remaining := frameBytes; remaining > 0; {
				remaining -= min(remaining, 2032)
				pc += 4
				if err := setSP(pc, remaining); err != nil {
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
