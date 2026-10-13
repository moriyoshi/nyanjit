package builder

import "github.com/moriyoshi/nyanjit/ir"

// valueExpression is a pure, immutable tree. A Value leaf is captured when the
// tree is built, while loads and calls stay in the Builder's ordered stream.
// Its nodes are turned into virtual instructions only when an operand is used.
type valueExpression struct {
	typ      Type
	op       op
	bits     uint8
	imm      uint64
	left     Value
	right    Value
	third    Value
	byValue  bool
	function string
}

// LiteralBits creates a deferred integer constant. It adds no instruction
// until the value is used by an operation, predicate, or control edge.
func (b *Builder) LiteralBits(bits uint8, value uint64) (Value, error) {
	if !b.canAppend() || !validScalarBits(bits) {
		return Value{}, ErrInvalidFunction
	}
	return Value{builder: b, id: -1, expression: &valueExpression{op: opConst, bits: bits, imm: value & integerMask(bits)}}, nil
}

// Literal creates a deferred 64-bit integer constant.
func (b *Builder) Literal(value uint64) (Value, error) { return b.LiteralBits(64, value) }

func expressionBinary(operation op, left, right Value) Value {
	if left.Type().Kind != ScalarKind && (operation == ir.OpFAdd || operation == ir.OpFSub || operation == ir.OpFMul || operation == ir.OpFDiv) {
		return Value{}
	}
	if left.builder == nil || left.builder != right.builder || !left.Type().Valid() || left.Type() != right.Type() {
		return Value{}
	}
	return Value{builder: left.builder, id: -1, expression: &valueExpression{
		op: operation, bits: left.Type().LaneBits, typ: left.Type(), left: left, right: right,
	}}
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
	if v.builder == nil || v.Type().Kind != ScalarKind || v.Type().LaneBits != 32 && v.Type().LaneBits != 64 {
		return Value{}
	}
	return Value{builder: v.builder, id: -1, expression: &valueExpression{
		op: ir.OpFSqrt, bits: v.Type().LaneBits, typ: v.Type(), left: v,
	}}
}

// FConvert composes a conversion between binary32 and binary64 bit patterns.
func (v Value) FConvert(bits uint8) Value {
	if v.builder == nil || v.Type().Kind != ScalarKind || v.Type().LaneBits != 32 && v.Type().LaneBits != 64 || bits != 32 && bits != 64 {
		return Value{}
	}
	if v.Type().LaneBits == bits {
		return v
	}
	return Value{builder: v.builder, id: -1, expression: &valueExpression{op: ir.OpFConvert, bits: bits, left: v}}
}

// FMA composes an explicitly fused multiply-add expression.
func (v Value) FMA(multiplier, addend Value) Value {
	if v.builder == nil || v.Type().Kind != ScalarKind || multiplier.Type().Kind != ScalarKind || addend.Type().Kind != ScalarKind || v.builder != multiplier.builder || v.builder != addend.builder ||
		(v.Type().LaneBits != 32 && v.Type().LaneBits != 64) || v.Type().LaneBits != multiplier.Type().LaneBits || v.Type().LaneBits != addend.Type().LaneBits {
		return Value{}
	}
	return Value{builder: v.builder, id: -1, expression: &valueExpression{
		op: ir.OpFMA, bits: v.Type().LaneBits, typ: v.Type(), left: v, right: multiplier, third: addend,
	}}
}

// MulConst composes a width-matched constant multiplication. Addition around
// this expression may be selected as a scaled arithmetic or address operation.
func (v Value) MulConst(value uint64) Value {
	bits := v.Type().LaneBits
	if v.builder == nil || !validScalarBits(bits) {
		return Value{}
	}
	literal := Value{builder: v.builder, id: -1, expression: &valueExpression{
		op: opConst, bits: bits, imm: value & integerMask(bits),
	}}
	if v.Type().Kind == VectorKind {
		literal = Value{builder: v.builder, id: -1, expression: &valueExpression{op: ir.OpVSplat, typ: v.Type(), bits: v.Type().LaneBits, left: literal}}
	}
	return expressionBinary(opMul, v, literal)
}

func (v Value) expressionShift(operation op, count uint8) Value {
	if v.builder == nil || !v.Type().Valid() {
		return Value{}
	}
	return Value{builder: v.builder, id: -1, expression: &valueExpression{
		op: operation, bits: v.Type().LaneBits, typ: v.Type(), imm: uint64(count), left: v,
	}}
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
	return Value{builder: v.builder, id: -1, expression: &valueExpression{
		op: operation, bits: v.Type().LaneBits, typ: v.Type(), left: v, right: count, byValue: true,
	}}
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
	if v.builder == nil || v.Type().Kind != ScalarKind || !validScalarBits(bits) && bits != 128 {
		return Value{}
	}
	return Value{builder: v.builder, id: -1, expression: &valueExpression{op: opCastUnsigned, bits: bits, left: v}}
}

// SignExtend composes sign extension of the low width bits to 64 bits.
func (v Value) SignExtend(width uint8) Value {
	if v.builder == nil || v.Type().Kind != ScalarKind || !validScalarBits(v.Type().LaneBits) || width != 8 && width != 16 && width != 32 {
		return Value{}
	}
	return Value{builder: v.builder, id: -1, expression: &valueExpression{op: opSignExtend, bits: 64, imm: uint64(width), left: v}}
}

// Select chooses between two equal-width expressions using a nonzero condition.
func (v Value) Select(ifTrue, ifFalse Value) Value {
	if v.builder == nil || v.Type().Kind != ScalarKind || ifTrue.Type().Kind != ScalarKind || ifFalse.Type().Kind != ScalarKind || !validScalarBits(v.Type().LaneBits) || v.builder != ifTrue.builder || v.builder != ifFalse.builder ||
		!ifTrue.Type().Valid() || ifTrue.Type().LaneBits != ifFalse.Type().LaneBits {
		return Value{}
	}
	return Value{builder: v.builder, id: -1, expression: &valueExpression{
		op: opSelect, bits: ifTrue.Type().LaneBits, typ: ifTrue.Type(), left: v, right: ifTrue, third: ifFalse,
	}}
}

func (b *Builder) literalValue(value Value) (uint64, bool) {
	if value.expression != nil && value.expression.op == opConst {
		return value.expression.imm, true
	}
	if b.valid(value) && b.code[value.id].Op == opConst && b.code[value.id].Symbol == "" {
		return b.code[value.id].Imm, true
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
func (b *Builder) materialize(value Value) (Value, error) {
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
		if b.control != nil && value.id < b.control.start {
			return b.capture(value.id)
		}
		return value, nil
	}
	if cached, ok := b.expressions[value.expression]; ok && (b.valid(cached) || b.validComposite(cached)) {
		return b.materialize(cached)
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
				if b.expressions == nil {
					b.expressions = make(map[*valueExpression]Value)
				}
				b.expressions[node] = result
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
					b.code[result.id].Contract = true
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
		if b.expressions == nil {
			b.expressions = make(map[*valueExpression]Value)
		}
		b.expressions[node] = result
	}
	return result, err
}

func (b *Builder) materializeValues(values []Value) ([]Value, error) {
	result := make([]Value, len(values))
	for i, value := range values {
		var err error
		result[i], err = b.materialize(value)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (b *Builder) validExpression(value Value) bool {
	if b == nil || value.builder != b || !b.canAppend() {
		return false
	}
	if value.expression != nil {
		if cached, ok := b.expressions[value.expression]; ok && (b.valid(cached) || b.validComposite(cached)) {
			return true
		}
		node := value.expression
		if node.op == opConst {
			return validScalarBits(node.bits)
		}
		if !b.validExpression(node.left) {
			return false
		}
		switch node.op {
		case opAdd, opSub, opMul, opAnd, opOr, opXor, ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv:
			return b.validExpression(node.right)
		case ir.OpFMA:
			return b.validExpression(node.right) && b.validExpression(node.third)
		case opSelect:
			return b.validExpression(node.right) && b.validExpression(node.third)
		case opShiftLeft, opShiftRight, opShiftRightArithmetic, opRotateLeft, opRotateRight:
			return !node.byValue || b.validExpression(node.right)
		case opFloatMath:
			return node.right.builder == nil || b.validExpression(node.right)
		case ir.OpVSplat, opCastUnsigned, opSignExtend, ir.OpFSqrt, ir.OpFConvert:
			return true
		default:
			return false
		}
	}
	if value.composite != nil {
		for _, part := range value.composite.parts {
			if !b.validExpression(part) {
				return false
			}
		}
		return true
	}
	return b.valid(value)
}
