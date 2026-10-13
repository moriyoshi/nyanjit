package builder

import (
	"fmt"
	"math"

	"github.com/moriyoshi/nyanjit/ir"
)

// Math functions expand inline into branch-free sequences of Builder
// primitives. They stay inside the current block, make no calls, and need no
// stack frame or call-capable compile mode. Special cases are chosen with
// Select, so every path of a function is evaluated. Results are computed in
// binary64; binary32 operands are widened and the result is narrowed.
// The algorithms and coefficients are adapted from Go's math package.
const opFloatMath op = 255 // Expression-only; materialized as an inline expansion.

// expandMathFunction returns false for an unknown function name.
func expandMathFunction(m *mathEmitter, name string, a []Value) (Value, bool) {
	switch name {
	case "Sin":
		return m.sin(a[0]), true
	case "Cos":
		return m.cos(a[0]), true
	case "Tan":
		return m.tan(a[0]), true
	case "Asin":
		return m.asin(a[0]), true
	case "Acos":
		return m.acos(a[0]), true
	case "Atan":
		return m.atan(a[0]), true
	case "Sinh":
		return m.sinh(a[0]), true
	case "Cosh":
		return m.cosh(a[0]), true
	case "Tanh":
		return m.tanh(a[0]), true
	case "Asinh":
		return m.asinh(a[0]), true
	case "Acosh":
		return m.acosh(a[0]), true
	case "Atanh":
		return m.atanh(a[0]), true
	case "Exp":
		return m.exp(a[0]), true
	case "Exp2":
		return m.exp2(a[0]), true
	case "Expm1":
		return m.expm1(a[0]), true
	case "Log":
		return m.log(a[0]), true
	case "Log2":
		return m.log2(a[0]), true
	case "Log10":
		return m.log10(a[0]), true
	case "Log1p":
		return m.log1p(a[0]), true
	case "Cbrt":
		return m.cbrt(a[0]), true
	case "Floor":
		return m.floor(a[0]), true
	case "Ceil":
		return m.ceil(a[0]), true
	case "Trunc":
		return m.trunc(a[0]), true
	case "Round":
		return m.round(a[0]), true
	case "RoundToEven":
		return m.roundToEven(a[0]), true
	case "Pow":
		return m.pow(a[0], a[1]), true
	case "Atan2":
		return m.atan2(a[0], a[1]), true
	case "Hypot":
		return m.hypot(a[0], a[1]), true
	}
	return Value{}, false
}

func (b *Builder) floatMath(name string, args ...Value) (Value, error) {
	if b == nil || len(args) == 0 {
		return Value{}, ErrInvalidFunction
	}
	width := args[0].Type().LaneBits
	if width != 32 && width != 64 {
		return Value{}, ErrInvalidFunction
	}
	deferred := false
	for _, x := range args {
		if !b.validExpression(x) || x.Type().LaneBits != width {
			return Value{}, fmt.Errorf("F%s: operands must be in scope with matching floating widths: %w", name, ErrInvalidFunction)
		}
		deferred = deferred || x.expression != nil
	}
	if deferred {
		return mathExpression(name, args...), nil
	}
	return b.expandMath(func(m *mathEmitter) Value {
		wide := make([]Value, len(args))
		for i, x := range args {
			wide[i] = m.take(b.FConvert(x, 64))
		}
		result, ok := expandMathFunction(m, name, wide)
		if !ok {
			m.take(Value{}, fmt.Errorf("F%s: unknown math function: %w", name, ErrInvalidFunction))
		}
		return m.take(b.FConvert(result, uint8(width)))
	})
}

type mathFailure struct{ err error }

func (b *Builder) expandMath(expand func(*mathEmitter) Value) (result Value, err error) {
	defer func() {
		if p := recover(); p != nil {
			failure, ok := p.(mathFailure)
			if !ok {
				panic(p)
			}
			result, err = Value{}, failure.err
		}
	}()
	return expand(&mathEmitter{b: b}), nil
}

// mathEmitter appends binary64 operations and stops the expansion at the
// first construction error.
type mathEmitter struct{ b *Builder }

const (
	signBit  = uint64(1) << 63
	absMask  = signBit - 1
	fracMask = uint64(1)<<52 - 1
	infBits  = uint64(0x7ff0000000000000)
	nanBits  = uint64(0x7ff8000000000001)
	oneBits  = uint64(0x3ff0000000000000)
	magicInt = uint64(0x4338000000000000) // 1.5 * 2^52
)

func (m *mathEmitter) take(v Value, err error) Value {
	if err != nil {
		panic(mathFailure{err})
	}
	return v
}

func (m *mathEmitter) c(x uint64) Value             { return m.take(m.b.Const(x)) }
func (m *mathEmitter) f(x float64) Value            { return m.c(math.Float64bits(x)) }
func (m *mathEmitter) add(x, y Value) Value         { return m.take(m.b.Add(x, y)) }
func (m *mathEmitter) sub(x, y Value) Value         { return m.take(m.b.Sub(x, y)) }
func (m *mathEmitter) mul(x, y Value) Value         { return m.take(m.b.Mul(x, y)) }
func (m *mathEmitter) and(x, y Value) Value         { return m.take(m.b.And(x, y)) }
func (m *mathEmitter) or(x, y Value) Value          { return m.take(m.b.Or(x, y)) }
func (m *mathEmitter) xor(x, y Value) Value         { return m.take(m.b.Xor(x, y)) }
func (m *mathEmitter) andc(x Value, k uint64) Value { return m.and(x, m.c(k)) }
func (m *mathEmitter) shl(x Value, n uint8) Value {
	return m.take(m.b.ShiftLeft(x, n))
}
func (m *mathEmitter) shr(x Value, n uint8) Value {
	return m.take(m.b.ShiftRight(x, n))
}
func (m *mathEmitter) sar(x Value, n uint8) Value {
	return m.take(m.b.ShiftRightArithmetic(x, n))
}
func (m *mathEmitter) shlBy(x, n Value) Value { return m.take(m.b.ShiftLeftBy(x, n)) }
func (m *mathEmitter) shrBy(x, n Value) Value { return m.take(m.b.ShiftRightBy(x, n)) }
func (m *mathEmitter) sel(c, x, y Value) Value {
	return m.take(m.b.Select(c, x, y))
}
func (m *mathEmitter) mulHigh(x, y Value) Value {
	return m.b.append(instruction{Op: ir.OpMulHigh, Bits: 64, A: x.id, B: y.id})
}
func (m *mathEmitter) load(address Value) Value { return m.take(m.b.Load64(address)) }

func (m *mathEmitter) fadd(x, y Value) Value { return m.take(m.b.FAdd(x, y)) }
func (m *mathEmitter) fsub(x, y Value) Value { return m.take(m.b.FSub(x, y)) }
func (m *mathEmitter) fmul(x, y Value) Value { return m.take(m.b.FMul(x, y)) }
func (m *mathEmitter) fdiv(x, y Value) Value { return m.take(m.b.FDiv(x, y)) }
func (m *mathEmitter) fsqrt(x Value) Value   { return m.take(m.b.FSqrt(x)) }
func (m *mathEmitter) fneg(x Value) Value    { return m.xor(x, m.c(signBit)) }

// Predicates are 0 or 1.

func (m *mathEmitter) not(p Value) Value { return m.xor(p, m.c(1)) }

// lt compares integers whose difference fits in 63 bits, such as float
// magnitudes or small signed integers.
func (m *mathEmitter) lt(x, y Value) Value         { return m.shr(m.sub(x, y), 63) }
func (m *mathEmitter) ltc(x Value, y uint64) Value { return m.lt(x, m.c(y)) }
func (m *mathEmitter) gtc(x Value, y uint64) Value { return m.lt(m.c(y), x) }
func (m *mathEmitter) isZero(x Value) Value {
	return m.shr(m.and(m.sub(x, m.c(1)), m.xor(x, m.c(^uint64(0)))), 63)
}
func (m *mathEmitter) eq(x, y Value) Value         { return m.isZero(m.xor(x, y)) }
func (m *mathEmitter) eqc(x Value, y uint64) Value { return m.eq(x, m.c(y)) }

func (m *mathEmitter) abs(x Value) Value   { return m.andc(x, absMask) }
func (m *mathEmitter) sign(x Value) Value  { return m.shr(x, 63) }
func (m *mathEmitter) isNaN(x Value) Value { return m.gtc(m.abs(x), infBits) }
func (m *mathEmitter) isInf(x Value) Value { return m.eqc(m.abs(x), infBits) }
func (m *mathEmitter) withSign(x, s Value) Value {
	return m.or(x, m.shl(s, 63))
}
func (m *mathEmitter) copysign(x, y Value) Value {
	return m.or(m.abs(x), m.andc(y, signBit))
}

// rint rounds |x| < 2^51 to the nearest integer, returning it as a float and
// as a signed integer.
func (m *mathEmitter) rint(x Value) (Value, Value) {
	t := m.fadd(x, m.c(magicInt))
	return m.fsub(t, m.c(magicInt)), m.sub(t, m.c(magicInt))
}

// float converts a signed integer |k| < 2^51 exactly.
func (m *mathEmitter) float(k Value) Value {
	return m.fsub(m.add(k, m.c(magicInt)), m.c(magicInt))
}

// pow2 returns 2^n for a normal exponent n.
func (m *mathEmitter) pow2(n Value) Value { return m.shl(m.add(n, m.c(1023)), 52) }

// ldexp scales y by 2^k for -1075 <= k <= 1024 with one rounding.
func (m *mathEmitter) ldexp(y, k Value) Value {
	k1 := m.sar(k, 1)
	return m.fmul(m.fmul(y, m.pow2(k1)), m.pow2(m.sub(k, k1)))
}

// horner evaluates c[0] + t*(c[1] + t*(c[2] + ...)).
func (m *mathEmitter) horner(t Value, c ...float64) Value {
	acc := m.f(c[len(c)-1])
	for i := len(c) - 2; i >= 0; i-- {
		acc = m.fadd(m.f(c[i]), m.fmul(t, acc))
	}
	return acc
}

// leading evaluates ((c[0]*t + c[1])*t + c[2])...
func (m *mathEmitter) leading(t Value, c ...float64) Value {
	acc := m.f(c[0])
	for _, k := range c[1:] {
		acc = m.fadd(m.fmul(acc, t), m.f(k))
	}
	return acc
}

// monic evaluates ((t + c[0])*t + c[1])...
func (m *mathEmitter) monic(t Value, c ...float64) Value {
	acc := m.fadd(t, m.f(c[0]))
	for _, k := range c[1:] {
		acc = m.fadd(m.fmul(acc, t), m.f(k))
	}
	return acc
}

func (m *mathEmitter) pi4Table() Value { return m.take(m.b.Symbol(ir.MathPi4Symbol)) }

func mathExpression(name string, args ...Value) Value {
	left := args[0]
	if left.builder == nil || left.Type().LaneBits != 32 && left.Type().LaneBits != 64 {
		return Value{}
	}
	node := &valueExpression{op: opFloatMath, bits: uint8(left.Type().LaneBits), left: left, function: name}
	if len(args) == 2 {
		if left.builder != args[1].builder || left.Type().LaneBits != args[1].Type().LaneBits {
			return Value{}
		}
		node.right = args[1]
	}
	return Value{builder: left.builder, id: -1, expression: node}
}
