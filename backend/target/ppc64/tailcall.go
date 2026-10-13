package ppc64

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// tailCall expands OpTailCall: the frame is left as OpFrameLeave leaves it,
// restoring LR and this function's TOC, then mtctr r12; bctr branches to
// the C callee's global entry in r12. The original caller restores its TOC
// after its own indirect call returns.
func (p *expansion) tailCall(ins machine.Instruction, pc int) error {
	if p.emitter.stack != abi.StackPlatform {
		return machine.ErrUnsupportedOperation
	}
	if ins.Imm != 0 {
		if err := p.operation(machine.Instruction{Op: machine.OpFrameLeave, Imm: ins.Imm}, pc); err != nil {
			return err
		}
	}
	p.emit(Instruction{Op: Mtctr, A: 12})
	p.emit(Instruction{Op: Bctr})
	return nil
}
