//go:build linux && 386

package i386_test

import (
	"math"
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
)

// expansionFrame is the frame of the test functions: four spill slots
// above no call area, and EBX, ESI, EDI and EBP saved.
const expansionFrame = 48

// referenceDivide applies the op.DivisionSemantics at width w.
func referenceDivide(operation op.Opcode, semantics op.DivisionSemantics, w uint8, x, y uint64) uint64 {
	mask := uint64(math.MaxUint64) >> (64 - w)
	sx, sy := int64(x<<(64-w))>>(64-w), int64(y<<(64-w))>>(64-w)
	signed := operation == op.OpDivSigned || operation == op.OpRemSigned
	quotient := operation == op.OpDiv || operation == op.OpDivSigned
	switch {
	case y == 0 && quotient && semantics == op.DivisionAllOnes:
		return mask
	case y == 0 && quotient:
		return 0
	case y == 0:
		return x
	case signed && sy == -1:
		if quotient {
			return uint64(-sx) & mask
		}
		return 0
	case operation == op.OpDiv:
		return x / y
	case operation == op.OpRem:
		return x % y
	case operation == op.OpDivSigned:
		return uint64(sx/sy) & mask
	}
	return uint64(sx%sy) & mask
}

// divisionInputs mixes edge values with random ones of random magnitude,
// which reach every path of the 64-bit expansion.
func divisionInputs(w uint8, n int) [][2]uint64 {
	mask := uint64(math.MaxUint64) >> (64 - w)
	edges := []uint64{0, 1, 2, 3, 7, mask, mask - 1, mask >> 1, mask>>1 + 1, mask>>1 + 2, 0xffffffff, 0x100000000, 0x80000000, 0xfffffffffffffffe}
	var out [][2]uint64
	for _, x := range edges {
		for _, y := range edges {
			out = append(out, [2]uint64{x & mask, y & mask})
		}
	}
	r := rand.New(rand.NewPCG(1, uint64(w)))
	value := func() uint64 { return r.Uint64() >> r.IntN(64) & mask }
	for range n {
		x, y := value(), value()
		if r.IntN(4) == 0 {
			y = x/uint64(1+r.IntN(5)) + uint64(r.IntN(3)) - 1 // quotients near 1-5
		}
		out = append(out, [2]uint64{x & mask, y & mask})
	}
	return out
}

func TestDivideExecution(t *testing.T) {
	for _, w := range []uint8{8, 16, 32, 64} {
		for _, operation := range []op.Opcode{op.OpDiv, op.OpDivSigned, op.OpRem, op.OpRemSigned} {
			for _, semantics := range []op.DivisionSemantics{op.DivisionZero, op.DivisionAllOnes, op.DivisionUnchecked} {
				ins := machine.Instruction{Op: machine.OpDivide, Bits: w, Arithmetic: operation, Imm: uint64(semantics)}
				var load func(e *i386.ByteAssembler, word func(backend.Register, int))
				if w == 64 {
					ins.A, ins.HighA, ins.Slot = 0, 2, 0
					load = func(e *i386.ByteAssembler, word func(backend.Register, int)) {
						for n := range 4 {
							word(0, n)
							e.Spill(0, n)
						}
					}
				}
				// One function per aliasing; each runs every input.
				type alias struct{ a, b, c backend.Register }
				aliases := []alias{{3, 6, 7}, {6, 6, 7}, {7, 6, 7}, {0, 2, 1}}
				if w == 64 {
					aliases = aliases[:1]
				}
				for _, al := range aliases {
					if w != 64 {
						ins.A, ins.B, ins.C = al.a, al.b, al.c
						al := al
						load = func(e *i386.ByteAssembler, word func(backend.Register, int)) {
							word(al.b, 0)
							word(al.c, 1)
						}
					}
					run := buildExpansion(t, load, ins, w != 64, ins.A)
					for _, in := range divisionInputs(w, 3000) {
						x, y := in[0], in[1]
						mask := uint64(math.MaxUint64) >> (64 - w)
						if semantics == op.DivisionUnchecked && (y == 0 || (operation == op.OpDivSigned || operation == op.OpRemSigned) && x == mask>>1+1 && y == mask) {
							continue
						}
						var got uint64
						if w == 64 {
							got = run(uintptr(x), uintptr(x>>32), uintptr(y), uintptr(y>>32))
						} else {
							got = run(uintptr(x), uintptr(y))
						}
						if want := referenceDivide(operation, semantics, w, x, y); got != want {
							t.Fatalf("%v/%d sem%d alias%v: %#x, %#x = %#x, want %#x", operation, w, semantics, al, x, y, got, want)
						}
					}
				}
			}
		}
	}
}

// buildExpansion is callExpansion for many calls; with word set, the result
// is the word in r.
func buildExpansion(t *testing.T, load func(e *i386.ByteAssembler, word func(backend.Register, int)), body machine.Instruction, word bool, r backend.Register) func(args ...uintptr) uint64 {
	t.Helper()
	e := i386.NewByteAssembler(abi.StackPlatform)
	e.FrameEnter(expansionFrame)
	load(e, func(reg backend.Register, n int) { e.IncomingWord(reg, 4+4*n) })
	if err := machine.Visit(body, e); err != nil {
		t.Fatal(err)
	}
	if word {
		e.Move(0, r)
		e.Constant(2, 0)
	}
	e.FrameLeave(expansionFrame)
	e.Ret()
	if e.Err() != nil {
		t.Fatalf("%+v: %v", body, e.Err())
	}
	code, err := execution.NewCode(e.Bytes(), execution.CodeOptions{ABI: abi.C, Arity: 4, ArgumentWords: 4}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { code.Close() })
	return func(args ...uintptr) uint64 {
		for len(args) < 4 {
			args = append(args, 0)
		}
		lo, hi, err := code.CallPair(args...)
		if err != nil {
			t.Fatal(err)
		}
		return execution.Wide(lo, hi)
	}
}

func TestIntToFloatExecution(t *testing.T) {
	xmm := backend.VectorRegister(4)
	r := rand.New(rand.NewPCG(2, 3))
	var inputs []uint64
	for _, x := range []uint64{0, 1, 2, 1<<24 + 1, 1<<53 + 1, 1<<63 + 1<<11 + 1, 0xfffffffffffff801, 0x7ffffffffffffc01, 1<<31 + 1, 0xffffff01,
		0x80000000, 0xffffffff, 0x7fffffff, 1 << 63, math.MaxUint64, 1<<53 - 1, 1 << 53, 1<<54 + 3, 0xffe0000000000000, 0xffdfffffffffffff, 0x0020000000000000, 0x001fffffffffffff} {
		inputs = append(inputs, x, -x)
	}
	for range 20000 {
		x := r.Uint64() >> r.IntN(64)
		if r.IntN(2) == 0 {
			// Halfway cases and their neighbours.
			x = x&^(1<<r.IntN(40)-1) | uint64(r.IntN(3))
		}
		inputs = append(inputs, x)
	}
	for _, source := range []uint64{32, 64} {
		for _, out := range []uint8{32, 64} {
			for _, operation := range []op.Opcode{op.OpFFromSigned, op.OpFFromUnsigned} {
				ins := machine.Instruction{Op: machine.OpIntToFloat, Bits: out, Arithmetic: operation, Imm: source, A: xmm, B: 3, HighB: 6, Slot: 1}
				e := func(e *i386.ByteAssembler, word func(backend.Register, int)) {
					word(3, 0)
					word(6, 1)
				}
				run := buildFloatResult(t, e, ins, out)
				for _, x := range inputs {
					if source == 32 {
						x = uint64(uint32(x))
					}
					var want uint64
					signed := operation == op.OpFFromSigned
					switch {
					case signed && source == 32 && out == 32:
						want = uint64(math.Float32bits(float32(int32(x))))
					case signed && source == 32:
						want = math.Float64bits(float64(int32(x)))
					case signed && out == 32:
						want = binary32(new(big.Float).SetInt64(int64(x)))
					case signed:
						want = math.Float64bits(float64(int64(x)))
					case out == 32:
						want = binary32(new(big.Float).SetUint64(x))
					default:
						want = math.Float64bits(float64(x))
					}
					if got := run(uintptr(x), uintptr(x>>32)); got != want {
						t.Fatalf("%v %d->%d: %#x gave %#x, want %#x", operation, source, out, x, got, want)
					}
				}
			}
		}
	}
}

// binary32 rounds f to nearest, ties to even. Go's own 64-bit integer to
// float32 conversion on 386 (runtime.uint64tofloat32) rounds some values
// from 2^46 to 2^48 incorrectly, so it cannot be the reference.
func binary32(f *big.Float) uint64 {
	x, _ := f.SetMode(big.ToNearestEven).Float32()
	return uint64(math.Float32bits(x))
}

// buildFloatResult runs body and returns the bits of XMM A, Bits wide.
func buildFloatResult(t *testing.T, load func(e *i386.ByteAssembler, word func(backend.Register, int)), body machine.Instruction, w uint8) func(args ...uintptr) uint64 {
	t.Helper()
	e := i386.NewByteAssembler(abi.StackPlatform)
	e.FrameEnter(expansionFrame)
	load(e, func(reg backend.Register, n int) { e.IncomingWord(reg, 4+4*n) })
	if err := machine.Visit(body, e); err != nil {
		t.Fatal(err)
	}
	e.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: w, A: 0, HighA: 2, B: body.A})
	if w == 32 {
		e.Constant(2, 0)
	}
	e.FrameLeave(expansionFrame)
	e.Ret()
	if e.Err() != nil {
		t.Fatalf("%+v: %v", body, e.Err())
	}
	code, err := execution.NewCode(e.Bytes(), execution.CodeOptions{ABI: abi.C, Arity: 4, ArgumentWords: 4}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { code.Close() })
	return func(args ...uintptr) uint64 {
		for len(args) < 4 {
			args = append(args, 0)
		}
		lo, hi, err := code.CallPair(args...)
		if err != nil {
			t.Fatal(err)
		}
		return execution.Wide(lo, hi)
	}
}

// referenceFloatToInt truncates f and saturates, NaN giving 0.
func referenceFloatToInt(signed bool, w uint8, f float64) uint64 {
	mask := uint64(math.MaxUint64) >> (64 - w)
	t := math.Trunc(f)
	switch {
	case math.IsNaN(f):
		return 0
	case signed && t < -math.Ldexp(1, int(w)-1):
		return mask>>1 + 1
	case signed && t >= math.Ldexp(1, int(w)-1):
		return mask >> 1
	case signed:
		return uint64(int64(t)) & mask
	case t < 0:
		return 0
	case t >= math.Ldexp(1, int(w)):
		return mask
	}
	return uint64(t)
}

// inRange reports whether f truncates to a w-bit integer.
func inRange(signed bool, w uint8, f float64) bool {
	t := math.Trunc(f)
	if signed {
		return t >= -math.Ldexp(1, int(w)-1) && t < math.Ldexp(1, int(w)-1)
	}
	return t >= 0 && t < math.Ldexp(1, int(w))
}

func TestFloatToIntExecution(t *testing.T) {
	xmm := backend.VectorRegister(5)
	r := rand.New(rand.NewPCG(4, 5))
	var floats []float64
	for _, f := range []float64{0, -0.0, 0.5, 0.999, 1, 1.5, 2147483647.5, 2147483648, 4294967295.5, 4294967296, 9223372036854774784, 9223372036854775808,
		18446744073709549568, 18446744073709551616, 1e30, math.Inf(1), math.NaN(), math.SmallestNonzeroFloat64, 1 << 52, 1<<53 + 2, 1 << 62} {
		floats = append(floats, f, -f)
	}
	for range 20000 {
		floats = append(floats, math.Ldexp(r.Float64()+0.5, r.IntN(70)-2)*float64(1-2*r.IntN(2)))
	}
	for _, source := range []uint64{32, 64} {
		for _, out := range []uint8{32, 64} {
			for _, operation := range []op.Opcode{op.OpFToSigned, op.OpFToUnsigned} {
				for _, semantics := range []op.ConversionSemantics{op.ConversionSaturate, op.ConversionNative} {
					ins := machine.Instruction{Op: machine.OpFloatToInt, Bits: out, Arithmetic: operation, Imm: uint64(semantics), Disp: source, A: 3, B: xmm}
					if out == 64 {
						ins.A, ins.HighA = 0, 2
					}
					load := func(e *i386.ByteAssembler, word func(backend.Register, int)) {
						word(0, 0)
						word(2, 1)
						e.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: uint8(source), A: xmm, B: 0, HighB: 2})
					}
					run := buildExpansion(t, load, ins, out == 32, ins.A)
					signed := operation == op.OpFToSigned
					for _, f := range floats {
						x := math.Float64bits(f)
						if source == 32 {
							x = uint64(math.Float32bits(float32(f)))
							f = float64(float32(f))
						}
						want := referenceFloatToInt(signed, out, f)
						if semantics == op.ConversionNative && !inRange(signed, out, f) {
							continue // out of range: any result
						}
						if got := run(uintptr(x), uintptr(x>>32)); got != want {
							t.Fatalf("%v %d->%d sem%d: %v (%#x) gave %#x, want %#x", operation, source, out, semantics, f, x, got, want)
						}
					}
				}
			}
		}
	}
}
