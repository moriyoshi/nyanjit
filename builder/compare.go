package builder

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/ir"
)

// CompareValue compares equal-width scalars by condition and returns 1 when it
// holds and 0 otherwise, at the operands' width (64 bits for 128-bit operands).
// Signed conditions compare at the operands' own width; no sign extension is
// needed first.
func (b *Builder) CompareValue(left Value, condition ir.BranchCondition, right Value) (Value, error) {
	var err error
	if left, err = b.materialize(left); err != nil {
		return Value{}, err
	}
	if right, err = b.materialize(right); err != nil {
		return Value{}, err
	}
	if err := requireScalars("CompareValue", left, right); err != nil {
		return Value{}, err
	}
	if left.composite != nil || right.composite != nil {
		p, err := compareWide(b, left, condition, right)
		if err != nil {
			return Value{}, err
		}
		return b.PredicateValue(p, 64)
	}
	x, y, err := b.scalarPair(ir.OpCompare, left, right)
	if err != nil {
		return Value{}, err
	}
	if condition > ir.BranchAbove {
		return Value{}, fmt.Errorf("CompareValue: condition %d is not defined: %w", condition, ErrInvalidFunction)
	}
	return b.append(instruction{Op: ir.OpCompare, A: x.id, B: y.id, Imm: uint64(condition), Bits: x.Type().LaneBits}), nil
}

// PredicateValue returns 1 when p holds and 0 otherwise, as a bits-wide scalar
// (8, 16, 32 or 64). It emits one instruction per distinct predicate node, so
// shared subtrees are not expanded again, and it evaluates every comparison.
// An empty Predicate always holds and gives 1.
func (b *Builder) PredicateValue(p Predicate, bits uint8) (Value, error) {
	if b == nil || !validScalarBits(bits) {
		return Value{}, fmt.Errorf("PredicateValue: width %d is not 8, 16, 32 or 64: %w", bits, ErrInvalidFunction)
	}
	if p.builder == nil && p.node == nil {
		return b.ConstBits(bits, 1)
	}
	if p.builder != b || p.node == nil {
		return Value{}, fmt.Errorf("PredicateValue: predicate belongs to another Builder: %w", ErrInvalidFunction)
	}
	// Leaves are pure and never trap, so both sides of And and Or are
	// evaluated. The walk is iterative: chains have no depth limit.
	memo := make(map[*predicateNode]Value)
	var one Value
	work := []*predicateNode{p.node}
	for len(work) != 0 {
		node := work[len(work)-1]
		if _, done := memo[node]; done {
			work = work[:len(work)-1]
			continue
		}
		var v Value
		var err error
		switch node.kind {
		case predicateCompare:
			v, err = b.predicateLeaf(node, bits)
		case predicateNot:
			left, ok := memo[node.left]
			if !ok {
				if node.left == nil {
					return Value{}, ErrInvalidFunction
				}
				work = append(work, node.left)
				continue
			}
			if one.builder == nil {
				if one, err = b.ConstBits(bits, 1); err != nil {
					return Value{}, err
				}
			}
			v, err = b.Xor(left, one)
		case predicateAnd, predicateOr:
			left, okLeft := memo[node.left]
			right, okRight := memo[node.right]
			if !okLeft || !okRight {
				if node.left == nil || node.right == nil {
					return Value{}, ErrInvalidFunction
				}
				if !okRight {
					work = append(work, node.right)
				}
				if !okLeft {
					work = append(work, node.left)
				}
				continue
			}
			if node.kind == predicateAnd {
				v, err = b.And(left, right)
			} else {
				v, err = b.Or(left, right)
			}
		default:
			return Value{}, ErrInvalidFunction
		}
		if err != nil {
			return Value{}, err
		}
		memo[node] = v
		work = work[:len(work)-1]
	}
	return memo[p.node], nil
}

// predicateLeaf emits the comparison of node as an OpCompare at its operand
// width, cast to bits. An immediate becomes a constant of that width.
func (b *Builder) predicateLeaf(node *predicateNode, bits uint8) (Value, error) {
	if node.condition > ir.BranchAbove {
		return Value{}, ErrInvalidFunction
	}
	left := Value{builder: b, id: node.value}
	if !b.valid(left) {
		return Value{}, b.invalidValue(left)
	}
	width := left.Type().LaneBits
	var right Value
	var err error
	if node.withValue {
		right = Value{builder: b, id: node.other}
	} else if right, err = b.ConstBits(width, uint64(node.immediate)&integerMask(width)); err != nil {
		return Value{}, err
	}
	v, err := b.CompareValue(left, node.condition, right)
	if err != nil || width == bits {
		return v, err
	}
	return b.CastUnsigned(v, bits)
}
