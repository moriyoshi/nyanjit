package i386

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// OpPrefetch is PREFETCHT0 for both kinds. PREFETCHW needs the PRFCHW CPUID
// bit, which the baseline profile does not promise.

func validPrefetch(i machine.Instruction) bool {
	return validInstructionRegisters(i) && i.Imm <= uint64(op.PrefetchWrite)
}

func (e *ByteAssembler) Prefetch(i machine.Instruction) error {
	if !e.require(validPrefetch(i)) {
		return e.err
	}
	e.code = append(e.code, 0x0f)
	e.code = appendBase(e.code, 0x18, 1, i.A, 0)
	return nil
}

func (e *TextAssembler) Prefetch(i machine.Instruction) error {
	if !e.require(validPrefetch(i)) {
		return e.err
	}
	e.record("prefetcht0 byte ptr [%s]", name(i.A))
	return e.ByteAssembler.Prefetch(i)
}
