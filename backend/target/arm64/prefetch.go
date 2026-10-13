package arm64

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// prefetchOperations are the PRFM prfop fields: PLDL1KEEP and PSTL1KEEP.
var prefetchOperations = [...]struct {
	prfop uint32
	text  string
}{
	op.PrefetchRead:  {0b00000, "pldl1keep"},
	op.PrefetchWrite: {0b10000, "pstl1keep"},
}

// Prefetch implements OpPrefetch as PRFM <prfop>, [Xn]. A base of 31 is SP,
// as for the other memory forms.
func (e *ByteAssembler) Prefetch(i machine.Instruction) error {
	if !e.require(validVisitFields(&i) && validInstructionRegisters(&i)) {
		return e.err
	}
	e.words = append(e.words, 0xf9800000|uint32(i.A)<<5|prefetchOperations[i.Imm].prfop)
	return nil
}

func (e *TextAssembler) Prefetch(i machine.Instruction) error {
	if err := e.validErr(func(enc *ByteAssembler) error { return enc.Prefetch(i) }); err != nil {
		return err
	}
	e.line("prfm %s, [X%d]", prefetchOperations[i.Imm].text, i.A)
	return nil
}
