package ir

import "reflect"

// Input is accepted by compiler entry points. Program is a mutable adapter;
// SealedProgram reuses validated storage and analyses. Clone always returns an
// independent mutable Program, including for a sealed input.
type Input interface{ Clone() *Program }

// SealedProgram owns an immutable, validated function and its analyses. It is
// safe for concurrent use.
//
// Its accessors return storage borrowed from the program, without copying:
// Instructions, Variables, ScopeExits, Control, ResultParts and
// ParameterTypes, and the slices reachable from them (Args, Data, Effect,
// predicates). Read borrowed storage, but never modify it: the program was
// validated once, and compilers trust that validation. Use Clone for an
// editable copy. A zero value is not a valid input.
type SealedProgram struct {
	source   *Program
	analysis Analysis
}

// Clone returns an independent mutable copy, including nested slices and
// predicates. Editing it cannot change this program. Nil and zero inputs return nil.
func (p *SealedProgram) Clone() *Program {
	if p == nil {
		return nil
	}
	return p.source.Clone()
}

// program returns the sealed storage, or an empty program for a nil or zero
// value.
func (p *SealedProgram) program() *Program {
	if p == nil || p.source == nil {
		return &Program{}
	}
	return p.source
}

// Len returns the number of instructions.
func (p *SealedProgram) Len() int { return len(p.program().Instructions) }

// Arity returns the number of logical entry parameters.
func (p *SealedProgram) Arity() int { return p.program().Arity }

// Result returns the result value, or -1 for a control program without a
// global result.
func (p *SealedProgram) Result() int { return p.program().Result }

// Instructions returns the borrowed, read-only instruction storage.
func (p *SealedProgram) Instructions() []Instruction { return p.program().Instructions }

// Variables returns the borrowed, read-only variable table.
func (p *SealedProgram) Variables() []Variable { return p.program().Variables }

// ScopeExits returns the borrowed, read-only scope table.
func (p *SealedProgram) ScopeExits() []ScopeExit { return p.program().ScopeExits }

// Control returns the borrowed, read-only control structure, or nil for a
// straight-line program.
func (p *SealedProgram) Control() *Control { return p.program().Control }

// ResultParts returns the borrowed low and high result values of a 128-bit
// result, or nil.
func (p *SealedProgram) ResultParts() []int { return p.program().ResultParts }

// ParameterTypes returns the borrowed host Go aggregate entry types.
func (p *SealedProgram) ParameterTypes() []reflect.Type { return p.program().ParameterTypes }

// Analysis returns the analyses computed when the program was sealed. It is
// nil for a nil or zero SealedProgram, which no compiler accepts.
func (p *SealedProgram) Analysis() *Analysis {
	if p == nil || p.source == nil {
		return nil
	}
	return &p.analysis
}

// Seal detaches and validates a mutable program, then computes its analyses
// once. Edits to p or to a later Clone do not affect the returned program.
// Its errors are Program.Validate's: a *ValidationError (wrapping
// ErrInvalidProgram) or ErrPredicateLimit.
func Seal(p *Program) (*SealedProgram, error) {
	return SealOwned(p.Clone())
}

// SealOwned validates p and takes ownership of it without copying. The caller
// must not use p, or any slice reachable from it, afterwards. Use Seal to
// keep using p. Its errors are Program.Validate's; a nil p is an empty
// program, which fails validation.
func SealOwned(p *Program) (*SealedProgram, error) {
	if p == nil {
		p = &Program{}
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &SealedProgram{source: p, analysis: Analysis{program: p, base: analyzeProgram(p, false)}}, nil
}
