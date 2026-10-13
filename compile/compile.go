// Package compile lowers nyanjit IR programs to native code.
package compile

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

func Analyze(program *ir.Program, options abi.CompileOptions) (abi.EmissionReport, error) {
	template, err := EmitTemplate(program, options)
	if err != nil {
		return abi.EmissionReport{}, err
	}
	return template.Report, nil
}

// Preflight reports a complete Program's effects before execution.
func Preflight(program *ir.Program, options abi.CompileOptions) (abi.EmissionReport, error) {
	return Analyze(program, options)
}

func Emit(program *ir.Program, options abi.CompileOptions) ([]byte, abi.EmissionReport, error) {
	template, err := EmitTemplate(program, options)
	if err != nil {
		return nil, abi.EmissionReport{}, err
	}
	code, err := template.Link(nil)
	if err != nil {
		return nil, abi.EmissionReport{}, err
	}
	return code, template.Report, nil
}

// EmitAssembly lowers a program to a text listing without executable memory.
// options.Architecture selects a Linux target independently of the host;
// an empty Architecture retains the host platform and CPU defaults.
func EmitAssembly(program *ir.Program, options abi.CompileOptions) (string, error) {
	return emitProgramAssemblyFromIR(program, options)
}

func Compile(program *ir.Program, options abi.CompileOptions) (*Code, error) {
	template, err := Prepare(program, options)
	if err != nil {
		return nil, err
	}
	return template.Bind(nil)
}

func Prepare(program *ir.Program, options abi.CompileOptions) (*CodeTemplate, error) {
	return prepareProgramFromIR(program, options)
}

// PrepareGo compiles a Go-managed function with either C or Go entry ABI.
// A C entry must be reached from a Go-managed call chain, such as one entered
// through a Go runtime callback trampoline.
// Outbound Go calls are direct; outbound C calls use the runtime gateway.
func PrepareGo(program *ir.Program, options abi.CompileOptions) (*CodeTemplate, error) {
	return prepareGoProgramFromIR(program, options)
}

// PrepareForeign compiles a C entry that runs in a foreign call context and
// may call other C functions. Use PrepareGo for a runtime-managed C entry.
func PrepareForeign(program *ir.Program, options abi.CompileOptions) (*CodeTemplate, error) {
	return prepareForeignProgramFromIR(program, options)
}

func CompileGo(program *ir.Program, options abi.CompileOptions) (*Code, error) {
	template, err := PrepareGo(program, options)
	if err != nil {
		return nil, err
	}
	return template.Bind(nil)
}

func CompileForeign(program *ir.Program, options abi.CompileOptions) (*Code, error) {
	template, err := PrepareForeign(program, options)
	if err != nil {
		return nil, err
	}
	return template.Bind(nil)
}

func EmitTemplate(program *ir.Program, options abi.CompileOptions) (EmissionTemplate, error) {
	return emitProgramTemplateFromIR(program, options)
}

func AnalyzeWithBindings(program *ir.Program, options abi.CompileOptions, bindings map[string]uint64) (abi.EmissionReport, error) {
	bound, err := bindSymbols(program, bindings)
	if err != nil {
		return abi.EmissionReport{}, err
	}
	return Analyze(bound, options)
}

func EmitWithBindings(program *ir.Program, options abi.CompileOptions, bindings map[string]uint64) ([]byte, abi.EmissionReport, error) {
	template, err := EmitTemplate(program, options)
	if err != nil {
		return nil, abi.EmissionReport{}, err
	}
	code, err := template.Link(bindings)
	if err != nil {
		return nil, abi.EmissionReport{}, err
	}
	return code, template.Report, nil
}

func EmitAssemblyWithBindings(program *ir.Program, options abi.CompileOptions, bindings map[string]uint64) (string, error) {
	bound, err := bindSymbols(program, bindings)
	if err != nil {
		return "", err
	}
	return EmitAssembly(bound, options)
}

func CompileWithBindings(program *ir.Program, options abi.CompileOptions, bindings map[string]uint64) (*Code, error) {
	template, err := Prepare(program, options)
	if err != nil {
		return nil, err
	}
	return template.Bind(bindings)
}

func CompileGoWithBindings(program *ir.Program, options abi.CompileOptions, bindings map[string]uint64) (*Code, error) {
	template, err := PrepareGo(program, options)
	if err != nil {
		return nil, err
	}
	return template.Bind(bindings)
}

func CompileForeignWithBindings(program *ir.Program, options abi.CompileOptions, bindings map[string]uint64) (*Code, error) {
	template, err := PrepareForeign(program, options)
	if err != nil {
		return nil, err
	}
	return template.Bind(bindings)
}

// bindSymbols resolves constants before selection so target instructions can
// fold the bound value. Other binding APIs link after selection.
func bindSymbols(program *ir.Program, bindings map[string]uint64) (*ir.Program, error) {
	if program == nil {
		return nil, ErrInvalidFunction
	}
	bound := program.Clone()
	if err := bound.Validate(); err != nil {
		return nil, ErrInvalidFunction
	}
	used := make(map[string]bool)
	for i := range bound.Instructions {
		name := bound.Instructions[i].Symbol
		if name == "" {
			continue
		}
		value, ok := bindings[name]
		if !ok {
			if name == ir.MathPi4Symbol {
				continue
			}
			return nil, ErrUnboundSymbol
		}
		bound.Instructions[i].Imm = value
		bound.Instructions[i].Symbol = ""
		used[name] = true
	}
	for name := range bindings {
		if !used[name] {
			return nil, ErrInvalidFunction
		}
	}
	return bound, nil
}
