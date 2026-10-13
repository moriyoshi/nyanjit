package ir

import (
	"errors"
	"fmt"
	"strconv"
)

// ValidationError locates the rule a program breaks. It wraps
// ErrInvalidProgram, so errors.Is(err, ErrInvalidProgram) holds for every
// validation failure; use errors.As to read the location.
type ValidationError struct {
	// Index is the offending instruction, or -1 when the rule concerns no
	// single instruction (a control step, label, scope or Var).
	Index int
	// Op is the opcode at Index. It is meaningful only when Index >= 0.
	Op Opcode
	// Rule describes the broken rule. When Index is -1 it names the step,
	// label, scope or Var concerned.
	Rule string
}

// Error returns "instruction 12 (OpAdd): <rule>: ir: invalid program", or
// "<rule>: ir: invalid program" when Index is -1.
func (e *ValidationError) Error() string {
	if e.Index < 0 {
		return e.Rule + ": " + ErrInvalidProgram.Error()
	}
	return "instruction " + strconv.Itoa(e.Index) + " (" + e.Op.String() + "): " + e.Rule + ": " + ErrInvalidProgram.Error()
}

// Unwrap returns ErrInvalidProgram.
func (e *ValidationError) Unwrap() error { return ErrInvalidProgram }

// invalidAt reports a rule broken by instruction index of ops.
func invalidAt(ops []Instruction, index int, format string, args ...any) error {
	e := &ValidationError{Index: index, Rule: fmt.Sprintf(format, args...)}
	if index >= 0 && index < len(ops) {
		e.Op = ops[index].Op
	}
	return e
}

// invalidRule reports a rule that concerns no single instruction.
func invalidRule(format string, args ...any) error {
	return &ValidationError{Index: -1, Rule: fmt.Sprintf(format, args...)}
}

// prefixRule adds context to a ValidationError's rule; other errors (such as
// ErrPredicateLimit) are wrapped with the same prefix.
func prefixRule(err error, format string, args ...any) error {
	prefix := fmt.Sprintf(format, args...)
	var invalid *ValidationError
	if errors.As(err, &invalid) {
		out := *invalid
		out.Rule = prefix + ": " + out.Rule
		return &out
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

// atInstruction locates a ValidationError at instruction index of ops.
func atInstruction(err error, ops []Instruction, index int) error {
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		return err
	}
	out := *invalid
	out.Index, out.Op = index, ops[index].Op
	return &out
}
