package compile

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// recordImplicitWrites adds architecture expansion registers to the emitter's
// write set before an assembler renders the physical stream.
func recordImplicitWrites(target machineTarget, program physicalProgram, written *registerWrites) {
	provider, ok := target.(interface {
		ImplicitWrites(machine.Instruction) []backend.Register
	})
	if !ok {
		return
	}
	for _, located := range program.insns {
		for _, reg := range provider.ImplicitWrites(located.ins) {
			written[reg] = true
		}
	}
}

// Register ordinals fit in uint8 on every target.
type registerWrites [256]bool
