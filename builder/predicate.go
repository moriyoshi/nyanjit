package builder

import "github.com/moriyoshi/nyanjit/ir"

type BranchCondition = ir.BranchCondition

const (
	BranchEqual        = ir.BranchEqual
	BranchNotEqual     = ir.BranchNotEqual
	BranchLess         = ir.BranchLess
	BranchGreaterEqual = ir.BranchGreaterEqual
	BranchLessEqual    = ir.BranchLessEqual
	BranchGreater      = ir.BranchGreater
	BranchBelow        = ir.BranchBelow
	BranchAboveEqual   = ir.BranchAboveEqual
	BranchBelowEqual   = ir.BranchBelowEqual
	BranchAbove        = ir.BranchAbove
)

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
	condition BranchCondition
	value     int
	other     int
	anchor    int
	immediate int64
	withValue bool
	hasAnchor bool
	left      *predicateNode
	right     *predicateNode
}

// Compare creates a comparison between two virtual values. Signed or
// unsigned interpretation is chosen by condition.
func (b *Builder) Compare(left Value, condition BranchCondition, right Value) (Predicate, error) {
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
	if b == nil || condition > BranchAbove || !b.valid(left) || !b.valid(right) || left.Type().LaneBits != right.Type().LaneBits {
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
	return Predicate{builder: b, node: &predicateNode{
		kind: predicateCompare, condition: condition, value: left.id, other: right.id, withValue: true,
	}}, nil
}

// CompareImmediate creates a comparison between a virtual value and a signed
// immediate. The immediate's uint64 bit pattern is used for unsigned conditions.
func (b *Builder) CompareImmediate(value Value, condition BranchCondition, immediate int64) (Predicate, error) {
	var err error
	value, err = b.materialize(value)
	if err != nil {
		return Predicate{}, err
	}
	if err := requireScalars("CompareImmediate", value); err != nil {
		return Predicate{}, err
	}
	if value.composite != nil {
		if !b.validComposite(value) || condition > BranchAbove {
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
	if b == nil || condition > BranchAbove || !b.valid(value) {
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
	return Predicate{builder: b, node: &predicateNode{
		kind: predicateCompare, condition: condition, value: value.id, immediate: immediate,
	}}, nil
}

func signedCondition(condition BranchCondition) bool {
	return condition == BranchLess || condition == BranchGreaterEqual || condition == BranchLessEqual || condition == BranchGreater
}

// IsZero creates the zero-flag equivalent for value.
func (b *Builder) IsZero(value Value) (Predicate, error) {
	return b.CompareImmediate(value, BranchEqual, 0)
}

// IsNegative creates the sign-flag equivalent for value.
func (b *Builder) IsNegative(value Value) (Predicate, error) {
	return b.CompareImmediate(value, BranchLess, 0)
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
	ins := b.code[value.id]
	var predicate Predicate
	switch ins.Op {
	case opAdd:
		predicate, err = b.Compare(value, BranchBelow, Value{builder: b, id: ins.A})
	case opSub:
		left := Value{builder: b, id: ins.A}
		if b.code[ins.B].Op == opConst && b.code[ins.B].Symbol == "" && b.code[ins.B].Imm == 1 {
			predicate, err = b.CompareImmediate(left, BranchBelow, 1)
		} else {
			predicate, err = b.Compare(left, BranchBelow, Value{builder: b, id: ins.B})
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

// And combines predicates with short-circuit conjunction.
func (p Predicate) And(other Predicate) Predicate {
	if p.builder == nil || p.node == nil || p.builder != other.builder || other.node == nil {
		return Predicate{}
	}
	return Predicate{builder: p.builder, node: &predicateNode{kind: predicateAnd, left: p.node, right: other.node}}
}

// Or combines predicates with short-circuit disjunction.
func (p Predicate) Or(other Predicate) Predicate {
	if p.builder == nil || p.node == nil || p.builder != other.builder || other.node == nil {
		return Predicate{}
	}
	return Predicate{builder: p.builder, node: &predicateNode{kind: predicateOr, left: p.node, right: other.node}}
}

// Not negates a predicate without evaluating it eagerly.
func (p Predicate) Not() Predicate {
	if p.builder == nil || p.node == nil {
		return Predicate{}
	}
	return Predicate{builder: p.builder, node: &predicateNode{kind: predicateNot, left: p.node}}
}

func (b *Builder) predicateValues(p Predicate) ([]int, error) {
	if p.builder != b || p.node == nil {
		return nil, ErrInvalidFunction
	}
	seen := make(map[int]bool)
	var values []int
	var walk func(*predicateNode) error
	walk = func(node *predicateNode) error {
		if node == nil {
			return ErrInvalidFunction
		}
		if node.kind == predicateCompare {
			if node.condition > BranchAbove {
				return ErrInvalidFunction
			}
			ids := make([]int, 0, 3)
			if node.hasAnchor {
				ids = append(ids, node.anchor)
			}
			ids = append(ids, node.value)
			if node.withValue {
				ids = append(ids, node.other)
			}
			for _, id := range ids {
				if !b.valid(Value{builder: b, id: id}) {
					return ErrInvalidFunction
				}
				if !seen[id] {
					seen[id] = true
					values = append(values, id)
				}
			}
			return nil
		}
		if node.kind != predicateAnd && node.kind != predicateOr && node.kind != predicateNot {
			return ErrInvalidFunction
		}
		if err := walk(node.left); err != nil {
			return err
		}
		if node.kind != predicateNot {
			return walk(node.right)
		}
		return nil
	}
	if err := walk(p.node); err != nil {
		return nil, err
	}
	return values, nil
}
