package ppc64

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// GoFrame describes a laid-out ppc64le function to the Go runtime unwinder.
// code holds the final instructions with their PCs, codeBytes is the encoded
// size and frameBytes the size given to OpFrameEnter and OpFrameLeave. The
// result has CodeBytes, FrameBytes and SP set; the caller sets Name, ArgBytes
// and PointerFree before goregister.Register. Registered frames keep LR at 0(SP); SP points follow the concrete
// instructions each frame operation expands to. GoFrame returns
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
	profile, err := abi.NewPPC64Profile("ppc64le", abi.Go)
	if err != nil {
		return goregister.Frame{}, err
	}
	emitter, err := NewEmitter(profile, abi.StackPlatform)
	if err != nil {
		return goregister.Frame{}, err
	}
	for index, located := range code {
		ins, pc := located.Instruction, located.PC
		switch ins.Op {
		case machine.OpFrameEnter, machine.OpFrameLeave:
			if int(ins.Imm) != frameBytes || ins.Op == machine.OpFrameEnter && delta != 0 || ins.Op == machine.OpFrameLeave && delta != frameBytes {
				return goregister.Frame{}, goregister.ErrInvalidFrame
			}
			sequence, err := emitter.Expand(ins, pc)
			if err != nil {
				return goregister.Frame{}, err
			}
			found := false
			for _, native := range sequence {
				if native.A == 1 && (native.Op == Addi || native.Op == Add) {
					next := 0
					if ins.Op == machine.OpFrameEnter {
						next = frameBytes
					}
					if err := setSP(native.PC+4, next); err != nil {
						return goregister.Frame{}, err
					}
					found = true
				}
			}
			if !found {
				return goregister.Frame{}, goregister.ErrInvalidFrame
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
