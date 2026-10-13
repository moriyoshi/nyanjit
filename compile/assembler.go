package compile

import (
	"errors"
	"github.com/moriyoshi/nyanjit/machine"
)

// machineAssembler is an architecture assembler with a root artifact adapter.
type machineAssembler interface {
	SetPC(int)
	AssembledSize() int
	artifact() emissionArtifact
}

func assembleProgram(program physicalProgram, assembler machineAssembler) (emissionArtifact, error) {
	for _, located := range program.insns {
		if assembler.AssembledSize() != located.pc {
			return emissionArtifact{}, ErrInvalidFunction
		}
		assembler.SetPC(located.pc)
		var err error
		if targetEmitter, ok := assembler.(interface {
			emitInstruction(machine.Instruction) error
		}); ok {
			err = targetEmitter.emitInstruction(located.ins)
		} else if visitor, ok := assembler.(machine.Assembler); ok {
			err = machine.Visit(located.ins, visitor)
		} else {
			err = machine.ErrUnsupportedOperation
		}
		if err != nil {
			if errors.Is(err, machine.ErrUnsupportedOperation) {
				return emissionArtifact{}, ErrUnsupported
			}
			return emissionArtifact{}, err
		}
	}
	if assembler.AssembledSize() != program.codeBytes {
		return emissionArtifact{}, ErrInvalidFunction
	}
	artifact := assembler.artifact()
	artifact.stackBytes = program.stackBytes
	return artifact, nil
}
