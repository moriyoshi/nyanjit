package compile

import (
	"github.com/moriyoshi/nyanjit/backend/abi"

	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/ir"
)

// lowerUnit resolves spec and lowers p.
func lowerUnit(p ir.Input, spec TargetSpec, f Func) (*Unit, error) {
	target, err := NewTarget(spec)
	if err != nil {
		return nil, err
	}
	return Lower(target, p, f)
}

// lowerTemplate lowers p for spec and encodes its template.
func lowerTemplate(p ir.Input, spec TargetSpec, f Func) (*execution.Template, error) {
	u, err := lowerUnit(p, spec, f)
	if err != nil {
		return nil, err
	}
	return u.Template()
}

// lowerListing lowers p for spec and renders its listing. Go-managed
// functions use symbolic gateway names so listings do not depend on the host.
func lowerListing(p ir.Input, spec TargetSpec, f Func) (string, error) {
	u, err := lowerUnit(p, spec, f)
	if err != nil {
		return "", err
	}
	if f.Context == ContextGoManaged {
		return u.ListingSymbolic()
	}
	return u.Listing()
}

// lowerReport lowers p for spec and returns its report.
func lowerReport(p ir.Input, spec TargetSpec, f Func) (abi.Report, error) {
	u, err := lowerUnit(p, spec, f)
	if err != nil {
		return nil, err
	}
	return u.Report(), nil
}

// compileCode lowers p for spec and binds it; internal tests cannot import
// the root package's Compile.
func compileCode(p ir.Input, spec TargetSpec, f Func) (*execution.Code, error) {
	template, err := lowerTemplate(p, spec, f)
	if err != nil {
		return nil, err
	}
	return template.Bind(nil)
}
