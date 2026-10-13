package compile

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

// emitProgramAssemblyFromIR emits architecture assembly text from a Program snapshot.
func emitProgramAssemblyFromIR(program *ir.Program, options CompileOptions) (string, error) {
	input, err := inputFromProgram(program, options)
	if err != nil {
		return "", err
	}
	return emitProgramAssembly(input, options)
}

// prepareProgramFromIR makes a relocatable executable template from a Program.
func prepareProgramFromIR(program *ir.Program, options CompileOptions) (*CodeTemplate, error) {
	input, err := inputFromProgram(program, options)
	if err != nil {
		return nil, err
	}
	return prepareProgram(input, options)
}

// prepareGoProgramFromIR prepares a Program for registered Go ABI calls.
func prepareGoProgramFromIR(program *ir.Program, options CompileOptions) (*CodeTemplate, error) {
	options.ManagedCalls = true
	input, err := inputFromProgram(program, options)
	if err != nil {
		return nil, err
	}
	return prepareGoProgram(input, options)
}

// prepareForeignProgramFromIR prepares a Program for C ABI callback calls.
func prepareForeignProgramFromIR(program *ir.Program, options CompileOptions) (*CodeTemplate, error) {
	input, err := inputFromProgram(program, options)
	if err != nil {
		return nil, err
	}
	return prepareForeignProgram(input, options)
}

// emitProgramTemplateFromIR lowers a Program while retaining symbol relocations.
func emitProgramTemplateFromIR(program *ir.Program, options CompileOptions) (EmissionTemplate, error) {
	input, err := inputFromProgram(program, options)
	if err != nil {
		return EmissionTemplate{}, err
	}
	return emitTemplateProgram(input, options)
}

// loweringInput is the validated, detached input to the machine
// backend. Compilation never reconstructs a mutable Builder.
type loweringInput struct {
	code             []instruction
	leaves           []scopeEvent
	control          *ir.Control
	arity            int
	result           int
	resultParts      []int
	vectorOperations []abi.VectorOperation
	// paramBits holds each entry parameter's scalar width; zero marks a
	// vector parameter.
	paramBits []uint8
	source    *ir.Program
	homes     *machine.HomePlan
}

func inputFromProgram(program *ir.Program, options ...CompileOptions) (*loweringInput, error) {
	if program == nil {
		return nil, ErrInvalidFunction
	}
	snapshot := program.Clone()
	if err := snapshot.Validate(); err != nil {
		return nil, ErrInvalidFunction
	}
	selectedOptions := CompileOptions{}
	if len(options) != 0 {
		selectedOptions = options[0]
	}
	paramBits := entryParamBits(snapshot)
	snapshot, vectorOperations, err := legalizeVectors(snapshot, selectedOptions)
	if err != nil {
		return nil, err
	}
	homes := planVariableHomes(snapshot, newMachineTarget(selectedOptions), selectedOptions.Stack)
	input := &loweringInput{
		source: snapshot, homes: homes,
		code: snapshot.Instructions, leaves: snapshot.ScopeExits, vectorOperations: vectorOperations,
		arity: snapshot.Arity, result: snapshot.Result, resultParts: snapshot.ResultParts,
		paramBits: paramBits,
	}
	if snapshot.Control != nil {
		input.control = snapshot.Control
	}
	return input, nil
}

// entryParamBits lists entry parameter widths in parameter order, or nil when
// the parameters do not match the Program's arity.
func entryParamBits(program *ir.Program) []uint8 {
	var bits []uint8
	for _, ins := range program.Instructions {
		if ins.Op != ir.OpParam {
			continue
		}
		width := ins.Bits
		if width == 0 {
			width = 64
		}
		if ins.Type.Kind == abi.VectorKind {
			width = 0
		}
		bits = append(bits, width)
	}
	if len(bits) != program.Arity {
		return nil
	}
	return bits
}
