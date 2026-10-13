//go:build linux && riscv64

package riscv64

import (
	"math"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func referenceFloatCompare(c op.FloatCondition, x, y float64) bool {
	unordered := x != x || y != y
	var holds bool
	switch c &^ 1 {
	case op.FloatEqual:
		holds = x == y
	case op.FloatLess:
		holds = x < y
	case op.FloatLessEqual:
		holds = x <= y
	case op.FloatGreater:
		holds = x > y
	case op.FloatGreaterEqual:
		holds = x >= y
	case op.FloatOrdered:
		holds = !unordered
	case op.FloatOrderedNotEqual:
		holds = x < y || x > y
	}
	return holds != (c&1 != 0)
}

// TestFloatCompareExecutes runs every condition and width on signed zeros,
// infinities and NaNs against Go's comparisons.
func TestFloatCompareExecutes(t *testing.T) {
	values := []float64{0, math.Copysign(0, -1), 1, -1, 1.5, math.SmallestNonzeroFloat64, math.MaxFloat64,
		math.Inf(1), math.Inf(-1), math.NaN(), -math.NaN()}
	f, g := backend.VectorRegister(6), backend.VectorRegister(7)
	for _, w := range []uint8{32, 64} {
		for c := op.FloatEqual; c <= op.FloatUnorderedEqual; c++ {
			a := NewByteAssembler(abi.StackNoUse, 0)
			a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: w, A: f, B: 10})
			a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: w, A: g, B: 11})
			// A starts with stale upper bits that the 0/1 result must replace.
			a.Constant(6, 0xdead)
			if err := machine.Visit(machine.Instruction{Op: machine.OpFloatCompare, Bits: w, A: 6, B: f, C: g, Imm: uint64(c)}, a); err != nil {
				t.Fatal(err)
			}
			a.Move(10, 6)
			a.Ret()
			for _, x := range values {
				for _, y := range values {
					xb, yb := math.Float64bits(x), math.Float64bits(y)
					if w == 32 {
						x, y = float64(float32(x)), float64(float32(y))
						xb, yb = uint64(math.Float32bits(float32(x))), uint64(math.Float32bits(float32(y)))
					}
					want := uint64(0)
					if referenceFloatCompare(c, x, y) {
						want = 1
					}
					if got := executeWords(t, a, uintptr(xb), uintptr(yb)); got != want {
						t.Fatalf("condition %d, %d bits: (%v, %v) = %d, want %d", c, w, x, y, got, want)
					}
				}
			}
		}
	}
}
