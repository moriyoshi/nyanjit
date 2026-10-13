//go:build linux && loong64

package loong64_test

import (
	"math"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
)

// divideReference computes OpDivide on w-bit zero-extended a and b.
func divideReference(arithmetic op.Opcode, s op.DivisionSemantics, w uint8, a, b uint64) uint64 {
	mask := ^uint64(0) >> (64 - w)
	signed := arithmetic == op.OpDivSigned || arithmetic == op.OpRemSigned
	rem := arithmetic == op.OpRem || arithmetic == op.OpRemSigned
	if b == 0 {
		if rem {
			return a
		}
		if s == op.DivisionAllOnes {
			return mask
		}
		return 0
	}
	if !signed {
		if rem {
			return a % b
		}
		return a / b
	}
	x, y := int64(a<<(64-w))>>(64-w), int64(b<<(64-w))>>(64-w)
	if y == -1 {
		if rem {
			return 0
		}
		return uint64(-x) & mask
	}
	if rem {
		return uint64(x%y) & mask
	}
	return uint64(x/y) & mask
}

// Every aliasing of A, B and C, with zero divisors and MIN / -1.
func TestNativeDivide(t *testing.T) {
	for _, arithmetic := range []op.Opcode{op.OpDiv, op.OpDivSigned, op.OpRem, op.OpRemSigned} {
		for _, w := range []uint8{8, 16, 32, 64} {
			mask := ^uint64(0) >> (64 - w)
			min := uint64(1) << (w - 1)
			values := []uint64{0, 1, 2, 3, 7, min, min - 1, min + 1, mask, mask - 1, 0x5a5a5a5a5a5a5a5a & mask}
			for s := op.DivisionZero; s.Valid(); s++ {
				for _, regs := range [][3]backend.Register{{4, 5, 6}, {5, 5, 6}, {6, 5, 6}, {4, 5, 5}, {5, 5, 5}} {
					a := loong64.NewByteAssembler(abi.StackNoUse, 0)
					if err := machine.Visit(machine.Instruction{Op: machine.OpDivide, Arithmetic: arithmetic, Bits: w, A: regs[0], B: regs[1], C: regs[2], Imm: uint64(s)}, a); err != nil {
						t.Fatal(err)
					}
					a.Move(4, regs[0])
					a.Ret()
					for _, x := range values {
						for _, y := range values {
							if regs[1] == regs[2] {
								y = x
							}
							signed := arithmetic == op.OpDivSigned || arithmetic == op.OpRemSigned
							if s == op.DivisionUnchecked && (y == 0 || signed && x == min && y == mask) {
								continue
							}
							want := divideReference(arithmetic, s, w, x, y)
							if got := uint64(callPlan(t, a, 0, uintptr(x), uintptr(y))); got != want {
								t.Fatalf("%v w%d s%d regs%v %#x, %#x got%#x want%#x:\n%s", arithmetic, w, s, regs, x, y, got, want, a.Text())
							}
						}
					}
				}
			}
		}
	}
}

// Conversions write no FP register but A (int to float) or the f0
// temporary (float to int): f1, f2 and B keep their bits, as does f0 for an
// int-to-float conversion, and an integer result is zero-extended.
func TestNativeConvert(t *testing.T) {
	f := backend.VectorRegister
	sentinel := uint64(0x7ff4a5a5a5a5a5a5)
	floats := []float64{0, -0.0, 0.5, -0.5, 0.999, 1, -1, 1.5, -1.5, 2147483647.5, -2147483648.5, -2147483649, 2147483648, 4294967295.5, 4294967296,
		9223372036854774784, 9223372036854775808, -9223372036854775808, -9223372036854777856, 18446744073709549568, 18446744073709551616,
		1e30, -1e30, math.Inf(1), math.Inf(-1), math.NaN(), -math.NaN(), 5e-324, 1e-40}
	for _, fw := range []uint8{32, 64} {
		for _, iw := range []uint8{32, 64} {
			for _, arithmetic := range []op.Opcode{op.OpFToSigned, op.OpFToUnsigned} {
				for s := op.ConversionSaturate; s.Valid(); s++ {
					a := loong64.NewByteAssembler(abi.StackNoUse, 0)
					for _, r := range []uint8{0, 1, 2} {
						a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: f(r), B: 5})
					}
					a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: f(9), B: 4})
					if err := machine.Visit(machine.Instruction{Op: machine.OpFloatToInt, Arithmetic: arithmetic, Bits: iw, A: 6, B: f(9), Disp: uint64(fw), Imm: uint64(s)}, a); err != nil {
						t.Fatal(err)
					}
					// r8 is nonzero when B, f1 or f2 changed.
					a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: 7, B: f(9)})
					a.Binary(op.OpXor, 64, 8, 4, 7)
					for _, r := range []uint8{1, 2} {
						a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: 7, B: f(r)})
						a.Binary(op.OpXor, 64, 7, 7, 5)
						a.Binary(op.OpOr, 64, 8, 8, 7)
					}
					// A clobber returns 0x5a5a... instead of the result.
					a.Constant(7, 0x5a5a5a5a5a5a5a5a)
					a.SelectValue(4, 8, 7, 6)
					a.Ret()
					for _, v := range floats {
						bits := math.Float64bits(v)
						if fw == 32 {
							bits = uint64(math.Float32bits(float32(v)))
						}
						got := uint64(callPlan(t, a, uintptr(bits), uintptr(sentinel)))
						want, defined := saturate(arithmetic == op.OpFToSigned, fw, iw, bits)
						if s == op.ConversionNative && !defined {
							// Any value, but zero-extended and with no clobber.
							if got == 0x5a5a5a5a5a5a5a5a || iw == 32 && got>>32 != 0 {
								t.Fatalf("f%d->i%d %v native %v got%#x:\n%s", fw, iw, arithmetic, v, got, a.Text())
							}
							continue
						}
						if got != want {
							t.Fatalf("f%d->i%d %v s%d %v got%#x want%#x:\n%s", fw, iw, arithmetic, s, v, got, want, a.Text())
						}
					}
				}
			}
		}
	}
	for _, fw := range []uint8{32, 64} {
		for _, iw := range []uint8{32, 64} {
			for _, arithmetic := range []op.Opcode{op.OpFFromSigned, op.OpFFromUnsigned} {
				a := loong64.NewByteAssembler(abi.StackNoUse, 0)
				for _, r := range []uint8{0, 1, 2} {
					a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: f(r), B: 5})
				}
				if err := machine.Visit(machine.Instruction{Op: machine.OpIntToFloat, Arithmetic: arithmetic, Bits: fw, A: f(9), B: 4, Imm: uint64(iw)}, a); err != nil {
					t.Fatal(err)
				}
				a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: fw, A: 4, B: f(9)})
				for _, r := range []uint8{0, 1, 2} {
					a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: 7, B: f(r)})
					a.Binary(op.OpXor, 64, 7, 7, 5)
					a.Binary(op.OpOr, 64, 4, 4, 7)
				}
				a.Ret()
				for _, x := range []uint64{0, 1, 1<<24 + 1, 1<<53 + 1, 1<<63 + 1<<11 + 1, 0xfffffffffffff801, 0x7ffffffffffffc01, 1<<31 + 1, 0xffffff01, 1 << 63, ^uint64(0)} {
					x &= ^uint64(0) >> (64 - iw)
					var want uint64
					switch {
					case arithmetic == op.OpFFromSigned && iw == 32:
						want = floatBits(fw, float64(int32(x)), float32(int32(x)))
					case arithmetic == op.OpFFromSigned:
						want = floatBits(fw, float64(int64(x)), float32(int64(x)))
					default:
						want = floatBits(fw, float64(x), float32(x))
					}
					if got := uint64(callPlan(t, a, uintptr(x), uintptr(sentinel))); got != want {
						t.Fatalf("i%d->f%d %v %#x got%#x want%#x:\n%s", iw, fw, arithmetic, x, got, want, a.Text())
					}
				}
			}
		}
	}
}

func floatBits(w uint8, d float64, s float32) uint64 {
	if w == 32 {
		return uint64(math.Float32bits(s))
	}
	return math.Float64bits(d)
}

// saturate is ConversionSaturate's truncation of the fw-bit float bits, and
// whether ConversionNative defines it too (the value is in range).
func saturate(signed bool, fw, iw uint8, bits uint64) (uint64, bool) {
	v := math.Float64frombits(bits)
	if fw == 32 {
		v = float64(math.Float32frombits(uint32(bits)))
	}
	mask := ^uint64(0) >> (64 - iw)
	t := math.Trunc(v)
	switch {
	case math.IsNaN(v):
		return 0, false
	case signed && t < -math.Ldexp(1, int(iw)-1):
		return 1 << (iw - 1), false
	case signed && t >= math.Ldexp(1, int(iw)-1):
		return mask >> 1, false
	case signed:
		return uint64(int64(t)) & mask, true
	case t < 0:
		return 0, false
	case t >= math.Ldexp(1, int(iw)):
		return mask, false
	}
	return uint64(t), true
}
