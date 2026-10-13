package execution

import (
	"errors"
	"reflect"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// Template owns a lowered, relocatable function body. Each Bind creates a
// separate executable mapping without rerunning selection or allocation.
type Template struct {
	code        []byte
	report      abi.Report
	relocations []machine.Relocation
	options     CodeOptions
	frame       goregister.Frame
	resolve     SymbolResolver
}

// SymbolResolver supplies built-in bindings before ordinary symbol linking.
type SymbolResolver func([]machine.Relocation, map[string]uint64) (map[string]uint64, error)

// Symbols maps external symbol names to the addresses or values that linking
// patches into relocations.
type Symbols map[string]uint64

// NewTemplate copies code, relocations, parameter widths and frame.SP, and
// keeps report, an immutable view; nil gives an empty report. Frame metadata
// is used only when options.GoManaged is true.
func NewTemplate(code []byte, report abi.Report, relocations []machine.Relocation, options CodeOptions, frame goregister.Frame) *Template {
	return NewTemplateWithResolver(code, report, relocations, options, frame, nil)
}

// NewTemplateWithResolver adds automatic built-in symbol resolution to a template.
func NewTemplateWithResolver(code []byte, report abi.Report, relocations []machine.Relocation, options CodeOptions, frame goregister.Frame, resolver SymbolResolver) *Template {
	frame.SP = append([]goregister.SPPoint(nil), frame.SP...)
	options.ParamBits = append([]uint8(nil), options.ParamBits...)
	options.ParameterTypes = append([]reflect.Type(nil), options.ParameterTypes...)
	options.GoCallTargets = append([]GoCallTarget(nil), options.GoCallTargets...)
	return &Template{code: append([]byte(nil), code...), report: report, relocations: append([]machine.Relocation(nil), relocations...), options: options, frame: frame, resolve: resolver}
}

// Report returns the lowered body's resource and clobber requirements.
func (t *Template) Report() abi.Report {
	if t == nil || t.report == nil {
		return abi.NewReport(abi.EmissionReport{})
	}
	return t.report
}

// Relocations returns an independent copy of unresolved symbol and code-base records.
func (t *Template) Relocations() []machine.Relocation {
	if t == nil {
		return nil
	}
	return append([]machine.Relocation(nil), t.relocations...)
}

// Bytes returns a linked copy of the code for use without a mapping address.
// Code-relative references (386 floating-point literals) return
// machine.ErrCodeBaseRequired; use LinkAt for those. A symbol without a
// binding returns machine.ErrUnboundSymbol, and a zero binding or a nil
// Template backend.ErrInvalidFunction.
func (t *Template) Bytes(symbols Symbols) ([]byte, error) {
	if t == nil {
		return nil, backend.ErrInvalidFunction
	}
	bindings, err := t.resolveSymbols(symbols)
	if err != nil {
		return nil, err
	}
	return machine.LinkRelocations(t.code, t.relocations, bindings)
}

// LinkAt returns a copy of the code linked to run at base. The caller must
// place the bytes at base without moving them. Its errors are those of Bytes,
// except machine.ErrCodeBaseRequired.
func (t *Template) LinkAt(symbols Symbols, base uint64) ([]byte, error) {
	if t == nil {
		return nil, backend.ErrInvalidFunction
	}
	bindings, err := t.resolveSymbols(symbols)
	if err != nil {
		return nil, err
	}
	return machine.LinkRelocationsAt(t.code, t.relocations, bindings, base)
}

func (t *Template) resolveSymbols(symbols Symbols) (map[string]uint64, error) {
	if t.resolve == nil {
		return symbols, nil
	}
	return t.resolve(t.relocations, symbols)
}

// Bind links symbols, maps the code, and registers its Go frame if required.
// Templates for external code return ErrExternalCallOnly. Bind returns the
// linking errors of Bytes; backend.ErrInvalidFunction for a Go call target
// symbol without a binding or a frame the Go runtime rejects; ErrUnsupported
// when Go-managed code cannot be registered with this Go runtime or native
// execution is unsupported; and otherwise NewCode's errors.
func (t *Template) Bind(bindings Symbols) (*Code, error) {
	return t.bind(bindings, nil)
}

// BindIn is Bind, except that the code occupies whole pages taken from arena
// instead of a mapping of its own; closing the Code returns them. It returns
// ErrArenaFull when no free run of pages fits the code, and ErrClosed after
// arena.Close. A nil arena gives Bind.
func (t *Template) BindIn(arena *Arena, bindings Symbols) (*Code, error) {
	return t.bind(bindings, arena)
}

func (t *Template) bind(bindings Symbols, arena *Arena) (*Code, error) {
	code, options, register, local, err := t.link(bindings)
	if err != nil {
		return nil, err
	}
	handle, err := newCode(code, options, register, local, arena)
	if err != nil {
		return nil, bindError(err)
	}
	return handle, nil
}

// prepare is the part of bind that precedes allocation.
func (t *Template) prepare(bindings Symbols) (mapping, error) {
	code, options, register, local, err := t.link(bindings)
	if err != nil {
		return mapping{}, err
	}
	m, err := prepareCode(code, options, register, local)
	if err != nil {
		return mapping{}, bindError(err)
	}
	return m, nil
}

// link resolves and links symbols and returns what newCode needs: the
// linked code, options with Go call targets resolved, the frame registrar
// and the relocations the mapping address resolves.
func (t *Template) link(bindings Symbols) ([]byte, CodeOptions, FrameRegistrar, []machine.Relocation, error) {
	if t == nil {
		return nil, CodeOptions{}, nil, nil, backend.ErrInvalidFunction
	}
	if t.options.External {
		return nil, CodeOptions{}, nil, nil, ErrExternalCallOnly
	}
	local := make([]machine.Relocation, 0)
	symbols := make([]machine.Relocation, 0)
	for _, r := range t.relocations {
		if r.NeedsCodeBase() {
			local = append(local, r)
		} else {
			symbols = append(symbols, r)
		}
	}
	if t.resolve != nil {
		var err error
		bindings, err = t.resolve(symbols, bindings)
		if err != nil {
			return nil, CodeOptions{}, nil, nil, err
		}
	}
	// Link external symbols and validate local records before allocating.
	// Local addresses are placeholders here; the mapping hook rebases them.
	code, err := machine.LinkRelocationsAt(t.code, t.relocations, bindings, 0)
	if err != nil {
		return nil, CodeOptions{}, nil, nil, err
	}
	var register FrameRegistrar
	if t.options.GoManaged {
		register = func(mem []byte) (bool, error) {
			return goregister.Register(mem, t.frame)
		}
	}
	options := t.options
	options.GoCallTargets = append([]GoCallTarget(nil), t.options.GoCallTargets...)
	for i := range options.GoCallTargets {
		target := &options.GoCallTargets[i]
		if target.Symbol != "" {
			value, ok := bindings[target.Symbol]
			if !ok {
				return nil, CodeOptions{}, nil, nil, backend.ErrInvalidFunction
			}
			target.Address = value
			target.Symbol = ""
		}
	}
	return code, options, register, local, nil
}

// bindError maps Go runtime registration errors to the package's.
func bindError(err error) error {
	switch {
	case errors.Is(err, goregister.ErrUnsupported):
		return ErrUnsupported
	case errors.Is(err, goregister.ErrInvalidFrame):
		return backend.ErrInvalidFunction
	}
	return err
}
