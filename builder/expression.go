package builder

import (
	"github.com/moriyoshi/nyanjit/ir"
	"sync"
)

// valueExpression has immutable tree edges and builder-owned analysis caches.
// A Value leaf is captured when the
// tree is built, while loads and calls stay in the Builder's ordered stream.
// Its nodes are turned into virtual instructions only when an operand is used.
type valueExpression struct {
	typ                  ir.Type
	op                   op
	bits                 uint8
	imm                  uint64
	left                 Value
	right                Value
	third                Value
	byValue              bool
	function             string
	materialized         Value
	materializedIn       int // the blockKey where materialized was emitted
	validEpoch, seenWalk uint64
}

// LiteralBits creates a deferred integer constant. It adds no instruction
// until the value is used by an operation, predicate, or control edge.
func (b *Builder) LiteralBits(bits uint8, value uint64) (Value, error) {
	if !b.canAppend() || !validScalarBits(bits) {
		return Value{}, ErrInvalidFunction
	}
	return Value{builder: b, id: -1, expression: b.newExpression(valueExpression{op: opConst, bits: bits, imm: value & integerMask(bits)})}, nil
}

// Literal creates a deferred 64-bit integer constant.
func (b *Builder) Literal(value uint64) (Value, error) { return b.LiteralBits(64, value) }

func expressionBinary(operation op, left, right Value) Value {
	if left.Type().Kind != ir.ScalarKind && (operation == ir.OpFAdd || operation == ir.OpFSub || operation == ir.OpFMul || operation == ir.OpFDiv) {
		return Value{}
	}
	if left.builder == nil || left.builder != right.builder || !left.Type().Valid() || left.Type() != right.Type() {
		return Value{}
	}
	return Value{builder: left.builder, id: -1, expression: left.builder.newExpression(valueExpression{
		op: operation, bits: left.Type().LaneBits, typ: left.Type(), left: left, right: right,
	})}
}

// Add, Sub, Mul, And, Or, and Xor compose pure integer expressions. The
// resulting Value can be passed anywhere a materialized Value is accepted.
func (v Value) Add(other Value) Value { return expressionBinary(opAdd, v, other) }
func (v Value) Sub(other Value) Value { return expressionBinary(opSub, v, other) }
func (v Value) Mul(other Value) Value { return expressionBinary(opMul, v, other) }
func (v Value) And(other Value) Value { return expressionBinary(opAnd, v, other) }
func (v Value) Or(other Value) Value  { return expressionBinary(opOr, v, other) }
func (v Value) Xor(other Value) Value { return expressionBinary(opXor, v, other) }

// FAdd, FSub, FMul, and FDiv compose floating point bit-pattern expressions.
func (v Value) FAdd(other Value) Value { return expressionBinary(ir.OpFAdd, v, other) }
func (v Value) FSub(other Value) Value { return expressionBinary(ir.OpFSub, v, other) }
func (v Value) FMul(other Value) Value { return expressionBinary(ir.OpFMul, v, other) }
func (v Value) FDiv(other Value) Value { return expressionBinary(ir.OpFDiv, v, other) }

// FSqrt composes a floating point square-root expression.
func (v Value) FSqrt() Value {
	if v.builder == nil || v.Type().Kind != ir.ScalarKind || v.Type().LaneBits != 32 && v.Type().LaneBits != 64 {
		return Value{}
	}
	return Value{builder: v.builder, id: -1, expression: v.builder.newExpression(valueExpression{
		op: ir.OpFSqrt, bits: v.Type().LaneBits, typ: v.Type(), left: v,
	})}
}

// FConvert composes a conversion between binary32 and binary64 bit patterns.
func (v Value) FConvert(bits uint8) Value {
	if v.builder == nil || v.Type().Kind != ir.ScalarKind || v.Type().LaneBits != 32 && v.Type().LaneBits != 64 || bits != 32 && bits != 64 {
		return Value{}
	}
	if v.Type().LaneBits == bits {
		return v
	}
	return Value{builder: v.builder, id: -1, expression: v.builder.newExpression(valueExpression{op: ir.OpFConvert, bits: bits, left: v})}
}

// FMA composes an explicitly fused multiply-add expression.
func (v Value) FMA(multiplier, addend Value) Value {
	if v.builder == nil || v.Type().Kind != ir.ScalarKind || multiplier.Type().Kind != ir.ScalarKind || addend.Type().Kind != ir.ScalarKind || v.builder != multiplier.builder || v.builder != addend.builder ||
		(v.Type().LaneBits != 32 && v.Type().LaneBits != 64) || v.Type().LaneBits != multiplier.Type().LaneBits || v.Type().LaneBits != addend.Type().LaneBits {
		return Value{}
	}
	return Value{builder: v.builder, id: -1, expression: v.builder.newExpression(valueExpression{
		op: ir.OpFMA, bits: v.Type().LaneBits, typ: v.Type(), left: v, right: multiplier, third: addend,
	})}
}

// MulConst composes a width-matched constant multiplication. Addition around
// this expression may be selected as a scaled arithmetic or address operation.
func (v Value) MulConst(value uint64) Value {
	bits := v.Type().LaneBits
	if v.builder == nil || !validScalarBits(bits) {
		return Value{}
	}
	literal := Value{builder: v.builder, id: -1, expression: v.builder.newExpression(valueExpression{
		op: opConst, bits: bits, imm: value & integerMask(bits),
	})}
	if v.Type().Kind == ir.VectorKind {
		literal = Value{builder: v.builder, id: -1, expression: v.builder.newExpression(valueExpression{op: ir.OpVSplat, typ: v.Type(), bits: v.Type().LaneBits, left: literal})}
	}
	return expressionBinary(opMul, v, literal)
}

func (v Value) expressionShift(operation op, count uint8) Value {
	if v.builder == nil || !v.Type().Valid() {
		return Value{}
	}
	return Value{builder: v.builder, id: -1, expression: v.builder.newExpression(valueExpression{
		op: operation, bits: v.Type().LaneBits, typ: v.Type(), imm: uint64(count), left: v,
	})}
}

// ShiftLeft, ShiftRight, ShiftRightArithmetic, RotateLeft, and RotateRight
// compose width-aware shifts and rotations.
func (v Value) ShiftLeft(count uint8) Value  { return v.expressionShift(opShiftLeft, count) }
func (v Value) ShiftRight(count uint8) Value { return v.expressionShift(opShiftRight, count) }
func (v Value) ShiftRightArithmetic(count uint8) Value {
	return v.expressionShift(opShiftRightArithmetic, count)
}
func (v Value) RotateLeft(count uint8) Value  { return v.expressionShift(opRotateLeft, count) }
func (v Value) RotateRight(count uint8) Value { return v.expressionShift(opRotateRight, count) }

func (v Value) expressionShiftBy(operation op, count Value) Value {
	if v.builder == nil || v.builder != count.builder || !v.Type().Valid() || !count.Type().Valid() {
		return Value{}
	}
	return Value{builder: v.builder, id: -1, expression: v.builder.newExpression(valueExpression{
		op: operation, bits: v.Type().LaneBits, typ: v.Type(), left: v, right: count, byValue: true,
	})}
}

// ShiftLeftBy, ShiftRightBy, ShiftRightArithmeticBy, RotateLeftBy, and
// RotateRightBy compose shifts and rotations with a Value count.
func (v Value) ShiftLeftBy(count Value) Value  { return v.expressionShiftBy(opShiftLeft, count) }
func (v Value) ShiftRightBy(count Value) Value { return v.expressionShiftBy(opShiftRight, count) }
func (v Value) ShiftRightArithmeticBy(count Value) Value {
	return v.expressionShiftBy(opShiftRightArithmetic, count)
}
func (v Value) RotateLeftBy(count Value) Value  { return v.expressionShiftBy(opRotateLeft, count) }
func (v Value) RotateRightBy(count Value) Value { return v.expressionShiftBy(opRotateRight, count) }

// CastUnsigned composes a truncation or zero extension.
func (v Value) CastUnsigned(bits uint8) Value {
	if v.builder == nil || v.Type().Kind != ir.ScalarKind || !validScalarBits(bits) && bits != 128 {
		return Value{}
	}
	return Value{builder: v.builder, id: -1, expression: v.builder.newExpression(valueExpression{op: opCastUnsigned, bits: bits, left: v})}
}

// SignExtend composes sign extension of the low width bits to 64 bits.
func (v Value) SignExtend(width uint8) Value {
	if v.builder == nil || v.Type().Kind != ir.ScalarKind || !validScalarBits(v.Type().LaneBits) || width != 8 && width != 16 && width != 32 {
		return Value{}
	}
	return Value{builder: v.builder, id: -1, expression: v.builder.newExpression(valueExpression{op: opSignExtend, bits: 64, imm: uint64(width), left: v})}
}

// Select chooses between two equal-width expressions using a nonzero condition.
func (v Value) Select(ifTrue, ifFalse Value) Value {
	if v.builder == nil || v.Type().Kind != ir.ScalarKind || ifTrue.Type().Kind != ir.ScalarKind || ifFalse.Type().Kind != ir.ScalarKind || !validScalarBits(v.Type().LaneBits) || v.builder != ifTrue.builder || v.builder != ifFalse.builder ||
		!ifTrue.Type().Valid() || ifTrue.Type().LaneBits != ifFalse.Type().LaneBits {
		return Value{}
	}
	return Value{builder: v.builder, id: -1, expression: v.builder.newExpression(valueExpression{
		op: opSelect, bits: ifTrue.Type().LaneBits, typ: ifTrue.Type(), left: v, right: ifTrue, third: ifFalse,
	})}
}

func (b *Builder) literalValue(value Value) (uint64, bool) {
	if value.expression != nil && value.expression.op == opConst {
		return value.expression.imm, true
	}
	if b.valid(value) {
		return b.instructionLiteral(value.id)
	}
	return 0, false
}

func (b *Builder) scaledExpression(value Value) (Value, uint64, bool) {
	if value.expression == nil || value.expression.op != opMul {
		return Value{}, 0, false
	}
	for _, pair := range [][2]Value{{value.expression.left, value.expression.right}, {value.expression.right, value.expression.left}} {
		if scale, ok := b.literalValue(pair[1]); ok {
			return pair[0], scale, true
		}
	}
	return Value{}, 0, false
}

// materialize turns a pure tree into virtual instructions at its use site.
// Shared subtrees are evaluated once while their result remains in scope.
// materialize walks deferred values without putting expression depth on the
// Go stack. materializeOne only sees already-cached expression dependencies.
func (b *Builder) materialize(value Value) (Value, error) {
	if b == nil || !b.canAppend() || value.builder != b {
		return Value{}, b.invalidValue(value)
	}
	if value.expression == nil && value.composite == nil {
		return b.materializeOne(value)
	}
	cached := func(v Value) bool {
		return v.expression != nil && b.reusable(v.expression)
	}
	if cached(value) {
		return b.materializeOne(value)
	}
	type frame struct {
		value Value
		next  int
	}
	var initialWork [16]frame
	work := append(initialWork[:0], frame{value: value})
	for len(work) != 0 {
		top := &work[len(work)-1]
		if input, ok := b.materializeInput(top.value, top.next); ok {
			top.next++
			if input.builder != b {
				return Value{}, b.invalidValue(input)
			}
			if input.expression == nil && input.composite == nil {
				// The parent materializes (and captures) plain leaves. Doing
				// that here too emits an unused Get for outer-scope values.
				if !b.valid(input) {
					return Value{}, b.invalidValue(input)
				}
			} else if !cached(input) {
				work = append(work, frame{value: input})
			}
			continue
		}
		result, err := b.materializeOne(top.value)
		if err != nil {
			return Value{}, err
		}
		work = work[:len(work)-1]
		if len(work) == 0 {
			return result, nil
		}
	}
	return Value{}, ErrInvalidFunction
}

// materializeInput preserves the scaled-expression fold: visit the base and
// index directly, without emitting the multiply that the add absorbs.
func (b *Builder) materializeInput(value Value, index int) (Value, bool) {
	if value.composite != nil {
		if index < len(value.composite.parts) {
			return value.composite.parts[index], true
		}
		return Value{}, false
	}
	node := value.expression
	if node == nil || node.op == opConst {
		return Value{}, false
	}
	if node.op == opAdd && (node.bits == 64 || node.bits == NativeBits) {
		for _, pair := range [][2]Value{{node.left, node.right}, {node.right, node.left}} {
			if v, _, ok := b.scaledExpression(pair[1]); ok {
				switch index {
				case 0:
					return pair[0], true
				case 1:
					return v, true
				}
				return Value{}, false
			}
		}
	}
	if index == 0 {
		return node.left, true
	}
	switch node.op {
	case opSelect, ir.OpFMA:
		if index == 2 {
			return node.third, true
		}
		fallthrough
	case opAdd, opSub, opMul, opAnd, opOr, opXor, ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv, ir.OpVCarrylessMul:
		if index == 1 {
			return node.right, true
		}
	case opFloatMath:
		if index == 1 && node.right.builder != nil {
			return node.right, true
		}
	case opShiftLeft, opShiftRight, opShiftRightArithmetic, opRotateLeft, opRotateRight:
		if index == 1 && node.byValue {
			return node.right, true
		}
	}
	return Value{}, false
}

func (b *Builder) materializeOne(value Value) (Value, error) {
	if b == nil || !b.canAppend() || value.builder != b {
		return Value{}, b.invalidValue(value)
	}
	if value.expression == nil {
		if value.composite != nil {
			parts := make([]Value, len(value.composite.parts))
			for i, part := range value.composite.parts {
				var err error
				parts[i], err = b.materialize(part)
				if err != nil {
					return Value{}, err
				}
			}
			return compositeValue(parts...), nil
		}
		if !b.valid(value) {
			return Value{}, b.invalidValue(value)
		}
		if b.control != nil && value.id < b.control.start && b.captured(value.id) {
			return b.capture(value.id)
		}
		return value, nil
	}
	if b.reusable(value.expression) {
		return b.materialize(value.expression.materialized)
	}
	node := value.expression
	if node.op == opAdd && (node.bits == 64 || node.bits == NativeBits) {
		for _, pair := range [][2]Value{{node.left, node.right}, {node.right, node.left}} {
			index, scale, ok := b.scaledExpression(pair[1])
			if !ok {
				continue
			}
			base, err := b.materialize(pair[0])
			if err != nil {
				return Value{}, err
			}
			index, err = b.materialize(index)
			if err != nil {
				return Value{}, err
			}
			result, err := b.addScaled(base, index, scale)
			if err == nil {
				node.materialized, node.materializedIn = result, b.blockKey()
			}
			return result, err
		}
	}
	var result Value
	var err error
	if node.op == opConst {
		result, err = b.ConstBits(node.bits, node.imm)
	} else {
		left, leftErr := b.materialize(node.left)
		if leftErr != nil {
			return Value{}, leftErr
		}
		switch node.op {
		case opAdd, opSub, opMul, opAnd, opOr, opXor, ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv, opSelect:
			right, rightErr := b.materialize(node.right)
			if rightErr != nil {
				return Value{}, rightErr
			}
			switch node.op {
			case opSelect:
				third, thirdErr := b.materialize(node.third)
				if thirdErr != nil {
					return Value{}, thirdErr
				}
				result, err = b.Select(left, right, third)
			case ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv:
				result, err = b.floatBinary(node.op, left, right)
				if err == nil && node.op == ir.OpFAdd &&
					(node.left.expression != nil && node.left.expression.op == ir.OpFMul ||
						node.right.expression != nil && node.right.expression.op == ir.OpFMul) {
					b.setContract(result.id)
				}
			default:
				result, err = b.binary(node.op, left, right)
			}
		case opFloatMath:
			args := []Value{left}
			if node.right.builder != nil {
				right, rightErr := b.materialize(node.right)
				if rightErr != nil {
					return Value{}, rightErr
				}
				args = append(args, right)
			}
			result, err = b.floatMath(node.function, args...)
		case ir.OpVCarrylessMul:
			right, rightErr := b.materialize(node.right)
			if rightErr != nil {
				return Value{}, rightErr
			}
			result = b.append(instruction{Op: node.op, Type: node.typ, Bits: 64, A: left.id, B: right.id, Imm: node.imm})
		case ir.OpVSplat:
			result, err = b.emitSplat(node.typ, left)
		case ir.OpFSqrt:
			result, err = b.FSqrt(left)
		case ir.OpFConvert:
			result, err = b.FConvert(left, node.bits)
		case ir.OpFMA:
			right, rightErr := b.materialize(node.right)
			if rightErr != nil {
				return Value{}, rightErr
			}
			third, thirdErr := b.materialize(node.third)
			if thirdErr != nil {
				return Value{}, thirdErr
			}
			result, err = b.FMA(left, right, third)
		case opShiftLeft, opShiftRight, opShiftRightArithmetic, opRotateLeft, opRotateRight:
			if node.byValue {
				right, rightErr := b.materialize(node.right)
				if rightErr != nil {
					return Value{}, rightErr
				}
				result, err = b.shiftBy(node.op, left, right)
			} else {
				result, err = b.shiftImmediate(node.op, left, uint8(node.imm))
			}
		case opCastUnsigned:
			result, err = b.CastUnsigned(left, node.bits)
		case opSignExtend:
			result, err = b.SignExtend(left, uint8(node.imm))
		default:
			return Value{}, ErrInvalidFunction
		}
	}
	if err == nil {
		node.materialized, node.materializedIn = result, b.blockKey()
	}
	return result, err
}

// blockKey identifies the builder block that is open now. Every Bind starts
// a block with a new key, and Enter keeps the block: the edge a scope-entry
// split adds leaves the entry dominated by its predecessor. The entry block,
// before and after NewLabel creates the control record, is 0.
func (b *Builder) blockKey() int { return b.blockSerial }

// reusable reports whether node's materialization can stand for node here.
// A materialization from the current block or the entry block, which
// dominates every block, is reused. One from another block may not dominate
// this one, as with a value shared by both arms of a branch, so node is
// materialized again. When that is impossible because inputs have left
// scope (node was exported by Leave), the materialization is used, and
// Finish checks that its block dominates this one.
func (b *Builder) reusable(node *valueExpression) bool {
	c := node.materialized
	if c.builder == nil || !b.valid(c) && !b.validComposite(c) {
		return false
	}
	if node.materializedIn == 0 || node.materializedIn == b.blockKey() {
		return true
	}
	expression := Value{builder: b, id: -1, expression: node}
	for index := 0; ; index++ {
		input, ok := b.materializeInput(expression, index)
		if !ok {
			return false
		}
		if input.builder != b {
			return true
		}
		if input.expression == nil && input.composite == nil {
			if !b.valid(input) {
				return true
			}
		} else if !b.validExpression(input) {
			return true
		}
	}
}

// materializeValues materializes each value. Callers only read the result,
// so it is values itself when no value changes, as for materialized leaves.
func (b *Builder) materializeValues(values []Value) ([]Value, error) {
	var result []Value
	for i, value := range values {
		materialized, err := b.materialize(value)
		if err != nil {
			return nil, err
		}
		if result == nil && materialized != value {
			result = make([]Value, len(values))
			copy(result, values[:i])
		}
		if result != nil {
			result[i] = materialized
		}
	}
	if result == nil {
		return values, nil
	}
	return result, nil
}

func (b *Builder) validExpression(value Value) bool {
	if b == nil || !b.canAppend() {
		return false
	}
	// Appending instructions cannot invalidate existing leaves. Scope exits
	// and control-block changes advance the epoch because visibility changes.
	epoch := b.validationEpoch + 1
	if value.builder == b && value.expression != nil && value.expression.validEpoch == epoch {
		return true
	}
	work := append(b.expressionWork[:0], value)
	seen := b.expressionSeen[:0]
	b.expressionWalk++
	walk := b.expressionWalk
	defer func() { clear(work); clear(seen); b.expressionWork = work[:0]; b.expressionSeen = seen[:0] }()
	for len(work) != 0 {
		value := work[len(work)-1]
		work[len(work)-1] = Value{}
		work = work[:len(work)-1]
		if value.builder != b {
			return false
		}
		if value.expression != nil {
			if value.expression.validEpoch == epoch {
				continue
			}
			if cached := value.expression.materialized; cached.builder != nil && (b.valid(cached) || b.validComposite(cached)) {
				continue
			}
			node := value.expression
			if node.seenWalk == walk {
				continue
			}
			node.seenWalk = walk
			seen = append(seen, node)
			if node.op == opConst {
				if !validScalarBits(node.bits) {
					return false
				}
				continue
			}
			work = append(work, node.left)
			switch node.op {
			case opAdd, opSub, opMul, opAnd, opOr, opXor, ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv, ir.OpVCarrylessMul:
				work = append(work, node.right)
			case ir.OpFMA, opSelect:
				work = append(work, node.right, node.third)
			case opShiftLeft, opShiftRight, opShiftRightArithmetic, opRotateLeft, opRotateRight:
				if node.byValue {
					work = append(work, node.right)
				}
			case opFloatMath:
				if node.right.builder != nil {
					work = append(work, node.right)
				}
			case ir.OpVSplat, opCastUnsigned, opSignExtend, ir.OpFSqrt, ir.OpFConvert:
			default:
				return false
			}
		} else if value.composite != nil {
			work = append(work, value.composite.parts...)
		} else if !b.valid(value) {
			return false
		}
	}
	for _, node := range seen {
		node.validEpoch = epoch
	}
	return true
}

// Slab backing arrays never move, so Values keep stable node addresses. A
// finished builder is consumed; its slabs are never reused by another builder.
// The mutex keeps pure Value composition safe from several goroutines, which
// TestConcurrentPureValueComposition covers. The rest of a Builder is not.
type expressionArena struct {
	mu         sync.Mutex
	slab       []valueExpression
	predicates []predicateNode
}

func (b *Builder) newExpression(value valueExpression) *valueExpression {
	arena := b.expressionArena
	if arena == nil {
		return newDetachedExpression(&value)
	}
	arena.mu.Lock()
	if len(arena.slab) == cap(arena.slab) {
		size := min(256, max(8, 2*cap(arena.slab)))
		arena.slab = make([]valueExpression, 0, size)
	}
	arena.slab = append(arena.slab, value)
	node := &arena.slab[len(arena.slab)-1]
	arena.mu.Unlock()
	return node
}

// newPredicate allocates a predicate node in the Builder's arena.
func (b *Builder) newPredicate(node predicateNode) *predicateNode {
	arena := b.expressionArena
	if arena == nil {
		return newDetachedPredicate(&node)
	}
	arena.mu.Lock()
	if len(arena.predicates) == cap(arena.predicates) {
		size := min(256, max(8, 2*cap(arena.predicates)))
		arena.predicates = make([]predicateNode, 0, size)
	}
	arena.predicates = append(arena.predicates, node)
	result := &arena.predicates[len(arena.predicates)-1]
	arena.mu.Unlock()
	return result
}

//go:noinline
func newDetachedPredicate(node *predicateNode) *predicateNode {
	result := new(predicateNode)
	*result = *node
	return result
}

// newDetachedExpression allocates one node for a zero Builder, which has no
// arena, so pure Value composition needs no lazy shared mutation. It copies
// value so that newExpression's argument stays on the stack.
//
//go:noinline
func newDetachedExpression(value *valueExpression) *valueExpression {
	node := new(valueExpression)
	*node = *value
	return node
}
