//go:build linux && riscv64

package riscv64

import (
	"math"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func executeWords(t *testing.T, a *ByteAssembler, args ...uintptr) uint64 {
	t.Helper()
	if a.Err() != nil {
		t.Fatal(a.Err())
	}
	code, err := execution.NewCode(a.Bytes(), execution.CodeOptions{ABI: abi.C, Arity: len(args)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	got, err := code.Call(args...)
	if err != nil {
		t.Fatal(err)
	}
	return uint64(got)
}

func referenceDivide(operation op.Opcode, semantics op.DivisionSemantics, w uint8, x, y uint64) uint64 {
	m := ^uint64(0) >> (64 - w)
	shift := 64 - w
	sx, sy := int64(x<<shift)>>shift, int64(y<<shift)>>shift
	switch {
	case y == 0 && (operation == op.OpRem || operation == op.OpRemSigned):
		return x
	case y == 0 && semantics == op.DivisionZero:
		return 0
	case y == 0:
		return m
	case operation == op.OpDiv:
		return x / y
	case operation == op.OpRem:
		return x % y
	case operation == op.OpDivSigned:
		return uint64(sx/sy) & m // Go defines MIN / -1 as MIN.
	}
	return uint64(sx%sy) & m
}

// TestDivisionExecutes runs every width, operation, semantics and register
// alias against a Go reference, including zero divisors and MIN / -1.
func TestDivisionExecutes(t *testing.T) {
	for _, form := range divisionForms() {
		// Use allocatable registers outside the argument registers.
		regs := map[backend.Register]backend.Register{10: 5, 11: 6, 12: 7}
		ins := form
		ins.A, ins.B, ins.C = regs[form.A], regs[form.B], regs[form.C]
		a := NewByteAssembler(abi.StackNoUse, 0)
		a.Move(ins.B, 10)
		if ins.C != ins.B {
			a.Move(ins.C, 11)
		}
		if err := machine.Visit(ins, a); err != nil {
			t.Fatal(err)
		}
		a.Move(10, ins.A)
		a.Ret()
		code, err := execution.NewCode(a.Bytes(), execution.CodeOptions{ABI: abi.C, Arity: 2}, nil)
		if err != nil {
			t.Fatal(err)
		}
		w := ins.Bits
		m := ^uint64(0) >> (64 - w)
		min := uint64(1) << (w - 1)
		values := []uint64{0, 1, 2, 3, 7, 100, min - 1, min, min + 1, m - 1, m, m / 3}
		for _, x := range values {
			for _, y := range values {
				if ins.B == ins.C {
					y = x
				}
				signed := ins.Arithmetic == op.OpDivSigned || ins.Arithmetic == op.OpRemSigned
				if op.DivisionSemantics(ins.Imm) == op.DivisionUnchecked && (y == 0 || signed && x == min && y == m) {
					continue
				}
				got, err := code.Call(uintptr(x), uintptr(y))
				if err != nil {
					t.Fatal(err)
				}
				if want := referenceDivide(ins.Arithmetic, op.DivisionSemantics(ins.Imm), w, x, y); uint64(got) != want {
					t.Fatalf("%v Bits=%d Imm=%d A=%d B=%d C=%d: %#x, %#x = %#x, want %#x", ins.Arithmetic, w, ins.Imm, ins.A, ins.B, ins.C, x, y, got, want)
				}
			}
		}
		code.Close()
	}
}

func referenceFloatToInt(operation op.Opcode, semantics op.ConversionSemantics, bits uint8, f float64) uint64 {
	m := ^uint64(0) >> (64 - bits)
	low, high := 0.0, math.Ldexp(1, int(bits)) // [low, high)
	max, min := m, uint64(0)
	if operation == op.OpFToSigned {
		low, high = -math.Ldexp(1, int(bits)-1), math.Ldexp(1, int(bits)-1)
		max, min = m>>1, (m>>1+1)&m
	}
	t := math.Trunc(f)
	switch {
	case math.IsNaN(f) && semantics == op.ConversionSaturate:
		return 0
	case math.IsNaN(f) || t >= high:
		return max // RISC-V converts NaN to the maximum.
	case t < low:
		return min
	case t < 0:
		return uint64(int64(t)) & m
	}
	return uint64(t)
}

func TestFloatToIntExecutes(t *testing.T) {
	values := []float64{0, math.Copysign(0, -1), 0.5, -0.5, 0.999, -1, -1.5, 42.75, 2147483647.5, -2147483648.5, -2147483649,
		2147483648, 4294967295.5, 4294967296, 9223372036854774784, 9223372036854775808, -9223372036854775808,
		-9223372036854777856, 18446744073709549568, 18446744073709551616, 1e30, -1e30,
		math.Inf(1), math.Inf(-1), math.NaN(), -math.NaN(), math.SmallestNonzeroFloat64}
	for _, ins := range conversionForms() {
		if ins.Op != machine.OpFloatToInt {
			continue
		}
		ins.A = 6
		a := NewByteAssembler(abi.StackNoUse, 0)
		a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: uint8(ins.Disp), A: ins.B, B: 10})
		if err := machine.Visit(ins, a); err != nil {
			t.Fatal(err)
		}
		a.Move(10, ins.A)
		a.Ret()
		for _, f := range values {
			bits := math.Float64bits(f)
			if ins.Disp == 32 {
				f = float64(float32(f))
				bits = uint64(math.Float32bits(float32(f)))
			}
			got := executeWords(t, a, uintptr(bits))
			if want := referenceFloatToInt(ins.Arithmetic, op.ConversionSemantics(ins.Imm), ins.Bits, f); got != want {
				t.Fatalf("%v Bits=%d Disp=%d Imm=%d (%v) = %#x, want %#x", ins.Arithmetic, ins.Bits, ins.Disp, ins.Imm, f, got, want)
			}
		}
	}
}

func TestIntToFloatExecutes(t *testing.T) {
	values := []uint64{0, 1, 42, 1<<24 + 1, 1<<53 + 1, 1<<63 + 1<<11 + 1, 0xfffffffffffff801, 0x7ffffffffffffc01,
		1<<31 + 1, 0xffffff01, 0x7fffffff, 0x80000000, 0xffffffff, 1 << 63, ^uint64(0)}
	for _, ins := range conversionForms() {
		if ins.Op != machine.OpIntToFloat {
			continue
		}
		ins.B = 6
		a := NewByteAssembler(abi.StackNoUse, 0)
		a.Move(ins.B, 10)
		if err := machine.Visit(ins, a); err != nil {
			t.Fatal(err)
		}
		a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: ins.Bits, A: 10, B: ins.A})
		a.Ret()
		for _, x := range values {
			if ins.Imm == 32 {
				x &= 0xffffffff
			}
			var f float64
			switch {
			case ins.Imm == 32 && ins.Arithmetic == op.OpFFromSigned:
				f = float64(int32(x))
			case ins.Imm == 32:
				f = float64(uint32(x))
			case ins.Arithmetic == op.OpFFromSigned:
				f = float64(int64(x))
				if ins.Bits == 32 {
					f = float64(float32(int64(x))) // Round once, from the integer.
				}
			default:
				f = float64(x)
				if ins.Bits == 32 {
					f = float64(float32(x))
				}
			}
			want := math.Float64bits(f)
			if ins.Bits == 32 {
				want = uint64(math.Float32bits(float32(f)))
			}
			if got := executeWords(t, a, uintptr(x)); got != want {
				t.Fatalf("%v Bits=%d Imm=%d (%#x) = %#x, want %#x", ins.Arithmetic, ins.Bits, ins.Imm, x, got, want)
			}
		}
	}
}
