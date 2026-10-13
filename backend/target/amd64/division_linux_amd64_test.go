//go:build linux && amd64

package amd64

import (
	"math"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// runExpansion runs ins in a C entry whose arguments x (RDI) and y (RSI)
// are first placed in the registers named by load, and returns A, or the
// bits of an XMM A.
func runExpansion(t *testing.T, ins machine.Instruction, load func(e *ByteAssembler)) func(...uintptr) uint64 {
	t.Helper()
	e := NewByteAssembler(abi.StackNoUse, 0)
	load(e)
	if err := machine.Visit(ins, e); err != nil {
		t.Fatalf("%+v: %v", ins, err)
	}
	if backend.IsVectorRegister(ins.A) {
		e.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: 0, B: ins.A})
	} else {
		e.Move(0, ins.A)
	}
	e.Ret()
	if e.Err() != nil {
		t.Fatalf("%+v: %v", ins, e.Err())
	}
	code, err := execution.NewCode(e.Bytes(), execution.CodeOptions{ABI: abi.C, Arity: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { code.Close() })
	return func(args ...uintptr) uint64 {
		r, err := code.Call(args...)
		if err != nil {
			t.Fatal(err)
		}
		return uint64(r)
	}
}

func referenceDivide(arithmetic op.Opcode, s op.DivisionSemantics, w uint8, x, y uint64) uint64 {
	mask := ^uint64(0) >> (64 - w)
	shift := 64 - w
	sx, sy := int64(x<<shift)>>shift, int64(y<<shift)>>shift
	remainder := arithmetic == op.OpRem || arithmetic == op.OpRemSigned
	signed := arithmetic == op.OpDivSigned || arithmetic == op.OpRemSigned
	switch {
	case y == 0 && remainder:
		return x
	case y == 0 && s == op.DivisionZero:
		return 0
	case y == 0:
		return mask
	case signed && sy == -1:
		if remainder {
			return 0
		}
		return uint64(-sx) & mask
	case signed && remainder:
		return uint64(sx%sy) & mask
	case signed:
		return uint64(sx/sy) & mask
	case remainder:
		return x % y
	}
	return x / y
}

// TestDivideAliasingExecution covers what conformance cannot choose: A, B
// and C aliasing each other, B or A in RAX, RDX or R11, and extended
// registers.
func TestDivideAliasingExecution(t *testing.T) {
	registers := [][3]backend.Register{{1, 6, 7}, {6, 6, 7}, {7, 6, 7}, {8, 9, 9}, {9, 9, 9}, {0, 2, 8}, {2, 11, 10}, {10, 8, 11}, {11, 1, 6}}
	for _, w := range []uint8{8, 16, 32, 64} {
		mask := ^uint64(0) >> (64 - w)
		min := uint64(1) << (w - 1)
		values := []uint64{0, 1, 2, 3, 7, 100, min, min + 1, min - 1, mask, mask - 1, 0x5555555555555555 & mask, 0xdeadbeefcafef00d & mask}
		for _, arithmetic := range []op.Opcode{op.OpDiv, op.OpDivSigned, op.OpRem, op.OpRemSigned} {
			for semantics := op.DivisionZero; semantics <= op.DivisionUnchecked; semantics++ {
				for _, r := range registers {
					ins := machine.Instruction{Op: machine.OpDivide, Arithmetic: arithmetic, Bits: w, A: r[0], B: r[1], C: r[2], Imm: uint64(semantics)}
					same := r[1] == r[2]
					run := runExpansion(t, ins, func(e *ByteAssembler) {
						// Push both arguments and pop them into C and B, so the
						// load order never matters.
						e.code = append(e.code, 0x57, 0x56)
						for _, reg := range []backend.Register{r[2], r[1]} {
							if reg >= 8 {
								e.code = append(e.code, 0x41)
							}
							e.code = append(e.code, 0x58|byte(reg&7))
						}
					})
					for _, x := range values {
						for _, y := range values {
							if same {
								y = x
							}
							if semantics == op.DivisionUnchecked && (y == 0 || arithmetic != op.OpDiv && arithmetic != op.OpRem && x == min && y == mask) {
								continue
							}
							got, want := run(uintptr(x), uintptr(y)), referenceDivide(arithmetic, semantics, w, x, y)
							if got != want {
								t.Fatalf("%+v: %#x, %#x = %#x, want %#x", ins, x, y, got, want)
							}
						}
					}
				}
			}
		}
	}
}

func referenceToInteger(signed, saturate bool, w uint8, f float64) (uint64, bool) {
	mask := ^uint64(0) >> (64 - w)
	if math.IsNaN(f) {
		return 0, saturate
	}
	f = math.Trunc(f)
	if signed {
		limit := math.Ldexp(1, int(w)-1)
		switch {
		case f < -limit:
			return mask ^ mask>>1, saturate
		case f >= limit:
			return mask >> 1, saturate
		}
		return uint64(int64(f)) & mask, true
	}
	switch {
	case f < 0:
		return 0, saturate
	case f >= math.Ldexp(1, int(w)):
		return mask, saturate
	}
	return uint64(f), true
}

// TestFloatConvertExecution runs the conversions with extended registers on
// both sides.
func TestFloatConvertExecution(t *testing.T) {
	v := backend.VectorRegister
	integers := []uint64{0, 1, 3, 1<<24 + 1, 1<<31 - 1, 1 << 31, 1<<31 + 1, 1<<32 - 1, 1<<53 + 1, 1<<63 - 1, 1 << 63, 1<<63 + 1<<11 + 1, 1<<63 + 1<<10, 1<<63 + 1<<11 - 1, 0xfffffffffffff801, 0xfffffffffffffc00, 1<<64 - 1}
	for _, out := range []uint8{32, 64} {
		for _, in := range []uint8{32, 64} {
			for _, arithmetic := range []op.Opcode{op.OpFFromSigned, op.OpFFromUnsigned} {
				for _, r := range [][2]backend.Register{{v(0), 7}, {v(9), 2}, {v(14), 10}} {
					ins := machine.Instruction{Op: machine.OpIntToFloat, Arithmetic: arithmetic, Bits: out, A: r[0], B: r[1], Imm: uint64(in)}
					run := runExpansion(t, ins, func(e *ByteAssembler) { e.Move(r[1], 7) })
					for _, x := range integers {
						x &= ^uint64(0) >> (64 - in)
						var f float64
						var f32 float32
						switch {
						case arithmetic == op.OpFFromSigned && in == 32:
							f, f32 = float64(int32(x)), float32(int32(x))
						case arithmetic == op.OpFFromSigned:
							f, f32 = float64(int64(x)), float32(int64(x))
						default:
							f, f32 = float64(x), float32(x)
						}
						want := math.Float64bits(f)
						got := run(uintptr(x), 0)
						if out == 32 {
							want, got = uint64(math.Float32bits(f32)), got&0xffffffff
						}
						if got != want {
							t.Fatalf("%+v: %#x = %#x, want %#x", ins, x, got, want)
						}
					}
				}
			}
		}
	}
	floats := []float64{0, math.Copysign(0, -1), 0.5, -0.5, 1.5, -1.5, 2147483647.5, -2147483648.5, -2147483649, 2147483648, 4294967295.5, 4294967296,
		9223372036854774784, 9223372036854775808, 9223372036854777856, -9223372036854775808, -9223372036854777856, 18446744073709549568, 18446744073709551616,
		1e30, -1e30, math.Inf(1), math.Inf(-1), math.NaN(), -math.NaN()}
	for _, out := range []uint8{32, 64} {
		for _, in := range []uint8{32, 64} {
			for _, arithmetic := range []op.Opcode{op.OpFToSigned, op.OpFToUnsigned} {
				for semantics := op.ConversionSaturate; semantics <= op.ConversionNative; semantics++ {
					for _, r := range [][2]backend.Register{{0, v(1)}, {9, v(8)}, {11, v(14)}} {
						ins := machine.Instruction{Op: machine.OpFloatToInt, Arithmetic: arithmetic, Bits: out, A: r[0], B: r[1], Disp: uint64(in), Imm: uint64(semantics)}
						run := runExpansion(t, ins, func(e *ByteAssembler) {
							e.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: r[1], B: 7})
						})
						for _, f := range floats {
							x := math.Float64bits(f)
							if in == 32 {
								x = uint64(math.Float32bits(float32(f)))
								f = float64(float32(f))
							}
							want, defined := referenceToInteger(arithmetic == op.OpFToSigned, semantics == op.ConversionSaturate, out, f)
							got := run(uintptr(x), 0)
							if out == 32 && got>>32 != 0 {
								t.Fatalf("%+v: %v = %#x is not zero-extended", ins, f, got)
							}
							if defined && got != want {
								t.Fatalf("%+v: %v = %#x, want %#x", ins, f, got, want)
							}
						}
					}
				}
			}
		}
	}
}
