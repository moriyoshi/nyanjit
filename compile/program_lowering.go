package compile

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/abi"
)

// emitProgramAssembly runs instruction selection, allocation, and physical emission,
// then assembles the emitted instructions with a target-specific text assembler.
// No machine code or executable memory is produced.
func emitProgramAssembly(b *loweringInput, options CompileOptions) (string, error) {
	if b != nil && b.control != nil {
		artifact, _, err := lowerControlProgram(b, options, emitText)
		return artifact.assembly, err
	}
	if b == nil {
		return "", ErrInvalidFunction
	}
	result, err := lowerFunctionMode(b.code, b.leaves, b.arity, b.result, b.resultParts, options, emitText, b)
	if err != nil {
		return "", err
	}
	return result.assembly, nil
}

// prepareGoProgram lowers a Go-managed function with either entry ABI.
// Its generated frame is registered with this exact Go runtime at Bind time.
// Values must not contain Go pointers. Registered code remains mapped for the
// lifetime of the process because the runtime cannot unload module metadata.
func prepareGoProgram(b *loweringInput, options CompileOptions) (*CodeTemplate, error) {
	if b == nil {
		return nil, ErrInvalidFunction
	}
	if options.ABI != ABIGo && options.ABI != ABIC {
		return nil, ErrUnsupportedABI
	}
	if !options.AssumePointerFree {
		return nil, ErrPointerMapsRequired
	}
	if options.Stack != StackPlatform || options.StackPointer != StackPointerReserved {
		return nil, ErrStackRequired
	}
	for _, ins := range b.code {
		if ins.Op == opSyscall {
			return nil, ErrExternalCallOnly
		}
	}
	options.ManagedCalls = true
	emission, err := emitTemplateProgram(b, options)
	if err != nil {
		return nil, err
	}
	target := newMachineTarget(options)
	if target == nil {
		return nil, ErrUnsupported
	}
	maxManagedFrame := target.ManagedFrameLimit()
	switch {
	case maxManagedFrame == 0:
		return nil, fmt.Errorf("%s has no managed frame support: %w", target.Architecture(), ErrBackendLimit)
	case emission.Report.WorkspaceBytes != 0:
		return nil, fmt.Errorf("managed entry requires %d workspace bytes: %w", emission.Report.WorkspaceBytes, ErrBackendLimit)
	case emission.Report.StackBytes > maxManagedFrame:
		return nil, fmt.Errorf("%s managed frame needs %d bytes, limit %d: %w", target.Architecture(), emission.Report.StackBytes, maxManagedFrame, ErrBackendLimit)
	case emission.Report.StackBytes == 0 && emission.Report.HasFunctionCalls:
		return nil, fmt.Errorf("managed calls require a nonempty frame: %w", ErrBackendLimit)
	case emission.Report.HasSystemCalls:
		return nil, fmt.Errorf("managed entry cannot make system calls: %w", ErrBackendLimit)
	}
	return newCodeTemplate(emission, b.arity, b.paramBits, options.ABI, false, true), nil
}

// prepareProgram lowers a callable leaf function once. Outbound calls and syscalls
// require an external execution boundary or PrepareForeign.
func prepareProgram(b *loweringInput, options CompileOptions) (*CodeTemplate, error) {
	if b == nil {
		return nil, ErrInvalidFunction
	}
	for _, ins := range b.code {
		if ins.Op == opCallC || ins.Op == opCallGo || ins.Op == opSyscall {
			return nil, ErrExternalCallOnly
		}
	}
	options.ManagedCalls = false
	emission, err := emitTemplateProgram(b, options)
	if err != nil {
		return nil, err
	}
	return newCodeTemplate(emission, b.arity, b.paramBits, options.ABI, false, false), nil
}

// prepareForeignProgram lowers a C ABI function that may call C ABI callbacks.
func prepareForeignProgram(b *loweringInput, options CompileOptions) (*CodeTemplate, error) {
	if b == nil {
		return nil, ErrInvalidFunction
	}
	if len(b.resultParts) != 0 {
		return nil, ErrBackendLimit
	}
	if b.control != nil {
		for _, step := range b.control.Steps {
			if len(step.ResultParts) != 0 {
				return nil, ErrBackendLimit
			}
		}
	}
	if options.ABI != ABIC {
		return nil, ErrUnsupportedABI
	}
	if options.Stack != StackPlatform || options.StackPointer != StackPointerReserved {
		return nil, ErrStackRequired
	}
	for _, ins := range b.code {
		if ins.Op == opCallGo || ins.Op == opSyscall {
			return nil, ErrExternalCallOnly
		}
	}
	options.ManagedCalls = false
	emission, err := emitTemplateProgram(b, options)
	if err != nil {
		return nil, err
	}
	return newCodeTemplate(emission, b.arity, b.paramBits, ABIC, true, false), nil
}

// emitTemplateProgram lowers a Program once and records all symbolic relocations.
// Unbound templates must be linked before execution.
func emitTemplateProgram(b *loweringInput, options CompileOptions) (EmissionTemplate, error) {
	if b != nil && b.control != nil {
		artifact, report, err := lowerControlProgram(b, options, emitNative)
		if err != nil {
			return EmissionTemplate{}, err
		}
		report.VectorOperations = append([]abi.VectorOperation(nil), b.vectorOperations...)
		return EmissionTemplate{Bytes: artifact.code, Report: report, Relocations: artifact.relocations, goUnwind: artifact.goUnwind}, nil
	}
	if b == nil {
		return EmissionTemplate{}, ErrInvalidFunction
	}
	result, err := lowerFunction(b.code, b.leaves, b.arity, b.result, b.resultParts, options, b)
	if err != nil {
		return EmissionTemplate{}, err
	}
	result.report.VectorOperations = append([]abi.VectorOperation(nil), b.vectorOperations...)
	return EmissionTemplate{Bytes: result.code, Report: result.report, Relocations: result.relocations, goUnwind: result.goUnwind}, nil
}
