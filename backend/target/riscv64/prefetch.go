package riscv64

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// prefetchWords are the Zicbop prefetch.r and prefetch.w hints with offset 0.
// They are ORI x0, rs1, 1 and ORI x0, rs1, 3, so a core without Zicbop runs
// them as no-ops; neither needs a CPU profile.
var prefetchWords = [...]uint32{
	op.PrefetchRead:  0x00106013,
	op.PrefetchWrite: 0x00306013,
}

func validPrefetch(i machine.Instruction) bool {
	return i.Imm <= uint64(op.PrefetchWrite) && validGeneralRegisters(i.A)
}

func (e *ByteAssembler) Prefetch(i machine.Instruction) error {
	if !e.require(validInstructionRegisters(&i) && validPrefetch(i)) {
		return e.err
	}
	e.words = append(e.words, prefetchWords[i.Imm]|uint32(i.A)<<15)
	return nil
}

// prefetchText names a Zicbop prefetch.r or prefetch.w hint word.
func prefetchText(word uint32) (string, bool) {
	if word&0x01f07fff != prefetchWords[op.PrefetchRead] && word&0x01f07fff != prefetchWords[op.PrefetchWrite] {
		return "", false
	}
	name := "prefetch.r"
	if word>>20&31 == 3 {
		name = "prefetch.w"
	}
	return fmt.Sprintf("%s %d(x%d)", name, signBits(word>>25, 7)<<5, word>>15&31), true
}
