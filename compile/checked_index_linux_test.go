//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"fmt"
	"math/bits"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func checkedIndexCode(t *testing.T, b *n.Builder, pressure bool) *execution.Code {
	t.Helper()
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	template, err := lowerTemplate(p, compile.TargetSpec{}, compile.Func{ABI: abi.C, Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
	if err != nil {
		t.Fatal(err)
	}
	if pressure && template.Report().StackBytes() < 96*int(builder.NativeBits/8) {
		t.Fatalf("pressure fixture lacks spill homes: %+v", template.Report().Snapshot())
	}
	c, err := template.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func checkedIndexResult(t *testing.T, c *execution.Code, want uint64, args ...any) {
	t.Helper()
	lo, hi, err := c.CallPair(execution.Words(args...)...)
	if got := execution.Wide(lo, hi); err != nil || got != want {
		t.Fatalf("args %v: got %#x, %v; want %#x", args, got, err, want)
	}
}
func compareReference(c ir.BranchCondition, a, b uint64) bool {
	switch c {
	case ir.BranchEqual:
		return a == b
	case ir.BranchNotEqual:
		return a != b
	case ir.BranchLess:
		return int64(a) < int64(b)
	case ir.BranchGreaterEqual:
		return int64(a) >= int64(b)
	case ir.BranchLessEqual:
		return int64(a) <= int64(b)
	case ir.BranchGreater:
		return int64(a) > int64(b)
	case ir.BranchBelow:
		return a < b
	case ir.BranchAboveEqual:
		return a >= b
	case ir.BranchBelowEqual:
		return a <= b
	case ir.BranchAbove:
		return a > b
	}
	panic("condition")
}

func TestCheckedIndexCompareImmediateEdges(t *testing.T) {
	constants := []uint64{4095, 4096, 1<<31 - 1, 1 << 31, 1<<32 - 1, 1 << 32}
	for _, k := range constants {
		for condition := ir.BranchEqual; condition <= ir.BranchAbove; condition++ {
			t.Run(fmt.Sprintf("%x/%d", k, condition), func(t *testing.T) {
				b := n.NewBuilder()
				x := b.Param()
				yes, no := b.NewLabel(), b.NewLabel()
				b.Branch(b.CompareImmediate(x, condition, int64(k)), yes)
				b.Jump(no)
				b.Bind(yes)
				b.Return(b.Const(1))
				b.Bind(no)
				b.Return(b.Const(0))
				c := checkedIndexCode(t, b, false)
				for _, x := range []uint64{0, k - 1, k, k + 1, 1<<63 - 1, 1 << 63, ^uint64(0)} {
					var want uint64
					if compareReference(condition, x, k) {
						want = 1
					}
					checkedIndexResult(t, c, want, x)
				}
			})
		}
	}
	for _, count := range constants {
		t.Run(fmt.Sprintf("bounds/%x", count), func(t *testing.T) {
			b := n.NewBuilder()
			index := b.Param()
			bad := b.NewLabel()
			address := b.IndexedAddress(b.Const(0x1000), index, b.Const(count), 8, bad)
			b.Return(address)
			b.Bind(bad)
			b.Return(b.Const(0xdead))
			c := checkedIndexCode(t, b, false)
			for _, i := range []uint64{0, count - 1, count, count + 1, 1 << 32, 1 << 63, ^uint64(0)} {
				want := uint64(0xdead)
				if i < count {
					want = 0x1000 + i*8
				}
				checkedIndexResult(t, c, want, i)
			}
		})
	}
}

func TestCheckedIndexDirtyUpper32Bits(t *testing.T) {
	b := n.NewBuilder()
	input := b.ParamBits(32)
	wrapped := b.Add(input, b.ConstBits(32, 2))
	index := b.CastUnsigned(wrapped, 64)
	bad := b.NewLabel()
	address := b.IndexedAddress(b.Const(0x1000), index, b.Const(4), 8, bad)
	b.Return(address)
	b.Bind(bad)
	b.Return(b.Const(0xdead))
	c := checkedIndexCode(t, b, false)
	for _, input := range []uint64{0, 1, 2, 0xffffffff, 0xfffffffe, 0xdeadbeef00000000, 0xdeadbeefffffffff, 0xdeadbeef00000002, 0x80000000ffffffff} {
		i := uint64(uint32(input) + uint32(2))
		want := uint64(0xdead)
		if i < 4 {
			want = 0x1000 + i*8
		}
		var arg any = input
		if builder.NativeBits == 32 {
			arg = uint32(input)
		}
		checkedIndexResult(t, c, want, arg)
	}
}

func TestCheckedIndexNarrowSpillReload(t *testing.T) {
	for _, width := range []uint8{8, 16, 32} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			b := n.NewBuilder()
			base := b.ParamNative()
			values := make([]n.Var, 96)
			for i := range values {
				values[i] = b.Var(b.LoadBits(b.Add(base, b.ConstNative(uintptr(i*8))), width))
			}
			bad := b.NewLabel()
			index := b.CastUnsigned(b.Get(values[0]), 64)
			address := b.IndexedAddress(b.Const(0x1000), index, b.Const(uint64(1)<<width), 8, bad)
			sum := address
			for _, v := range values {
				sum = b.Xor(b.RotateLeft(sum, 7), b.CastUnsigned(b.Get(v), 64))
			}
			b.Return(sum)
			b.Bind(bad)
			b.Return(b.Const(0xdead))
			c := checkedIndexCode(t, b, true)
			for _, seed := range []uint64{0, 1, 0x123456789abcdef0, ^uint64(0)} {
				var input [96]uint64
				mask := uint64(1)<<width - 1
				for i := range input {
					input[i] = (^mask) | (seed+uint64(i)*0x9e3779b9)&mask
				}
				want := uint64(0x1000) + (input[0]&mask)*8
				for _, v := range input {
					want = bits.RotateLeft64(want, 7) ^ (v & mask)
				}
				checkedIndexResult(t, c, want, &input)
			}
		})
	}
}

func TestCheckedDataIndexMemoryBoundaries(t *testing.T) {
	b := n.NewBuilder()
	data := n.SliceParam[uint32](b)
	input := b.ParamBits(32)
	index := b.Add(input, b.ConstBits(32, 2))
	bad := b.NewLabel()
	elem := data.Index(index, bad)
	b.Return(b.CastUnsigned(elem.Load(), 64))
	b.Bind(bad)
	b.Return(b.Const(0xdead))
	c := checkedIndexCode(t, b, false)
	backing := make([]uint32, 4096)
	for i := range backing {
		backing[i] = uint32(i) + 0x100000
	}
	for _, data := range [][]uint32{nil, backing[:0], backing[:4095], backing} {
		for _, input := range []uint64{0xfffffffe, 0xffffffff, 4092, 4093, 4094, 0xdeadbeefffffffff, 0xdeadbeef00000ffd} {
			index := uint32(input) + 2
			want := uint64(0xdead)
			if uint64(index) < uint64(len(data)) {
				want = uint64(data[index])
			}
			var arg any = input
			if builder.NativeBits == 32 {
				arg = uint32(input)
			}
			checkedIndexResult(t, c, want, data, arg)
		}
	}
}

func TestCheckedDataArrayImmediateBoundaries(t *testing.T) {
	for _, count := range []int{4095, 4096} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			b := n.NewBuilder()
			var data n.Data
			var input any
			if count == 4095 {
				var a [4095]uint32
				for i := range a {
					a[i] = uint32(i) + 0x100000
				}
				input = &a
				data = n.PointerParam[[4095]uint32](b)
			} else {
				var a [4096]uint32
				for i := range a {
					a[i] = uint32(i) + 0x100000
				}
				input = &a
				data = n.PointerParam[[4096]uint32](b)
			}
			i := b.Param()
			bad := b.NewLabel()
			elem := data.Index(i, bad)
			b.Return(b.CastUnsigned(elem.Load(), 64))
			b.Bind(bad)
			b.Return(b.Const(0xdead))
			c := checkedIndexCode(t, b, false)
			for _, i := range []uint64{0, uint64(count - 1), uint64(count), uint64(count + 1), 1 << 32, ^uint64(0)} {
				want := uint64(0xdead)
				if i < uint64(count) {
					want = i + 0x100000
				}
				checkedIndexResult(t, c, want, input, i)
			}
		})
	}
}
