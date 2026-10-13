//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || 386 || loong64)

package varfuzz

import (
	"errors"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// lowerUnit resolves spec and lowers p.
func lowerUnit(p ir.Input, spec compile.TargetSpec, f compile.Func) (*compile.Unit, error) {
	target, err := compile.NewTarget(spec)
	if err != nil {
		return nil, err
	}
	return compile.Lower(target, p, f)
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

// managedFrameLimit reports whether err is a Go-managed frame above the
// target's managed-frame limit.
func managedFrameLimit(err error) bool {
	var limit *compile.LimitError
	return errors.As(err, &limit) && limit.Kind == compile.LimitFrameBytes
}

// managedUnsupported reports whether err is the expected failure to bind
// Go-managed code in builds without runtime frame registration.
func managedUnsupported(err error) bool {
	return !goregister.Supported() && errors.Is(err, compile.ErrUnsupported)
}
