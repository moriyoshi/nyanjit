package ppc64

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// prefetch expands OpPrefetch to dcbt 0, rA or dcbtst 0, rA.
func (p *expansion) prefetch(ins machine.Instruction) error {
	if ins.Imm > uint64(op.PrefetchWrite) {
		return machine.ErrInvalidInstruction
	}
	if ins.A > 31 {
		return machine.ErrUnsupportedOperation
	}
	native := dcbt
	if op.PrefetchKind(ins.Imm) == op.PrefetchWrite {
		native = dcbtst
	}
	p.emit(Instruction{Op: native, B: ins.A})
	return nil
}
