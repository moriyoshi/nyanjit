package nyanjit_test

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// fpEdgeCase is a branch whose integer predicate reads a value that the taken
// edge carries to a block reading it as a float. The destination's FP home
// must not leak into the integer comparison. The value reaches the block by
// dominance, which lowering threads as an edge argument, or through a Var.
type fpEdgeCase struct {
	bits    uint8
	compare string // "zero", "immediate" or "register"
	operand int    // 0: the edge carries x, 1: the edge carries y
	other   string // the false edge: "int" or "fp" consumer of the same value, or "none"
	viaVar  bool   // the destinations read the value through a Var
}

func (c fpEdgeCase) String() string {
	return fmt.Sprintf("%d-bit %s operand%d other=%s var=%v", c.bits, c.compare, c.operand, c.other, c.viaVar)
}

func fpEdgeCases() []fpEdgeCase {
	var cases []fpEdgeCase
	for _, bits := range []uint8{32, 64} {
		for _, compare := range []string{"zero", "immediate", "register"} {
			for operand := range 2 {
				for _, other := range []string{"int", "fp", "none"} {
					for _, viaVar := range []bool{false, true} {
						cases = append(cases, fpEdgeCase{bits, compare, operand, other, viaVar})
					}
				}
			}
		}
	}
	return cases
}

func widen(b *nyanjit.Builder, v nyanjit.Value, bits uint8) nyanjit.Value {
	if bits == 64 {
		return v
	}
	return b.CastUnsigned(v, 64)
}

func (c fpEdgeCase) build() (*ir.SealedProgram, error) {
	b := nyanjit.NewBuilder()
	x, y := b.ParamBits(c.bits), b.ParamBits(c.bits)
	var predicate nyanjit.Predicate
	switch c.compare {
	case "zero":
		predicate = b.IsZero(x)
	case "immediate":
		predicate = b.CompareImmediate(x, ir.BranchBelow, 17)
	default:
		predicate = b.Compare(x, ir.BranchBelow, y)
	}
	carried := []nyanjit.Value{x, y}[c.operand]
	read := func() nyanjit.Value { return carried }
	if c.viaVar {
		v := b.Var(carried)
		read = func() nyanjit.Value { return b.Get(v) }
	}
	yes, no := b.NewLabel(), b.NewLabel()
	b.Branch(predicate, yes)
	b.Jump(no)
	b.Bind(yes)
	p := read()
	b.Return(widen(b, b.FAdd(p, p), c.bits))
	b.Bind(no)
	switch c.other {
	case "int":
		b.Return(widen(b, b.Add(read(), b.ConstBits(c.bits, 1)), c.bits))
	case "fp":
		q := read()
		b.Return(widen(b, b.FAdd(q, q), c.bits))
	default:
		b.Return(b.ConstBits(64, 7))
	}
	return b.Finish()
}

func (c fpEdgeCase) want(x, y uint64) uint64 {
	var taken bool
	switch c.compare {
	case "zero":
		taken = x == 0
	case "immediate":
		taken = x < 17
	default:
		taken = x < y
	}
	carried := []uint64{x, y}[c.operand]
	double := func() uint64 {
		if c.bits == 32 {
			f := math.Float32frombits(uint32(carried))
			return uint64(math.Float32bits(f + f))
		}
		f := math.Float64frombits(carried)
		return math.Float64bits(f + f)
	}
	switch {
	case taken, c.other == "fp":
		return double()
	case c.other == "int":
		if c.bits == 32 {
			return uint64(uint32(carried) + 1)
		}
		return carried + 1
	}
	return 7
}

func (c fpEdgeCase) inputs() [][2]uint64 {
	f := func(v float64) uint64 {
		if c.bits == 32 {
			return uint64(math.Float32bits(float32(v)))
		}
		return math.Float64bits(v)
	}
	// Both outcomes of every comparison, with float bit patterns on both sides.
	return [][2]uint64{{0, f(2.5)}, {3, f(1.5)}, {f(1.5), f(2.5)}, {f(2.5), f(1.5)}, {f(-0.75), 0}, {16, 17}}
}

// TestBranchCarriesFloatOperandOfIntegerPredicate covers integer predicates
// whose operand an edge passes to a float-consuming block.
func TestBranchCarriesFloatOperandOfIntegerPredicate(t *testing.T) {
	for _, c := range fpEdgeCases() {
		program, err := c.build()
		if err != nil {
			t.Fatalf("%v: %v", c, err)
		}
		for _, arch := range []string{"amd64", "arm64", "386", "riscv64", "loong64", "ppc64le"} {
			target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch})
			if err != nil {
				t.Fatal(err)
			}
			unit, err := compile.Lower(target, program, compile.Func{ABI: abi.C})
			if err == nil {
				_, err = unit.Listing()
			}
			if err != nil {
				t.Errorf("%v on %s: %v", c, arch, err)
			}
		}
		if runtime.GOOS != "linux" {
			continue
		}
		for name, f := range map[string]compile.Func{
			"workspace": {ABI: abi.C},
			"platform":  {ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}},
			"hardened":  {ABI: abi.C, Harden: true},
		} {
			code, err := nyanjit.Compile(program, compile.TargetSpec{}, f)
			if name == "hardened" && errors.Is(err, compile.ErrUnsupported) {
				continue // some targets cannot harden control flow
			}
			if err != nil {
				t.Fatalf("%v %s: %v", c, name, err)
			}
			for _, in := range c.inputs() {
				var args []uintptr
				if c.bits == 32 {
					args = execution.Words(uint32(in[0]), uint32(in[1]))
				} else {
					args = execution.Words(in[0], in[1])
				}
				lo, hi, err := code.CallPair(args...)
				if got, want := execution.Wide(lo, hi), c.want(in[0], in[1]); err != nil || got != want {
					t.Errorf("%v %s: f(%#x, %#x) = %#x, %v; want %#x", c, name, in[0], in[1], got, err, want)
				}
			}
			code.Close()
		}
	}
}

// The reviewer's reproducer: a 64-bit parameter compared with an immediate
// and narrowed to binary32 on the taken edge.
func TestBranchFloatConvertOfComparedParameter(t *testing.T) {
	b := nyanjit.NewBuilder()
	x := b.ParamBits(64)
	yes, no := b.NewLabel(), b.NewLabel()
	b.Branch(b.CompareImmediate(x, ir.BranchBelow, 17), yes)
	b.Jump(no)
	b.Bind(yes)
	b.Return(b.CastUnsigned(b.FConvert(x, 32), 64))
	b.Bind(no)
	b.Return(b.Const(0))
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"amd64", "arm64", "386", "riscv64", "loong64", "ppc64le"} {
		target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch})
		if err != nil {
			t.Fatal(err)
		}
		unit, err := compile.Lower(target, program, compile.Func{ABI: abi.C})
		if err == nil {
			_, err = unit.Listing()
		}
		if err != nil {
			t.Errorf("%s: %v", arch, err)
		}
	}
	if runtime.GOOS != "linux" {
		return
	}
	code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.C})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, x := range []uint64{0, 16, math.Float64bits(2.5)} {
		want := uint64(0)
		if x < 17 {
			want = uint64(math.Float32bits(float32(math.Float64frombits(x))))
		}
		lo, hi, err := code.CallPair(execution.Words(x)...)
		if got := execution.Wide(lo, hi); err != nil || got != want {
			t.Errorf("f(%#x) = %#x, %v; want %#x", x, got, err, want)
		}
	}
}
