package builder

// selectWide chooses one composite scalar using a nonzero scalar condition.
func selectWide(b *Builder, condition Value, ifTrue, ifFalse Value) (Value, error) {
	if !b.valid(condition) || !b.validComposite(ifTrue) || !b.validComposite(ifFalse) {
		return Value{}, ErrInvalidFunction
	}
	lo, err := b.Select(condition, wideLow(ifTrue), wideLow(ifFalse))
	if err != nil {
		return Value{}, err
	}
	hi, err := b.Select(condition, wideHigh(ifTrue), wideHigh(ifFalse))
	if err != nil {
		return Value{}, err
	}
	return compositeValue(lo, hi), nil
}

func shiftWideImmediate(b *Builder, operation op, value Value, count uint8) (Value, error) {
	switch operation {
	case opShiftLeft:
		return shiftLeftWide(b, value, count)
	case opShiftRight:
		return shiftRightWide(b, value, count)
	case opShiftRightArithmetic:
		return shiftRightArithmeticWide(b, value, count)
	case opRotateLeft:
		return rotateLeftWide(b, value, count)
	case opRotateRight:
		return rotateRightWide(b, value, count)
	default:
		return Value{}, ErrInvalidFunction
	}
}

func shiftWideBy(b *Builder, operation op, value Value, count Value) (Value, error) {
	if !b.validComposite(value) || !b.valid(count) {
		return Value{}, ErrInvalidFunction
	}
	wideCount, err := b.CastUnsigned(count, 64)
	if err != nil {
		return Value{}, err
	}
	result := value
	for amount := uint8(1); amount < 128; amount <<= 1 {
		mask, err := b.Const(uint64(amount))
		if err != nil {
			return Value{}, err
		}
		selected, err := b.And(wideCount, mask)
		if err != nil {
			return Value{}, err
		}
		shifted, err := shiftWideImmediate(b, operation, result, amount)
		if err != nil {
			return Value{}, err
		}
		result, err = selectWide(b, selected, shifted, result)
		if err != nil {
			return Value{}, err
		}
	}
	if operation == opRotateLeft || operation == opRotateRight {
		return result, nil
	}
	tooLarge, err := b.ShiftRight(wideCount, 7)
	if err != nil {
		return Value{}, err
	}
	var saturated Value
	if operation == opShiftRightArithmetic {
		saturated, err = shiftRightArithmeticWide(b, value, 127)
	} else {
		saturated, err = b.ConstBits(128, 0)
	}
	if err != nil {
		return Value{}, err
	}
	return selectWide(b, tooLarge, saturated, result)
}
