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
	var batch [decodeBatch]machine.Instruction
	for n := 0; n < program.code.count(); {
		insns := program.code.decodeFrom(n, batch[:])
		for i := range insns {
			for _, reg := range provider.ImplicitWrites(insns[i]) {
				written[reg] = true
			}
		}
		n += len(insns)
	}
}

// recordResolvedImplicitWrites is recordImplicitWrites over resolved
// instructions, for a program that will hold them unchanged.
func recordResolvedImplicitWrites(target machineTarget, resolved []machine.LocatedInstruction, written *registerWrites) {
	provider, ok := target.(interface {
		ImplicitWrites(machine.Instruction) []backend.Register
	})
	if !ok {
		return
	}
	for _, located := range resolved {
		for _, reg := range provider.ImplicitWrites(located.Instruction) {
			written[reg] = true
		}
	}
}

// Register ordinals fit in uint8 on every target.
type registerWrites [256]bool
