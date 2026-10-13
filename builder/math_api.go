package builder

// FSin computes the sine of a floating point bit pattern.
func (b *Builder) FSin(x Value) (Value, error) { return b.floatMath("Sin", x) }

// FSin composes a sine expression.
func (v Value) FSin() Value { return mathExpression("Sin", v) }

// FCos computes the cosine of a floating point bit pattern.
func (b *Builder) FCos(x Value) (Value, error) { return b.floatMath("Cos", x) }

// FCos composes a cosine expression.
func (v Value) FCos() Value { return mathExpression("Cos", v) }

// FTan computes the tangent of a floating point bit pattern.
func (b *Builder) FTan(x Value) (Value, error) { return b.floatMath("Tan", x) }

// FTan composes a tangent expression.
func (v Value) FTan() Value { return mathExpression("Tan", v) }

// FAsin computes the arcsine of a floating point bit pattern.
func (b *Builder) FAsin(x Value) (Value, error) { return b.floatMath("Asin", x) }

// FAsin composes an arcsine expression.
func (v Value) FAsin() Value { return mathExpression("Asin", v) }

// FAcos computes the arccosine of a floating point bit pattern.
func (b *Builder) FAcos(x Value) (Value, error) { return b.floatMath("Acos", x) }

// FAcos composes an arccosine expression.
func (v Value) FAcos() Value { return mathExpression("Acos", v) }

// FAtan computes the arctangent of a floating point bit pattern.
func (b *Builder) FAtan(x Value) (Value, error) { return b.floatMath("Atan", x) }

// FAtan composes an arctangent expression.
func (v Value) FAtan() Value { return mathExpression("Atan", v) }

// FSinh computes the hyperbolic sine of a floating point bit pattern.
func (b *Builder) FSinh(x Value) (Value, error) { return b.floatMath("Sinh", x) }

// FSinh composes a hyperbolic sine expression.
func (v Value) FSinh() Value { return mathExpression("Sinh", v) }

// FCosh computes the hyperbolic cosine of a floating point bit pattern.
func (b *Builder) FCosh(x Value) (Value, error) { return b.floatMath("Cosh", x) }

// FCosh composes a hyperbolic cosine expression.
func (v Value) FCosh() Value { return mathExpression("Cosh", v) }

// FTanh computes the hyperbolic tangent of a floating point bit pattern.
func (b *Builder) FTanh(x Value) (Value, error) { return b.floatMath("Tanh", x) }

// FTanh composes a hyperbolic tangent expression.
func (v Value) FTanh() Value { return mathExpression("Tanh", v) }

// FAsinh computes the inverse hyperbolic sine of a floating point bit pattern.
func (b *Builder) FAsinh(x Value) (Value, error) { return b.floatMath("Asinh", x) }

// FAsinh composes an inverse hyperbolic sine expression.
func (v Value) FAsinh() Value { return mathExpression("Asinh", v) }

// FAcosh computes the inverse hyperbolic cosine of a floating point bit pattern.
func (b *Builder) FAcosh(x Value) (Value, error) { return b.floatMath("Acosh", x) }

// FAcosh composes an inverse hyperbolic cosine expression.
func (v Value) FAcosh() Value { return mathExpression("Acosh", v) }

// FAtanh computes the inverse hyperbolic tangent of a floating point bit pattern.
func (b *Builder) FAtanh(x Value) (Value, error) { return b.floatMath("Atanh", x) }

// FAtanh composes an inverse hyperbolic tangent expression.
func (v Value) FAtanh() Value { return mathExpression("Atanh", v) }

// FExp computes the base-e exponential of a floating point bit pattern.
func (b *Builder) FExp(x Value) (Value, error) { return b.floatMath("Exp", x) }

// FExp composes a base-e exponential expression.
func (v Value) FExp() Value { return mathExpression("Exp", v) }

// FExp2 computes the base-2 exponential of a floating point bit pattern.
func (b *Builder) FExp2(x Value) (Value, error) { return b.floatMath("Exp2", x) }

// FExp2 composes a base-2 exponential expression.
func (v Value) FExp2() Value { return mathExpression("Exp2", v) }

// FExpm1 computes the base-e exponential minus one of a floating point bit pattern.
func (b *Builder) FExpm1(x Value) (Value, error) { return b.floatMath("Expm1", x) }

// FExpm1 composes a base-e exponential minus one expression.
func (v Value) FExpm1() Value { return mathExpression("Expm1", v) }

// FLog computes the natural logarithm of a floating point bit pattern.
func (b *Builder) FLog(x Value) (Value, error) { return b.floatMath("Log", x) }

// FLog composes a natural logarithm expression.
func (v Value) FLog() Value { return mathExpression("Log", v) }

// FLog2 computes the base-2 logarithm of a floating point bit pattern.
func (b *Builder) FLog2(x Value) (Value, error) { return b.floatMath("Log2", x) }

// FLog2 composes a base-2 logarithm expression.
func (v Value) FLog2() Value { return mathExpression("Log2", v) }

// FLog10 computes the base-10 logarithm of a floating point bit pattern.
func (b *Builder) FLog10(x Value) (Value, error) { return b.floatMath("Log10", x) }

// FLog10 composes a base-10 logarithm expression.
func (v Value) FLog10() Value { return mathExpression("Log10", v) }

// FLog1p computes the natural logarithm of one plus the input of a floating point bit pattern.
func (b *Builder) FLog1p(x Value) (Value, error) { return b.floatMath("Log1p", x) }

// FLog1p composes a natural logarithm of one plus the input expression.
func (v Value) FLog1p() Value { return mathExpression("Log1p", v) }

// FCbrt computes the cube root of a floating point bit pattern.
func (b *Builder) FCbrt(x Value) (Value, error) { return b.floatMath("Cbrt", x) }

// FCbrt composes a cube root expression.
func (v Value) FCbrt() Value { return mathExpression("Cbrt", v) }

// FFloor computes the floor of a floating point bit pattern.
func (b *Builder) FFloor(x Value) (Value, error) { return b.floatMath("Floor", x) }

// FFloor composes a floor expression.
func (v Value) FFloor() Value { return mathExpression("Floor", v) }

// FCeil computes the ceiling of a floating point bit pattern.
func (b *Builder) FCeil(x Value) (Value, error) { return b.floatMath("Ceil", x) }

// FCeil composes a ceiling expression.
func (v Value) FCeil() Value { return mathExpression("Ceil", v) }

// FTrunc computes the integer part of a floating point bit pattern.
func (b *Builder) FTrunc(x Value) (Value, error) { return b.floatMath("Trunc", x) }

// FTrunc composes a integer part expression.
func (v Value) FTrunc() Value { return mathExpression("Trunc", v) }

// FRound computes the nearest integer, rounding ties away from zero of a floating point bit pattern.
func (b *Builder) FRound(x Value) (Value, error) { return b.floatMath("Round", x) }

// FRound composes a nearest integer, rounding ties away from zero expression.
func (v Value) FRound() Value { return mathExpression("Round", v) }

// FRoundToEven computes the nearest integer, rounding ties to even of a floating point bit pattern.
func (b *Builder) FRoundToEven(x Value) (Value, error) { return b.floatMath("RoundToEven", x) }

// FRoundToEven composes a nearest integer, rounding ties to even expression.
func (v Value) FRoundToEven() Value { return mathExpression("RoundToEven", v) }

// FPow computes the power of matching floating point bit patterns.
func (b *Builder) FPow(x, y Value) (Value, error) { return b.floatMath("Pow", x, y) }

// FPow composes a power expression.
func (v Value) FPow(other Value) Value { return mathExpression("Pow", v, other) }

// FAtan2 computes the angle of (x, y), in radians, from floating point bit patterns.
func (b *Builder) FAtan2(y, x Value) (Value, error) { return b.floatMath("Atan2", y, x) }

// FAtan2 composes a two-argument arctangent expression.
func (v Value) FAtan2(other Value) Value { return mathExpression("Atan2", v, other) }

// FHypot computes the Euclidean norm of matching floating point bit patterns.
func (b *Builder) FHypot(x, y Value) (Value, error) { return b.floatMath("Hypot", x, y) }

// FHypot composes a Euclidean norm expression.
func (v Value) FHypot(other Value) Value { return mathExpression("Hypot", v, other) }
