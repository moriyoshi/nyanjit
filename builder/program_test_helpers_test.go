package builder

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

type Code = execution.Code

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

// lowerUnit resolves spec and lowers p.
func lowerUnit(p ir.Input, spec compile.TargetSpec, f compile.Func) (*compile.Unit, error) {
	target, err := compile.NewTarget(spec)
	if err != nil {
		return nil, err
	}
	return compile.Lower(target, p, f)
}

// lowerTemplate lowers p for spec and encodes its template.
func lowerTemplate(p ir.Input, spec compile.TargetSpec, f compile.Func) (*execution.Template, error) {
	u, err := lowerUnit(p, spec, f)
	if err != nil {
		return nil, err
	}
	return u.Template()
}

// lowerListing lowers p for spec and renders its listing. Go-managed
// functions use symbolic gateway names so listings do not depend on the host.
func lowerListing(p ir.Input, spec compile.TargetSpec, f compile.Func) (string, error) {
	u, err := lowerUnit(p, spec, f)
	if err != nil {
		return "", err
	}
	if f.Context == compile.ContextGoManaged {
		return u.ListingSymbolic()
	}
	return u.Listing()
}

// lowerReport lowers p for spec and returns its report.
func lowerReport(p ir.Input, spec compile.TargetSpec, f compile.Func) (abi.Report, error) {
	u, err := lowerUnit(p, spec, f)
	if err != nil {
		return nil, err
	}
	return u.Report(), nil
}

// compileCode lowers p for spec and binds it into executable memory.
func compileCode(p ir.Input, spec compile.TargetSpec, f compile.Func) (*execution.Code, error) {
	template, err := lowerTemplate(p, spec, f)
	if err != nil {
		return nil, err
	}
	return template.Bind(nil)
}

// testReport snapshots b and returns its report for spec and f.
func testReport(b *Builder, spec compile.TargetSpec, f compile.Func) (abi.Report, error) {
	program, err := testProgram(b)
	if err != nil {
		return nil, err
	}
	return lowerReport(program, spec, f)
}

// testListing snapshots b and renders its listing for spec and f.
func testListing(b *Builder, spec compile.TargetSpec, f compile.Func) (string, error) {
	program, err := testProgram(b)
	if err != nil {
		return "", err
	}
	return lowerListing(program, spec, f)
}

// testTemplate snapshots b and encodes its template for spec and f.
func testTemplate(b *Builder, spec compile.TargetSpec, f compile.Func) (*execution.Template, error) {
	program, err := testProgram(b)
	if err != nil {
		return nil, err
	}
	return lowerTemplate(program, spec, f)
}

// testCompile snapshots b and binds it as a native C ABI function.
func testCompile(b *Builder) (*Code, error) {
	return testCompileWithOptions(b, compile.Func{ABI: abi.C})
}

// testCompileWithOptions snapshots b and binds it for the host with f.
func testCompileWithOptions(b *Builder, f compile.Func) (*Code, error) {
	program, err := testProgram(b)
	if err != nil {
		return nil, err
	}
	return compileCode(program, compile.TargetSpec{}, f)
}
