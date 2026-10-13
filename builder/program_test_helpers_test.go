package builder

import (
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/execution"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

type Code = execution.Code
type CodeTemplate = execution.Template
type EmissionTemplate = compile.EmissionTemplate

var (
	ErrUnsupported      = execution.ErrUnsupported
	ErrClosed           = execution.ErrClosed
	ErrArity            = execution.ErrArity
	ErrUnboundSymbol    = machine.ErrUnboundSymbol
	ErrUnsupportedABI   = compile.ErrUnsupportedABI
	ErrExternalCallOnly = compile.ErrExternalCallOnly
	ErrStackRequired    = compile.ErrStackRequired
)

// Existing lowering tests compile snapshots so they can inspect one Builder
// with several options. Production callers finish the Builder once.
func testProgram(b *Builder) (*ir.Program, error) {
	if b == nil {
		return nil, ErrInvalidFunction
	}
	copyBuilder := *b
	program, err := copyBuilder.Finish()
	if err != nil {
		return nil, err
	}
	return program.Clone(), nil
}

func testAnalyze(b *Builder, options CompileOptions) (EmissionReport, error) {
	program, err := testProgram(b)
	if err != nil {
		return EmissionReport{}, err
	}
	return compile.Analyze(program, options)
}

func testEmit(b *Builder, options CompileOptions) ([]byte, EmissionReport, error) {
	program, err := testProgram(b)
	if err != nil {
		return nil, EmissionReport{}, err
	}
	return compile.Emit(program, options)
}

func testEmitAssembly(b *Builder, options CompileOptions) (string, error) {
	program, err := testProgram(b)
	if err != nil {
		return "", err
	}
	return compile.EmitAssembly(program, options)
}

func testCompile(b *Builder) (*Code, error) {
	return testCompileWithOptions(b, CompileOptions{ABI: ABIC})
}

func testCompileWithOptions(b *Builder, options CompileOptions) (*Code, error) {
	program, err := testProgram(b)
	if err != nil {
		return nil, err
	}
	return compile.Compile(program, options)
}

func testEmitTemplate(b *Builder, options CompileOptions) (EmissionTemplate, error) {
	program, err := testProgram(b)
	if err != nil {
		return EmissionTemplate{}, err
	}
	return compile.EmitTemplate(program, options)
}

func testPrepare(b *Builder, options CompileOptions) (*CodeTemplate, error) {
	program, err := testProgram(b)
	if err != nil {
		return nil, err
	}
	return compile.Prepare(program, options)
}

func testCompileWithBindings(b *Builder, options CompileOptions, bindings map[string]uint64) (*Code, error) {
	program, err := testProgram(b)
	if err != nil {
		return nil, err
	}
	template, err := compile.Prepare(program, options)
	if err != nil {
		return nil, err
	}
	return template.Bind(bindings)
}
