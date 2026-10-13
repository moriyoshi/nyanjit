package nyanjit

import "github.com/moriyoshi/nyanjit/ir"

// CompareValue compares equal-width Values by condition and returns 1 when it
// holds and 0 otherwise, at the operands' width (64 bits for 128-bit
// operands). Signed conditions compare at the operands' own width.
func (b *Builder) CompareValue(left Value, condition ir.BranchCondition, right Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.CompareValue(left, condition, right))
}

// PredicateValue returns 1 when p holds and 0 otherwise, as a bits-wide Value
// (8, 16, 32 or 64). Every comparison in p is evaluated; an empty Predicate
// gives 1.
func (b *Builder) PredicateValue(p Predicate, bits uint8) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.PredicateValue(p, bits))
}

// Overflow tests signed overflow of the Add, Sub or Mul that defined value.
// Targets with an overflow flag branch on it while it is live.
func (b *Builder) Overflow(value Value) Predicate {
	if !b.ready() {
		return Predicate{}
	}
	return b.okPred(b.inner.Overflow(value))
}
