// Package compile lowers nyanjit IR programs to native code.
//
// NewTarget resolves a code generation target once; Lower lowers a program
// for it into a Unit, whose report, listing and template are lazy views of
// one selection, allocation and layout.
package compile

import (
	"errors"
	"fmt"

	"github.com/moriyoshi/nyanjit/ir"
)

// bindSymbols substitutes Func.Constants before selection so target
// instructions can fold the bound values. Template binding links after
// selection instead.
func bindSymbols(program ir.Input, bindings map[string]uint64) (*ir.SealedProgram, error) {
	if program == nil {
		return nil, ErrInvalidFunction
	}
	bound := program.Clone()
	if bound == nil {
		return nil, ErrInvalidFunction
	}
	if sealed, ok := program.(*ir.SealedProgram); ok {
		if sealed.Analysis() == nil {
			return nil, ErrInvalidFunction
		}
	} else if err := bound.Validate(); err != nil {
		return nil, invalidSource(err)
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
		if bound.Instructions[i].Op == ir.OpConst {
			bits := bound.Instructions[i].Bits
			if bits != 0 && bits < 64 {
				bound.Instructions[i].Imm &= uint64(1)<<bits - 1
			}
		}
		bound.Instructions[i].Symbol = ""
		used[name] = true
	}
	for name := range bindings {
		if !used[name] {
			return nil, ErrInvalidFunction
		}
	}
	// Legacy raw IR can attach symbols to constrained non-constant immediates,
	// so the substituted program is validated again when it is sealed.
	sealed, err := ir.SealOwned(bound)
	if err != nil {
		return nil, invalidSource(err)
	}
	return sealed, nil
}

// invalidSource wraps a validation error so that it matches both
// ErrInvalidFunction and the IR's own error (ir.ErrInvalidProgram and any
// *ir.ValidationError); an oversized predicate is a LimitError.
func invalidSource(err error) error {
	if errors.Is(err, ir.ErrPredicateLimit) {
		return predicateLimitError("program")
	}
	return fmt.Errorf("%w: %w", ErrInvalidFunction, err)
}
