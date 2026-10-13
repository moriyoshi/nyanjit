package compile

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// baselinePrefetch encodes OpPrefetch of the address in general register
// address through a target encoder; kind is the ir.PrefetchKind.
func baselinePrefetch(a any, address backend.Register, kind uint64) error {
	encoder, ok := a.(interface {
		Prefetch(machine.Instruction) error
	})
	if !ok {
		return fmt.Errorf("baseline tier: prefetch: %w", ErrUnsupported)
	}
	return encoder.Prefetch(machine.Instruction{Op: machine.OpPrefetch, A: address, Imm: kind})
}
