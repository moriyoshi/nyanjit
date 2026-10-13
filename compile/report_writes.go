package compile

import "github.com/moriyoshi/nyanjit/machine"

// recordImplicitWrites adds architecture expansion registers to the emitter's
// write set before an assembler renders the physical stream.
func recordImplicitWrites(target machineTarget, program physicalProgram, written map[physReg]bool) {
	provider, ok := target.(interface {
		ImplicitWrites(machine.Instruction) []machine.Register
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
