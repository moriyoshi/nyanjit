package loong64

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// prefetchHints are the PRELD hints: 0 loads and 8 stores into L1.
var prefetchHints = [...]uint32{
	op.PrefetchRead:  0,
	op.PrefetchWrite: 8,
}

// Prefetch implements OpPrefetch as preld hint, rj, 0. PRELD never faults.
func (e *ByteAssembler) Prefetch(i machine.Instruction) error {
	if !e.require(validInstructionRegisters(i) && i.Imm <= uint64(op.PrefetchWrite)) {
		return e.err
	}
	e.emit(0x2ac00000|uint32(i.A)<<5|prefetchHints[i.Imm], "preld %d, r%d, 0", prefetchHints[i.Imm], i.A)
	return nil
}
