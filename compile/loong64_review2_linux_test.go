//go:build linux && loong64

package compile_test

import (
	"math"
	"math/bits"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// Reduced from the differential review's zztarget_linux_test.go. Garbage in
// upper parameter bits must not affect either a narrow predicate or a value
// carried across its label through a Var.
func TestLoong64NarrowParametersWithUpperGarbage(t *testing.T) {
	for _, width := range []uint8{8, 16, 32} {
		b := n.NewBuilder()
		x, y := b.ParamBits(width), b.ParamBits(width)
		xv, yv := b.Var(x), b.Var(y)
		below := b.NewLabel()
		b.Branch(b.Compare(x, ir.BranchBelow, y), below)
		b.Return(b.Add(b.CastUnsigned(b.Get(xv), 64), b.ShiftLeft(b.CastUnsigned(b.Get(yv), 64), 1)))
		b.Bind(below)
		b.Return(b.Const(1))
		p, err := testFinishRaw(b)
		if err != nil {
			t.Fatal(err)
		}
		for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
			for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
				fn := compile.Func{ABI: flavor, Stack: compile.StackPolicy{Practice: stack}}
				if stack == abi.StackPlatform {
					fn.Stack.BytesAvailable = 4096
				}
				c, err := n.Compile(p, compile.TargetSpec{}, fn)
				if err != nil {
					t.Fatal(err)
				}
				for _, args := range [][2]uint64{{0xffffffff80000000, 0x7fffffff}, {0xdeadbeef00000080, 0x7f}, {0x12348000, 0x7fff}, {0x80, 0x55555555ffffff7f}} {
					mask := uint64(1)<<width - 1
					a, z := args[0]&mask, args[1]&mask
					want := a + z<<1
					if a < z {
						want = 1
					}
					got, err := c.Call(uintptr(args[0]), uintptr(args[1]))
					if err != nil || uint64(got) != want {
						t.Errorf("width%d ABI%d stack%d args%x: got%x,%v want%x", width, flavor, stack, args, got, err, want)
					}
				}
				c.Close()
			}
		}
	}
}

// Reversed consumption keeps every chain prefix live. This reaches spill
// displacements beyond the signed 12-bit immediate on both GP and FP paths.
func TestLoong64LargeSpillDisplacements(t *testing.T) {
	const count = 320
	for _, floating := range []bool{false, true} {
		b := n.NewBuilder()
		x, y := b.Param(), b.Param()
		value := x
		values := make([]n.Value, count)
		for i := range values {
			if floating {
				value = b.FAdd(b.FMul(value, y), b.Float64(float64(i)+0.5))
			} else {
				value = b.Add(b.Mul(value, y), b.Const(uint64(i)*0x10000001+0x800))
			}
			values[i] = value
		}
		sum := b.Const(0)
		for i := len(values) - 1; i >= 0; i-- {
			sum = b.Add(b.RotateLeft(sum, 5), values[i])
		}
		b.Return(sum)
		p, err := testFinishRaw(b)
		if err != nil {
			t.Fatal(err)
		}
		a, z := uint64(3), uint64(5)
		if floating {
			a, z = math.Float64bits(0.25), math.Float64bits(0.999)
		}
		ref := make([]uint64, count)
		v := a
		for i := range ref {
			if floating {
				// The explicit conversion prevents Go from contracting the reference.
				product := float64(math.Float64frombits(v) * math.Float64frombits(z))
				v = math.Float64bits(product + (float64(i) + 0.5))
			} else {
				v = v*z + uint64(i)*0x10000001 + 0x800
			}
			ref[i] = v
		}
		var want uint64
		for i := len(ref) - 1; i >= 0; i-- {
			want = bits.RotateLeft64(want, 5) + ref[i]
		}
		for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
			fn := compile.Func{Stack: compile.StackPolicy{Practice: stack}}
			if stack == abi.StackPlatform {
				fn.Stack.BytesAvailable = 65536
				fn.Context = compile.ContextForeign
			}
			c, err := n.Compile(p, compile.TargetSpec{}, fn)
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.Call(uintptr(a), uintptr(z))
			c.Close()
			if err != nil || uint64(got) != want {
				t.Errorf("floating%v stack%d got%x,%v want%x", floating, stack, got, err, want)
			}
			external := fn
			external.Context = compile.ContextExternal
			emission, err := lowerTemplate(p, compile.TargetSpec{}, external)
			if err != nil {
				t.Fatal(err)
			}
			report := emission.Report()
			if max(report.StackBytes(), report.WorkspaceBytes()) <= 2047 {
				t.Fatalf("floating%v did not reach large spills: frame%d workspace%d", floating, report.StackBytes(), report.WorkspaceBytes())
			}
		}
	}
}
