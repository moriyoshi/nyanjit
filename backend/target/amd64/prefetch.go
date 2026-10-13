package amd64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// OpPrefetch is PREFETCHT0 for both kinds. PREFETCHW needs the PRFCHW CPUID
// bit, which the baseline profile does not promise.

func validPrefetch(i machine.Instruction) bool {
	return i.Imm <= 0xff && op.PrefetchKind(i.Imm).Valid() && validGeneralRegisters(i.A)
}

// prefetchSize is the byte size of PREFETCHT0 [base].
func prefetchSize(base backend.Register) int {
	size := 3
	if base >= 8 {
		size++ // REX.B
	}
	if base&7 == 4 || base&7 == 5 {
		size++ // SIB, or disp8 = 0
	}
	return size
}

func (e *ByteAssembler) Prefetch(i machine.Instruction) error {
	if !e.require(validInstructionRegisters(&i) && validPrefetch(i)) {
		return e.err
	}
	if i.A >= 8 {
		e.code = append(e.code, 0x41)
	}
	switch base := byte(i.A & 7); base {
	case 4:
		e.code = append(e.code, 0x0f, 0x18, 0x0c, 0x24)
	case 5:
		e.code = append(e.code, 0x0f, 0x18, 0x4d, 0)
	default:
		e.code = append(e.code, 0x0f, 0x18, 0x08|base)
	}
	return nil
}

func (e *TextAssembler) Prefetch(i machine.Instruction) error {
	if err := e.validErr(func(enc *ByteAssembler) error { return enc.Prefetch(i) }); err != nil {
		return err
	}
	e.line(prefetchSize(i.A), "prefetcht0 byte ptr [%s]", amd64Name(i.A))
	return nil
}
