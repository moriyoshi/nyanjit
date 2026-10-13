package builder

import "github.com/moriyoshi/nyanjit/ir"

// Predicate is an immutable boolean expression over Builder values. Values
// are evaluated by the Builder; comparisons and short-circuit edges are
// materialized only when the predicate is passed to Branch.
type Predicate struct {
	builder *Builder
	node    *predicateNode
}

type predicateKind uint8

const (
	predicateCompare predicateKind = iota
	predicateAnd
	predicateOr
	predicateNot
)

type predicateNode struct {
	kind      predicateKind
	condition ir.BranchCondition
	value     int
	other     int
	anchor    int
	immediate int64
	withValue bool
	hasAnchor bool
	left      *predicateNode
	right     *predicateNode
	// frozen is this node's copy in the segment being frozen at frozenAt.
	frozen   *ir.PredicateExpr
	frozenAt uint64
}

// Compare creates a comparison between two virtual values. Signed or
// unsigned interpretation is chosen by condition.
func (b *Builder) Compare(left Value, condition ir.BranchCondition, right Value) (Predicate, error) {
	var err error
	left, err = b.materialize(left)
	if err != nil {
		return Predicate{}, err
	}
	right, err = b.materialize(right)
	if err != nil {
		return Predicate{}, err
	}
	if err := requireScalars("Compare", left, right); err != nil {
		return Predicate{}, err
	}
	if left.composite != nil || right.composite != nil {
		return compareWide(b, left, condition, right)
	}
	if b == nil || condition > ir.BranchAbove || !b.valid(left) || !b.valid(right) || left.Type().LaneBits != right.Type().LaneBits {
		return Predicate{}, ErrInvalidFunction
	}
	if left.Type().LaneBits < 64 && signedCondition(condition) {
		var err error
		left, err = b.SignExtend(left, left.Type().LaneBits)
		if err != nil {
			return Predicate{}, err
		}
		right, err = b.SignExtend(right, right.Type().LaneBits)
		if err != nil {
			return Predicate{}, err
		}
	}
	return Predicate{builder: b, node: b.newPredicate(predicateNode{
		kind: predicateCompare, condition: condition, value: left.id, other: right.id, withValue: true,
	})}, nil
}

// CompareImmediate creates a comparison between a virtual value and a signed
// immediate. The immediate's uint64 bit pattern is used for unsigned conditions.
func (b *Builder) CompareImmediate(value Value, condition ir.BranchCondition, immediate int64) (Predicate, error) {
	var err error
	value, err = b.materialize(value)
	if err != nil {
		return Predicate{}, err
	}
	if err := requireScalars("CompareImmediate", value); err != nil {
		return Predicate{}, err
	}
	if value.composite != nil {
		if !b.validComposite(value) || condition > ir.BranchAbove {
			return Predicate{}, ErrInvalidFunction
		}
		hi := uint64(0)
		if signedCondition(condition) && immediate < 0 {
			hi = ^uint64(0)
		}
		low, err := b.Const(uint64(immediate))
		if err != nil {
			return Predicate{}, err
		}
		high, err := b.Const(hi)
		if err != nil {
			return Predicate{}, err
		}
		constant, err := b.Concat(low, high)
		if err != nil {
			return Predicate{}, err
		}
		return b.Compare(value, condition, constant)
	}
	if b == nil || condition > ir.BranchAbove || !b.valid(value) {
		return Predicate{}, ErrInvalidFunction
	}
	if bits := value.Type().LaneBits; bits < 64 {
		if signedCondition(condition) {
			limit := int64(1) << (bits - 1)
			if immediate < -limit || immediate >= limit {
				return Predicate{}, ErrInvalidFunction
			}
			var err error
			value, err = b.SignExtend(value, bits)
			if err != nil {
				return Predicate{}, err
			}
		} else {
			limit := int64(1) << (bits - 1)
			if immediate < -limit || uint64(immediate) > integerMask(bits) && immediate >= 0 {
				return Predicate{}, ErrInvalidFunction
			}
			immediate = int64(uint64(immediate) & integerMask(bits))
			if immediate > 4095 {
				other, err := b.ConstBits(bits, uint64(immediate))
				if err != nil {
					return Predicate{}, err
				}
				return b.Compare(value, condition, other)
			}
		}
	}
	return Predicate{builder: b, node: b.newPredicate(predicateNode{
		kind: predicateCompare, condition: condition, value: value.id, immediate: immediate,
	})}, nil
}

func signedCondition(condition ir.BranchCondition) bool {
	return condition == ir.BranchLess || condition == ir.BranchGreaterEqual || condition == ir.BranchLessEqual || condition == ir.BranchGreater
}

// IsZero creates the zero-flag equivalent for value.
func (b *Builder) IsZero(value Value) (Predicate, error) {
	return b.CompareImmediate(value, ir.BranchEqual, 0)
}

// IsNegative creates the sign-flag equivalent for value.
func (b *Builder) IsNegative(value Value) (Predicate, error) {
	return b.CompareImmediate(value, ir.BranchLess, 0)
}

// Carry creates the unsigned carry predicate for Add, or the unsigned borrow
// predicate for Sub. It remains correct when the target cannot reuse flags.
func (b *Builder) Carry(value Value) (Predicate, error) {
	var err error
	value, err = b.materialize(value)
	if err != nil {
		return Predicate{}, err
	}
	if b == nil || !b.valid(value) {
		return Predicate{}, ErrInvalidFunction
	}
	record := b.values[value.id]
	a, c := int(record.lo), int(record.hi)
	var predicate Predicate
	switch record.op {
	case opAdd:
		predicate, err = b.Compare(value, ir.BranchBelow, Value{builder: b, id: a})
	case opSub:
		left := Value{builder: b, id: a}
		if imm, literal := b.instructionLiteral(c); literal && imm == 1 {
			predicate, err = b.CompareImmediate(left, ir.BranchBelow, 1)
		} else {
			predicate, err = b.Compare(left, ir.BranchBelow, Value{builder: b, id: c})
		}
	default:
		return Predicate{}, ErrInvalidFunction
	}
	if err != nil {
		return Predicate{}, err
	}
	predicate.node.anchor, predicate.node.hasAnchor = value.id, true
	return predicate, nil
}

// And combines predicates with short-circuit conjunction. Chains have no depth
// limit; Branch rejects predicates exceeding ir.MaxPredicateNodes expanded nodes.
func (p Predicate) And(other Predicate) Predicate {
	if p.builder == nil || p.node == nil || p.builder != other.builder || other.node == nil {
		return Predicate{}
	}
	return Predicate{builder: p.builder, node: p.builder.newPredicate(predicateNode{kind: predicateAnd, left: p.node, right: other.node})}
}

// Or combines predicates with short-circuit disjunction. Chains have no depth
// limit; Branch rejects predicates exceeding ir.MaxPredicateNodes expanded nodes.
func (p Predicate) Or(other Predicate) Predicate {
	if p.builder == nil || p.node == nil || p.builder != other.builder || other.node == nil {
		return Predicate{}
	}
	return Predicate{builder: p.builder, node: p.builder.newPredicate(predicateNode{kind: predicateOr, left: p.node, right: other.node})}
}

// Not negates a predicate without evaluating it eagerly.
func (p Predicate) Not() Predicate {
	if p.builder == nil || p.node == nil {
		return Predicate{}
	}
	return Predicate{builder: p.builder, node: p.builder.newPredicate(predicateNode{kind: predicateNot, left: p.node})}
}

// predicateValues returns the distinct comparison operands of p, in walk
// order, and p's expanded node count. The list is reused by the next call.
func (b *Builder) predicateValues(p Predicate) ([]int, int, error) {
	if p.builder != b || p.node == nil {
		return nil, 0, ErrInvalidFunction
	}
	var seen map[int]bool
	values := b.predicateIDs[:0]
	work := append(b.predicateWork[:0], p.node)
	defer func() {
		clear(work[:cap(work)])
		b.predicateWork = work[:0]
		b.predicateIDs = values[:0]
	}()
	add := func(id int) {
		// Most predicates compare a few values: search them, and switch
		// to a set for large ones.
		if seen == nil {
			for _, v := range values {
				if v == id {
					return
				}
			}
			if len(values) < 16 {
				values = append(values, id)
				return
			}
			seen = make(map[int]bool, 2*len(values))
			for _, v := range values {
				seen[v] = true
			}
		}
		if !seen[id] {
			seen[id] = true
			values = append(values, id)
		}
	}
	count := 0
	for len(work) != 0 {
		node := work[len(work)-1]
		work = work[:len(work)-1]
		count++
		if count > ir.MaxPredicateNodes {
			return nil, 0, newLimitError("predicate nodes", count, ir.MaxPredicateNodes, "branch predicate", "predicate exceeds expanded-node limit (%d)", ir.MaxPredicateNodes)
		}
		if node == nil {
			return nil, 0, ErrInvalidFunction
		}
		if node.kind == predicateCompare {
			if node.condition > ir.BranchAbove {
				return nil, 0, ErrInvalidFunction
			}
			var idBuffer [3]int
			ids := idBuffer[:0]
			if node.hasAnchor {
				ids = append(ids, node.anchor)
			}
			ids = append(ids, node.value)
			if node.withValue {
				ids = append(ids, node.other)
			}
			for _, id := range ids {
				if !b.valid(Value{builder: b, id: id}) {
					return nil, 0, ErrInvalidFunction
				}
				add(id)
			}
			continue
		}
		if node.kind != predicateAnd && node.kind != predicateOr && node.kind != predicateNot {
			return nil, 0, ErrInvalidFunction
		}
		if node.kind != predicateNot {
			work = append(work, node.right)
		}
		work = append(work, node.left)
	}
	return values, count, nil
}
