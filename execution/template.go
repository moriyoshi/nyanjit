package execution

import (
	"errors"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/goregister"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/regalloc"
)

// Template owns a lowered, relocatable function body. Each Bind creates a
// separate executable mapping without rerunning selection or allocation.
type Template struct {
	code        []byte
	report      abi.EmissionReport
	relocations []machine.Relocation
	options     CodeOptions
	frame       goregister.Frame
	resolve     SymbolResolver
}

// SymbolResolver supplies built-in bindings before ordinary symbol linking.
type SymbolResolver func([]machine.Relocation, map[string]uint64) (map[string]uint64, error)

// NewTemplate copies the assembled bytes and relocation records. The frame is
// used only when options.GoManaged is true.
func NewTemplate(code []byte, report abi.EmissionReport, relocations []machine.Relocation, options CodeOptions, frame goregister.Frame) *Template {
	return NewTemplateWithResolver(code, report, relocations, options, frame, nil)
}

// NewTemplateWithResolver adds automatic built-in symbol resolution to a template.
func NewTemplateWithResolver(code []byte, report abi.EmissionReport, relocations []machine.Relocation, options CodeOptions, frame goregister.Frame, resolver SymbolResolver) *Template {
	frame.SP = append([]goregister.SPPoint(nil), frame.SP...)
	result := &Template{
		code: append([]byte(nil), code...), report: report.Clone(),
		relocations: append([]machine.Relocation(nil), relocations...),
		options:     options, frame: frame,
	}
	result.resolve = resolver
	return result
}

// Report returns the lowered body's resource and clobber requirements.
func (t *Template) Report() abi.EmissionReport {
	if t == nil {
		return abi.EmissionReport{}
	}
	return t.report.Clone()
}

// Relocations returns an independent copy of unresolved symbol records.
func (t *Template) Relocations() []machine.Relocation {
	if t == nil {
		return nil
	}
	return append([]machine.Relocation(nil), t.relocations...)
}

// Bind links symbols, maps the code, and registers its Go frame if required.
func (t *Template) Bind(bindings map[string]uint64) (*Code, error) {
	if t == nil {
		return nil, regalloc.ErrInvalidFunction
	}
	if t.resolve != nil {
		var err error
		bindings, err = t.resolve(t.relocations, bindings)
		if err != nil {
			return nil, err
		}
	}
	code, err := machine.LinkRelocations(t.code, t.relocations, bindings)
	if err != nil {
		return nil, err
	}
	var register FrameRegistrar
	if t.options.GoManaged {
		register = func(mem []byte) (bool, error) {
			return goregister.Register(mem, t.frame)
		}
	}
	handle, err := NewCode(code, t.options, register)
	if err != nil {
		switch {
		case errors.Is(err, goregister.ErrUnsupported):
			return nil, ErrUnsupported
		case errors.Is(err, goregister.ErrInvalidFrame):
			return nil, regalloc.ErrInvalidFunction
		}
		return nil, err
	}
	return handle, nil
}
