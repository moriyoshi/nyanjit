package compile

import (
	"errors"

	"github.com/moriyoshi/nyanjit/backend/machine"
)

// machineAssembler is an architecture assembler with a root artifact adapter.
type machineAssembler interface {
	SetPC(int)
	AssembledSize() int
	artifact() emissionArtifact
}

func assembleProgram(program physicalProgram, assembler machineAssembler) (emissionArtifact, error) {
	var batch [decodeBatch]machine.Instruction
	for first := 0; first < program.code.count(); {
		insns := program.code.decodeFrom(first, batch[:])
		for i := range insns {
			pc, ins := program.code.pcAt(first+i), &insns[i]
			if assembler.AssembledSize() != pc {
				return emissionArtifact{}, ErrInvalidFunction
			}
			assembler.SetPC(pc)
			var err error
			if targetEmitter, ok := assembler.(interface {
				emitInstruction(machine.Instruction) error
			}); ok {
				err = targetEmitter.emitInstruction(*ins)
			} else if visitor, ok := assembler.(machine.Assembler); ok {
				err = machine.Visit(*ins, visitor)
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
		first += len(insns)
	}
	if assembler.AssembledSize() != program.codeBytes {
		return emissionArtifact{}, ErrInvalidFunction
	}
	artifact := assembler.artifact()
	artifact.stackBytes = program.stackBytes
	return artifact, nil
}
