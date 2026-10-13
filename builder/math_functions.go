package builder

// Branch-free binary64 math functions. Each one follows the algorithm of the
// corresponding function in Go's math package (which takes most of it from
// FreeBSD's msun and Cephes); see builder/MATH-LICENSE. Branches in the
// originals become Select chains: special cases are applied last, from the
// lowest to the highest precedence, and inputs for impossible paths are
// replaced with harmless values so no path can fault.

import (
	"github.com/moriyoshi/nyanjit/ir"
	"math"
)

// Rounding.

// trunc clears the fraction bits below the binary point.
func (m *mathEmitter) trunc(x Value) Value {
	e := m.sub(m.andc(m.shr(x, 52), 0x7ff), m.c(1023))
	t := m.and(x, m.xor(m.shrBy(m.c(fracMask), e), m.c(^uint64(0))))
	return m.sel(m.ltc(m.abs(x), oneBits), m.andc(x, signBit), t)
}

func (m *mathEmitter) floor(x Value) Value {
	t := m.trunc(x)
	down := m.and(m.sign(x), m.lt(m.abs(t), m.abs(x)))
	return m.fsub(t, m.sel(down, m.f(1), m.f(0)))
}

func (m *mathEmitter) ceil(x Value) Value { return m.fneg(m.floor(m.fneg(x))) }

func (m *mathEmitter) round(x Value) Value {
	ax, s := m.abs(x), m.andc(x, signBit)
	e := m.sub(m.andc(m.shr(x, 52), 0x7ff), m.c(1023))
	r := m.add(x, m.shrBy(m.c(1<<51), e))
	r = m.and(r, m.xor(m.shrBy(m.c(fracMask), e), m.c(^uint64(0))))
	small := m.sel(m.ltc(ax, 0x3fe0000000000000), s, m.or(s, m.c(oneBits)))
	return m.sel(m.ltc(ax, oneBits), small, r)
}

func (m *mathEmitter) roundToEven(x Value) Value {
	ax, s := m.abs(x), m.andc(x, signBit)
	e := m.sub(m.andc(m.shr(x, 52), 0x7ff), m.c(1023))
	odd := m.andc(m.shrBy(x, m.sub(m.c(52), e)), 1)
	r := m.add(x, m.shrBy(m.add(m.c(1<<51-1), odd), e))
	r = m.and(r, m.xor(m.shrBy(m.c(fracMask), e), m.c(^uint64(0))))
	small := m.sel(m.gtc(ax, 0x3fe0000000000000), m.or(s, m.c(oneBits)), s)
	return m.sel(m.ltc(ax, oneBits), small, r)
}

// Exponentials.

const (
	ln2Hi = 6.93147180369123816490e-01
	ln2Lo = 1.90821492927058770002e-10
)

func (m *mathEmitter) expmulti(hi, lo, k Value) Value {
	r := m.fsub(hi, lo)
	t := m.fmul(r, r)
	c := m.fsub(r, m.fmul(t, m.horner(t, 1.66666666666666657415e-01, -2.77777777770155933842e-03,
		6.61375632143793436117e-05, -1.65339022054652515390e-06, 4.13813679705723846039e-08)))
	y := m.fsub(m.f(1), m.fsub(m.fsub(lo, m.fdiv(m.fmul(r, c), m.fsub(m.f(2), c))), hi))
	return m.ldexp(y, k)
}

// expLimits replaces NaN and out-of-range arguments with zero and applies
// their results after the expansion.
func (m *mathEmitter) expLimits(x Value, overflow, underflow float64, expand func(Value) Value) Value {
	ax, s := m.abs(x), m.sign(x)
	nan := m.isNaN(x)
	over := m.and(m.not(s), m.gtc(ax, math.Float64bits(overflow)))
	under := m.and(s, m.gtc(ax, math.Float64bits(-underflow)))
	r := expand(m.sel(m.or(nan, m.or(over, under)), m.f(0), x))
	r = m.sel(under, m.f(0), r)
	r = m.sel(over, m.c(infBits), r)
	return m.sel(nan, x, r)
}

func (m *mathEmitter) exp(x Value) Value {
	return m.expLimits(x, 7.09782712893383973096e+02, -7.45133219101941108420e+02, func(x Value) Value {
		kf, k := m.rint(m.fmul(x, m.f(1.44269504088896338700e+00)))
		return m.expmulti(m.fsub(x, m.fmul(kf, m.f(ln2Hi))), m.fmul(kf, m.f(ln2Lo)), k)
	})
}

func (m *mathEmitter) exp2(x Value) Value {
	return m.expLimits(x, 1.0239999999999999e+03, -1.0740e+03, func(x Value) Value {
		kf, k := m.rint(x)
		t := m.fsub(x, kf)
		return m.expmulti(m.fmul(t, m.f(ln2Hi)), m.fmul(m.fneg(t), m.f(ln2Lo)), k)
	})
}

func (m *mathEmitter) expm1(x Value) Value {
	ax, s := m.abs(x), m.sign(x)
	nan := m.isNaN(x)
	big := m.not(m.ltc(ax, math.Float64bits(3.88162421113569373274e+01)))
	toMinusOne := m.and(big, s)
	toInf := m.and(m.not(s), m.not(m.ltc(ax, math.Float64bits(7.09782712893383973096e+02))))
	x0 := m.sel(m.or(nan, m.or(toMinusOne, toInf)), m.f(0), x)

	kf, k := m.rint(m.fmul(x0, m.f(1.44269504088896338700e+00)))
	hi := m.fsub(x0, m.fmul(kf, m.f(ln2Hi)))
	lo := m.fmul(kf, m.f(ln2Lo))
	xr := m.fsub(hi, lo)
	c := m.fsub(m.fsub(hi, xr), lo)

	hfx := m.fmul(m.f(0.5), xr)
	hxs := m.fmul(xr, hfx)
	r1 := m.fadd(m.f(1), m.fmul(hxs, m.horner(hxs, -3.33333333333331316428e-02, 1.58730158725481460165e-03,
		-7.93650757867487942473e-05, 4.00821782732936239552e-06, -2.01099218183624371326e-07)))
	t := m.fsub(m.f(3), m.fmul(r1, hfx))
	e := m.fmul(hxs, m.fdiv(m.fsub(r1, t), m.fsub(m.f(6), m.fmul(xr, t))))
	k0 := m.fsub(xr, m.fsub(m.fmul(xr, e), hxs))
	e = m.fsub(m.fsub(m.fmul(xr, m.fsub(e, c)), c), hxs)

	scale := m.shl(k, 52)
	kMinus1 := m.fsub(m.fmul(m.f(0.5), m.fsub(xr, e)), m.f(0.5))
	below := m.and(m.sign(xr), m.gtc(m.abs(xr), math.Float64bits(0.25)))
	kPlus1 := m.sel(below, m.fmul(m.f(-2), m.fsub(e, m.fadd(xr, m.f(0.5)))),
		m.fadd(m.f(1), m.fmul(m.f(2), m.fsub(xr, e))))
	wide := m.fsub(m.add(m.fsub(m.f(1), m.fsub(e, xr)), scale), m.f(1))
	t20 := m.sub(m.c(oneBits), m.shrBy(m.c(0x20000000000000), k))
	under20 := m.add(m.fsub(t20, m.fsub(e, xr)), scale)
	t56 := m.shl(m.sub(m.c(0x3ff), k), 52)
	upTo56 := m.add(m.fadd(m.fsub(xr, m.fadd(e, t56)), m.f(1)), scale)

	r := m.sel(m.lt(k, m.c(20)), under20, upTo56)
	r = m.sel(m.or(m.lt(k, m.c(^uint64(0))), m.lt(m.c(56), k)), wide, r)
	r = m.sel(m.eqc(k, 1), kPlus1, r)
	r = m.sel(m.eqc(k, ^uint64(0)), kMinus1, r)
	r = m.sel(m.isZero(k), k0, r)
	r = m.sel(m.ltc(ax, 0x3c90000000000000), x, r)
	r = m.sel(toMinusOne, m.f(-1), r)
	r = m.sel(toInf, m.c(infBits), r)
	return m.sel(nan, x, r)
}

// Logarithms.

// frexp splits a positive finite x into a fraction in [0.5, 1) and an exponent.
func (m *mathEmitter) frexp(x Value) (Value, Value) {
	subnormal := m.ltc(m.abs(x), 0x0010000000000000)
	x = m.sel(subnormal, m.fmul(x, m.f(0x1p52)), x)
	e := m.sub(m.andc(m.shr(x, 52), 0x7ff), m.sel(subnormal, m.c(1022+52), m.c(1022)))
	return m.or(m.andc(x, fracMask), m.c(0x3fe0000000000000)), e
}

// logFinite computes log(x) for a positive finite x.
func (m *mathEmitter) logFinite(x Value) Value {
	f1, k := m.frexp(x)
	small := m.ltc(f1, math.Float64bits(math.Sqrt2/2))
	f1 = m.add(f1, m.shl(small, 52))
	kf := m.float(m.sub(k, small))
	f := m.fsub(f1, m.f(1))
	s := m.fdiv(f, m.fadd(m.f(2), f))
	s2 := m.fmul(s, s)
	s4 := m.fmul(s2, s2)
	t1 := m.fmul(s2, m.horner(s4, 6.666666666666735130e-01, 2.857142874366239149e-01, 1.818357216161805012e-01, 1.479819860511658591e-01))
	t2 := m.fmul(s4, m.horner(s4, 3.999999999940941908e-01, 2.222219843214978396e-01, 1.531383769920937332e-01))
	r := m.fadd(t1, t2)
	hfsq := m.fmul(m.fmul(m.f(0.5), f), f)
	inner := m.fsub(m.fsub(hfsq, m.fadd(m.fmul(s, m.fadd(hfsq, r)), m.fmul(kf, m.f(ln2Lo)))), f)
	return m.fsub(m.fmul(kf, m.f(ln2Hi)), inner)
}

// logSpecial applies log's special cases to a finite-path result.
func (m *mathEmitter) logSpecial(x, r Value) Value {
	ax, s := m.abs(x), m.sign(x)
	zero := m.isZero(ax)
	r = m.sel(zero, m.c(infBits|signBit), r)
	r = m.sel(m.and(s, m.not(zero)), m.c(nanBits), r)
	return m.sel(m.and(m.not(s), m.gtc(ax, infBits-1)), x, r)
}

func (m *mathEmitter) log(x Value) Value { return m.logSpecial(x, m.logFinite(x)) }

func (m *mathEmitter) log10(x Value) Value {
	return m.fmul(m.log(x), m.f(1/math.Ln10))
}

// log2 is exact for powers of two. Elsewhere it scales log(x), which avoids
// the cancellation near 1 of Go's Log(frac)/Ln2 + exp.
func (m *mathEmitter) log2(x Value) Value {
	frac, e := m.frexp(x)
	r := m.fmul(m.logFinite(x), m.f(1/math.Ln2))
	r = m.sel(m.eqc(frac, math.Float64bits(0.5)), m.float(m.sub(e, m.c(1))), r)
	return m.logSpecial(x, r)
}

// log1pOf returns log(u) - ((u-1)-y)/u for u = 1+y, or y when u is 1. The
// correction recovers the rounding error of 1+y.
func (m *mathEmitter) log1pOf(y, u, logU Value) Value {
	r := m.fsub(logU, m.fdiv(m.fsub(m.fsub(u, m.f(1)), y), u))
	return m.sel(m.eqc(u, oneBits), y, r)
}

func (m *mathEmitter) log1p(x Value) Value {
	ax, s := m.abs(x), m.sign(x)
	u := m.fadd(m.f(1), x)
	r := m.log1pOf(x, u, m.logFinite(u))
	r = m.sel(m.eqc(x, oneBits|signBit), m.c(infBits|signBit), r)
	r = m.sel(m.and(s, m.gtc(ax, oneBits)), m.c(nanBits), r)
	return m.sel(m.or(m.isNaN(x), m.eqc(x, infBits)), x, r)
}

// Powers and roots.

func (m *mathEmitter) cbrt(x Value) Value {
	const (
		b1 = 715094163
		b2 = 696219795
	)
	ax := m.abs(x)
	subnormal := m.ltc(ax, 0x0010000000000000)
	scaled := m.sel(subnormal, m.fmul(ax, m.f(0x1p54)), ax)
	third := m.shr(m.mulHigh(scaled, m.c(0xaaaaaaaaaaaaaaab)), 1)
	t := m.add(third, m.sel(subnormal, m.c(b2<<32), m.c(b1<<32)))
	r := m.fdiv(m.fmul(t, t), ax)
	s := m.fadd(m.f(5.42857142857142815906e-01), m.fmul(r, t))
	t = m.fmul(t, m.fadd(m.f(3.57142857142857150787e-01),
		m.fdiv(m.f(1.60714285714285720630e+00), m.fadd(m.fadd(s, m.f(1.41428571428571436819e+00)), m.fdiv(m.f(-7.05306122448979611050e-01), s)))))
	t = m.add(m.andc(t, 0xFFFFFFFFC<<28), m.c(1<<30))
	s = m.fmul(t, t)
	r = m.fdiv(ax, s)
	w := m.fadd(t, t)
	r = m.fdiv(m.fsub(r, t), m.fadd(w, r))
	t = m.or(m.fadd(t, m.fmul(t, r)), m.andc(x, signBit))
	return m.sel(m.or(m.isZero(ax), m.not(m.ltc(ax, infBits))), x, t)
}

func (m *mathEmitter) hypot(p, q Value) Value {
	p, q = m.abs(p), m.abs(q)
	inf := m.or(m.eqc(p, infBits), m.eqc(q, infBits))
	nan := m.or(m.isNaN(p), m.isNaN(q))
	swap := m.lt(p, q)
	p, q = m.sel(swap, q, p), m.sel(swap, p, q)
	q = m.fdiv(q, p)
	r := m.fmul(p, m.fsqrt(m.fadd(m.f(1), m.fmul(q, q))))
	r = m.sel(m.isZero(p), m.f(0), r)
	r = m.sel(nan, m.c(nanBits), r)
	return m.sel(inf, m.c(infBits), r)
}

// Hyperbolic functions.

func (m *mathEmitter) sinh(x Value) Value {
	ax := m.abs(x)
	e := m.exp(ax)
	big := m.fmul(e, m.f(0.5))
	mid := m.fmul(m.fsub(e, m.fdiv(m.f(1), e)), m.f(0.5))
	sq := m.fmul(ax, ax)
	small := m.fmul(m.leading(sq, -0.2630563213397497062819489e+2, -0.2894211355989563807284660366e+4,
		-0.8991272022039509355398013511e+5, -0.6307673640497716991184787251e+6), ax)
	small = m.fdiv(small, m.monic(sq, -0.173678953558233699533450911e+3, 0.1521517378790019070696485176e+5,
		-0.6307673640497716991212077277e+6))
	r := m.sel(m.gtc(ax, math.Float64bits(21)), big, m.sel(m.gtc(ax, math.Float64bits(0.5)), mid, small))
	return m.or(r, m.andc(x, signBit))
}

func (m *mathEmitter) cosh(x Value) Value {
	ax := m.abs(x)
	e := m.exp(ax)
	return m.sel(m.gtc(ax, math.Float64bits(21)), m.fmul(e, m.f(0.5)),
		m.fmul(m.fadd(e, m.fdiv(m.f(1), e)), m.f(0.5)))
}

func (m *mathEmitter) tanh(x Value) Value {
	ax := m.abs(x)
	s := m.exp(m.fmul(m.f(2), ax))
	big := m.or(m.fsub(m.f(1), m.fdiv(m.f(2), m.fadd(s, m.f(1)))), m.andc(x, signBit))
	sq := m.fmul(x, x)
	p := m.leading(sq, -9.64399179425052238628e-1, -9.92877231001918586564e1, -1.61468768441708447952e3)
	q := m.monic(sq, 1.12811678491632931402e2, 2.23548839060100448583e3, 4.84406305325125486048e3)
	small := m.fadd(x, m.fdiv(m.fmul(m.fmul(x, sq), p), q))
	r := m.sel(m.ltc(ax, math.Float64bits(0.625)), small, big)
	return m.sel(m.isZero(ax), x, r)
}

func (m *mathEmitter) asinh(x Value) Value {
	ax := m.abs(x)
	large := m.gtc(ax, math.Float64bits(1<<28))
	overTwo := m.gtc(ax, math.Float64bits(2))
	sq := m.fmul(ax, ax)
	outer := m.fadd(m.fmul(m.f(2), ax), m.fdiv(m.f(1), m.fadd(m.fsqrt(m.fadd(sq, m.f(1))), ax)))
	outer = m.sel(large, ax, outer)
	y := m.fadd(ax, m.fdiv(sq, m.fadd(m.f(1), m.fsqrt(m.fadd(m.f(1), sq)))))
	u := m.fadd(m.f(1), y)
	l := m.logFinite(m.sel(overTwo, outer, u))
	r := m.sel(overTwo, m.fadd(l, m.sel(large, m.f(math.Ln2), m.f(0))), m.log1pOf(y, u, l))
	r = m.sel(m.ltc(ax, math.Float64bits(1.0/(1<<28))), ax, r)
	r = m.or(r, m.andc(x, signBit))
	return m.sel(m.not(m.ltc(ax, infBits)), x, r)
}

func (m *mathEmitter) acosh(x Value) Value {
	large := m.not(m.ltc(x, math.Float64bits(1<<28)))
	overTwo := m.gtc(x, math.Float64bits(2))
	outer := m.fsub(m.fmul(m.f(2), x), m.fdiv(m.f(1), m.fadd(x, m.fsqrt(m.fsub(m.fmul(x, x), m.f(1))))))
	outer = m.sel(large, x, outer)
	t := m.fsub(x, m.f(1))
	y := m.fadd(t, m.fsqrt(m.fadd(m.fmul(m.f(2), t), m.fmul(t, t))))
	u := m.fadd(m.f(1), y)
	l := m.logFinite(m.sel(overTwo, outer, u))
	r := m.sel(overTwo, m.fadd(l, m.sel(large, m.f(math.Ln2), m.f(0))), m.log1pOf(y, u, l))
	r = m.sel(m.eqc(x, infBits), x, r)
	invalid := m.or(m.sign(x), m.or(m.ltc(x, oneBits), m.gtc(x, infBits)))
	return m.sel(invalid, m.c(nanBits), r)
}

func (m *mathEmitter) atanh(x Value) Value {
	ax := m.abs(x)
	twice := m.fadd(ax, ax)
	denominator := m.fsub(m.f(1), ax)
	y := m.sel(m.ltc(ax, math.Float64bits(0.5)),
		m.fadd(twice, m.fdiv(m.fmul(twice, ax), denominator)), m.fdiv(twice, denominator))
	r := m.fmul(m.f(0.5), m.log1p(y))
	r = m.sel(m.ltc(ax, math.Float64bits(1.0/(1<<28))), ax, r)
	r = m.or(r, m.andc(x, signBit))
	return m.sel(m.gtc(ax, oneBits), m.c(nanBits), r)
}

// Trigonometric functions.

// trigReduce returns j and z with |x| = (j + z/(Pi/4)) * Pi/4 modulo 2*Pi
// for a non-negative x. j is in [0, 8) and z in [-Pi/4, Pi/4].
func (m *mathEmitter) trigReduce(x Value) (Value, Value) {
	const threshold = uint64(0x41c0000000000000) // 1 << 29
	large := m.not(m.ltc(x, threshold))

	// Cody-Waite reduction with Pi/4 in three parts.
	q := m.fmul(m.sel(large, m.f(0), x), m.f(4/math.Pi))
	t := m.fadd(q, m.f(0x1p52))
	kf := m.fsub(t, m.f(0x1p52))
	down := m.lt(q, kf)
	kf = m.fsub(kf, m.sel(down, m.f(1), m.f(0)))
	k := m.sub(m.sub(t, m.c(math.Float64bits(0x1p52))), down)
	odd := m.andc(k, 1)
	smallJ := m.andc(m.add(k, odd), 7)
	y := m.fadd(kf, m.sel(odd, m.f(1), m.f(0)))
	smallZ := m.fsub(m.fsub(m.fsub(x, m.fmul(y, m.f(7.85398125648498535156e-1))),
		m.fmul(y, m.f(3.77489470793079817668e-8))), m.fmul(y, m.f(2.69515142907905952645e-15)))

	// Payne-Hanek reduction with the digits of 4/Pi.
	ix := m.sel(large, x, m.c(threshold))
	e61 := m.sub(m.shr(ix, 52), m.c(1023+52-61))
	ix = m.or(m.andc(ix, fracMask), m.c(1<<52))
	address := m.add(m.pi4Table(), m.shl(m.shr(e61, 6), 3))
	shift := m.andc(e61, 63)
	back := m.sub(m.c(64), shift)
	var digits [4]Value
	for i := range digits {
		digits[i] = m.load(m.add(address, m.c(uint64(8*i))))
	}
	var z [3]Value
	for i := range z {
		z[i] = m.or(m.shlBy(digits[i], shift), m.shrBy(digits[i+1], back))
	}
	z2hi := m.mulHigh(z[2], ix)
	z1hi, z1lo := m.mulHigh(z[1], ix), m.mul(z[1], ix)
	lo := m.add(z1lo, z2hi)
	carry := m.shr(m.or(m.and(z1lo, z2hi), m.and(m.or(z1lo, z2hi), m.xor(lo, m.c(^uint64(0))))), 63)
	hi := m.add(m.add(m.mul(z[0], ix), z1hi), carry)
	bigJ := m.shr(hi, 61)
	hi = m.or(m.shl(hi, 3), m.shr(lo, 61))
	lo = m.shl(lo, 3)
	lz := m.leadingZeros(hi)
	lz1 := m.add(lz, m.c(1))
	hi = m.or(m.shlBy(hi, lz1), m.shrBy(lo, m.sub(m.c(64), lz1)))
	bigZ := m.or(m.shr(hi, 12), m.shl(m.sub(m.c(1023), lz1), 52))
	odd = m.andc(bigJ, 1)
	bigJ = m.andc(m.add(bigJ, odd), 7)
	bigZ = m.fmul(m.fsub(bigZ, m.sel(odd, m.f(1), m.f(0))), m.f(math.Pi/4))

	return m.sel(large, bigJ, smallJ), m.sel(large, bigZ, smallZ)
}

// leadingZeros counts the leading zeros of a nonzero x from the exponent of
// an exactly converted 52-bit prefix.
func (m *mathEmitter) leadingZeros(x Value) Value {
	magic := m.f(0x1p52)
	high := m.fsub(m.or(m.shr(x, 12), magic), magic)
	low := m.fsub(m.or(m.andc(x, 0xfff), magic), magic)
	return m.sel(m.isZero(m.shr(x, 12)), m.sub(m.c(1086), m.shr(low, 52)), m.sub(m.c(1074), m.shr(high, 52)))
}

func (m *mathEmitter) sinPoly(z, zz Value) Value {
	return m.fadd(z, m.fmul(m.fmul(z, zz), m.leading(zz, 1.58962301576546568060e-10, -2.50507477628578072866e-8,
		2.75573136213857245213e-6, -1.98412698295895385996e-4, 8.33333333332211858878e-3, -1.66666666666666307295e-1)))
}

func (m *mathEmitter) cosPoly(zz Value) Value {
	return m.fadd(m.fsub(m.f(1), m.fmul(m.f(0.5), zz)), m.fmul(m.fmul(zz, zz), m.leading(zz, -1.13585365213876817300e-11,
		2.08757008419747316778e-9, -2.75573141792967388112e-7, 2.48015872888517045348e-5, -1.38888888888730564116e-3,
		4.16666666666665929218e-2)))
}

func (m *mathEmitter) sin(x Value) Value {
	ax := m.abs(x)
	j, z := m.trigReduce(ax)
	negative := m.xor(m.sign(x), m.shr(j, 2))
	j = m.andc(j, 3)
	zz := m.fmul(z, z)
	useCos := m.xor(m.andc(j, 1), m.shr(j, 1))
	r := m.xor(m.sel(useCos, m.cosPoly(zz), m.sinPoly(z, zz)), m.shl(negative, 63))
	r = m.sel(m.or(m.isZero(ax), m.isNaN(x)), x, r)
	return m.sel(m.eqc(ax, infBits), m.c(nanBits), r)
}

func (m *mathEmitter) cos(x Value) Value {
	ax := m.abs(x)
	j, z := m.trigReduce(ax)
	negative := m.shr(j, 2)
	j = m.andc(j, 3)
	negative = m.xor(negative, m.shr(j, 1))
	zz := m.fmul(z, z)
	useSin := m.xor(m.andc(j, 1), m.shr(j, 1))
	r := m.xor(m.sel(useSin, m.sinPoly(z, zz), m.cosPoly(zz)), m.shl(negative, 63))
	return m.sel(m.not(m.ltc(ax, infBits)), m.c(nanBits), r)
}

func (m *mathEmitter) tan(x Value) Value {
	ax := m.abs(x)
	j, z := m.trigReduce(ax)
	zz := m.fmul(z, z)
	p := m.leading(zz, -1.30936939181383777646e4, 1.15351664838587416140e6, -1.79565251976484877988e7)
	q := m.monic(zz, 1.36812963470692954678e4, -1.32089234440210967447e6, 2.50083801823357915839e7, -5.38695755929454629881e7)
	y := m.sel(m.gtc(zz, math.Float64bits(1e-14)), m.fadd(z, m.fmul(z, m.fdiv(m.fmul(zz, p), q))), z)
	y = m.sel(m.andc(j, 2), m.fdiv(m.f(-1), y), y)
	r := m.xor(y, m.andc(x, signBit))
	r = m.sel(m.or(m.isZero(ax), m.isNaN(x)), x, r)
	return m.sel(m.eqc(ax, infBits), m.c(nanBits), r)
}

// xatan evaluates a series valid in the range [0, 0.66].
func (m *mathEmitter) xatan(x Value) Value {
	z := m.fmul(x, x)
	p := m.leading(z, -8.750608600031904122785e-01, -1.615753718733365076637e+01, -7.500855792314704667340e+01,
		-1.228866684490136173410e+02, -6.485021904942025371773e+01)
	q := m.monic(z, 2.485846490142306297962e+01, 1.650270098316988542046e+02, 4.328810604912902668951e+02,
		4.853903996359136964868e+02, 1.945506571482613964425e+02)
	z = m.fdiv(m.fmul(z, p), q)
	return m.fadd(m.fmul(x, z), x)
}

// satan reduces a non-negative argument to [0, 0.66] and evaluates xatan.
// Pi/2 - xatan(1/x) is computed as Pi/2 + xatan(-1/x), which rounds the same.
func (m *mathEmitter) satan(x Value) Value {
	const morebits = 6.123233995736765886130e-17
	big := m.gtc(x, math.Float64bits(2.41421356237309504880))
	mid := m.and(m.not(big), m.gtc(x, math.Float64bits(0.66)))
	numerator := m.sel(big, m.f(-1), m.sel(mid, m.fsub(x, m.f(1)), x))
	denominator := m.sel(big, x, m.sel(mid, m.fadd(x, m.f(1)), m.f(1)))
	base := m.sel(big, m.f(math.Pi/2), m.sel(mid, m.f(math.Pi/4), m.f(0)))
	extra := m.sel(big, m.f(morebits), m.sel(mid, m.f(0.5*morebits), m.f(0)))
	return m.fadd(m.fadd(base, m.xatan(m.fdiv(numerator, denominator))), extra)
}

func (m *mathEmitter) atan(x Value) Value {
	r := m.xor(m.satan(m.abs(x)), m.andc(x, signBit))
	return m.sel(m.isZero(m.abs(x)), x, r)
}

func (m *mathEmitter) asin(x Value) Value {
	ax := m.abs(x)
	// (1-x)(1+x) avoids the cancellation of 1-x*x near 1.
	root := m.fsqrt(m.fmul(m.fsub(m.f(1), ax), m.fadd(m.f(1), ax)))
	outer := m.gtc(ax, math.Float64bits(0.7))
	s := m.satan(m.fdiv(m.sel(outer, root, ax), m.sel(outer, ax, root)))
	r := m.xor(m.sel(outer, m.fsub(m.f(math.Pi/2), s), s), m.andc(x, signBit))
	r = m.sel(m.isZero(ax), x, r)
	return m.sel(m.gtc(ax, oneBits), m.c(nanBits), r)
}

// acos uses Pi/2 - asin(x) for |x| <= 0.5. Beyond that it uses
// 2*asin(sqrt((1-|x|)/2)), which avoids the cancellation of Pi/2 - asin(x)
// near 1 that Go's math.Acos has.
func (m *mathEmitter) acos(x Value) Value {
	ax := m.abs(x)
	outer := m.gtc(ax, math.Float64bits(0.5))
	h := m.fsqrt(m.fmul(m.fsub(m.f(1), ax), m.f(0.5)))
	s := m.asin(m.sel(outer, h, x))
	twice := m.fadd(s, s)
	return m.sel(outer, m.sel(m.sign(x), m.fsub(m.f(math.Pi), twice), twice), m.fsub(m.f(math.Pi/2), s))
}

func (m *mathEmitter) atan2(y, x Value) Value {
	ax, ay := m.abs(x), m.abs(y)
	sx := m.sign(x)
	q := m.atan(m.fdiv(y, x))
	nonPositive := m.or(m.sign(q), m.isZero(m.abs(q)))
	r := m.sel(sx, m.sel(nonPositive, m.fadd(q, m.f(math.Pi)), m.fsub(q, m.f(math.Pi))), q)
	yInf, xInf := m.eqc(ay, infBits), m.eqc(ax, infBits)
	r = m.sel(yInf, m.copysign(m.f(math.Pi/2), y), r)
	corner := m.sel(sx, m.sel(yInf, m.f(3*math.Pi/4), m.f(math.Pi)), m.sel(yInf, m.f(math.Pi/4), m.f(0)))
	r = m.sel(xInf, m.copysign(corner, y), r)
	r = m.sel(m.isZero(ax), m.copysign(m.f(math.Pi/2), y), r)
	r = m.sel(m.isZero(ay), m.copysign(m.sel(sx, m.f(math.Pi), m.f(0)), y), r)
	return m.sel(m.or(m.isNaN(x), m.isNaN(y)), m.c(nanBits), r)
}

// Power.

// Double-double arithmetic (Knuth sums, target-selected exact products).

func (m *mathEmitter) twoSum(a, b Value) (Value, Value) {
	s := m.fadd(a, b)
	bb := m.fsub(s, a)
	return s, m.fadd(m.fsub(a, m.fsub(s, bb)), m.fsub(b, bb))
}

// fastTwoSum requires |a| >= |b|.
func (m *mathEmitter) fastTwoSum(a, b Value) (Value, Value) {
	s := m.fadd(a, b)
	return s, m.fsub(b, m.fsub(s, a))
}

func (m *mathEmitter) twoProd(a, b Value) (Value, Value) {
	p := m.fmul(a, b)
	e := m.b.append(instruction{Op: ir.OpFProductError, Bits: 64, A: a.id, B: b.id, C: p.id})
	return p, e
}

// logDD returns log(x) for a positive finite x as an unevaluated sum with
// about 100 bits of precision. With m = 1+f in [Sqrt2/2, Sqrt2) and
// s = f/(2+f), log(m) = 2s + 2s^3/3 + 2s^5/5 + ...; the first two terms are
// carried in double-double and the rest is under 2^-12 of the sum.
func (m *mathEmitter) logDD(x Value) (Value, Value) {
	f1, k := m.frexp(x)
	small := m.ltc(f1, math.Float64bits(math.Sqrt2/2))
	f1 = m.add(f1, m.shl(small, 52))
	kf := m.float(m.sub(k, small))
	f := m.fsub(f1, m.f(1))
	dh, dl := m.twoSum(f1, m.f(1))
	sh := m.fdiv(f, dh)
	p, pe := m.twoProd(sh, dh)
	sl := m.fdiv(m.fsub(m.fsub(m.fsub(f, p), pe), m.fmul(sh, dl)), dh)

	s2h, s2e := m.twoProd(sh, sh)
	s2l := m.fadd(s2e, m.fmul(m.fmul(m.f(2), sh), sl))
	s3h, s3e := m.twoProd(s2h, sh)
	s3l := m.fadd(s3e, m.fadd(m.fmul(s2h, sl), m.fmul(s2l, sh)))
	const twoThirds = 2.0 / 3
	twoThirdsLo := 1.0 / 3 * 0x1p-53 // 2/3 - twoThirds
	t1h, t1e := m.twoProd(s3h, m.f(twoThirds))
	t1l := m.fadd(t1e, m.fadd(m.fmul(s3h, m.f(twoThirdsLo)), m.fmul(s3l, m.f(twoThirds))))
	coefficients := make([]float64, 0, 12)
	for n := 5.0; n <= 27; n += 2 {
		coefficients = append(coefficients, 2/n)
	}
	tail := m.fmul(m.fmul(s3h, s2h), m.horner(s2h, coefficients...))

	ah, ae := m.twoSum(m.fadd(sh, sh), t1h)
	lmh, lml := m.fastTwoSum(ah, m.fadd(ae, m.fadd(m.fadd(m.fadd(sl, sl), t1l), tail)))
	bh, be := m.twoSum(m.fmul(kf, m.f(ln2Hi)), lmh)
	return m.fastTwoSum(bh, m.fadd(be, m.fadd(lml, m.fmul(kf, m.f(ln2Lo)))))
}

// integerParts reports whether y is an integer and whether it is odd.
func (m *mathEmitter) integerParts(y Value) (Value, Value) {
	e := m.sub(m.andc(m.shr(y, 52), 0x7ff), m.c(1023))
	fraction := m.and(y, m.shrBy(m.c(fracMask), e))
	integral := m.sel(m.lt(e, m.c(0)), m.isZero(m.abs(y)), m.isZero(fraction))
	bit := m.andc(m.shrBy(m.or(m.andc(y, fracMask), m.c(1<<52)), m.sub(m.c(52), e)), 1)
	return integral, m.and(integral, bit)
}

// pow computes exp(y*log|x|) with log|x| and the product in double-double,
// so the exponential argument keeps enough bits for results near the
// overflow and underflow limits.
func (m *mathEmitter) pow(x, y Value) Value {
	const (
		overflow  = 7.09782712893383973096e+02
		underflow = -7.45133219101941108420e+02
	)
	ax, ay := m.abs(x), m.abs(y)
	sx, sy := m.sign(x), m.sign(y)
	integral, odd := m.integerParts(y)
	negative := m.and(sx, odd)

	huge := m.not(m.ltc(ay, 0x43e0000000000000)) // |y| >= 2^63, Inf, or NaN
	yg := m.sel(huge, m.f(0), y)
	lh, ll := m.logDD(ax)
	zh, ze := m.twoProd(yg, lh)
	zh, zl := m.fastTwoSum(zh, m.fadd(ze, m.fmul(yg, ll)))
	az := m.abs(zh)
	over := m.and(m.not(m.sign(zh)), m.gtc(az, math.Float64bits(overflow)))
	under := m.and(m.sign(zh), m.gtc(az, math.Float64bits(-underflow)))
	limit := m.or(m.or(over, under), m.isNaN(zh))
	zh, zl = m.sel(limit, m.f(0), zh), m.sel(limit, m.f(0), zl)
	kf, k := m.rint(m.fmul(zh, m.f(1.44269504088896338700e+00)))
	r := m.expmulti(m.fsub(zh, m.fmul(kf, m.f(ln2Hi))), m.fsub(m.fmul(kf, m.f(ln2Lo)), zl), k)
	r = m.sel(under, m.f(0), r)
	r = m.sel(over, m.c(infBits), r)
	r = m.withSign(r, negative)

	// (|x| < 1) == (y > 0) selects 0 when |y| is huge or infinite.
	vanishing := m.eqc(m.xor(m.ltc(ax, oneBits), sy), 1)
	limitValue := m.sel(m.eqc(x, oneBits|signBit), m.f(1), m.sel(vanishing, m.f(0), m.c(infBits)))
	r = m.sel(huge, limitValue, r)
	r = m.sel(m.and(sx, m.not(integral)), m.c(nanBits), r)
	root := m.fsqrt(x)
	r = m.sel(m.eqc(y, math.Float64bits(0.5)), root, r)
	r = m.sel(m.eqc(y, math.Float64bits(-0.5)), m.fdiv(m.f(1), root), r)
	r = m.sel(m.isInf(x), m.withSign(m.sel(sy, m.f(0), m.c(infBits)), negative), r)
	r = m.sel(m.isInf(y), limitValue, r)
	r = m.sel(m.isZero(ax), m.withSign(m.sel(sy, m.c(infBits), m.f(0)), negative), r)
	r = m.sel(m.or(m.isNaN(x), m.isNaN(y)), m.c(nanBits), r)
	r = m.sel(m.eqc(y, oneBits), x, r)
	return m.sel(m.or(m.isZero(ay), m.eqc(x, oneBits)), m.f(1), r)
}
