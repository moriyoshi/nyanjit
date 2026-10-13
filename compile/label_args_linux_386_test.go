//go:build linux && 386

package compile

import (
	"strconv"
	"testing"

	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/execution"
)

func compileLabel386(t *testing.T, b *builder.Builder) uint64 {
	t.Helper()
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := Compile(program, CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	lo, hi, err := code.CallPair()
	if err != nil {
		t.Fatal(err)
	}
	return execution.Wide(lo, hi)
}

func labelConst386(t *testing.T, b *builder.Builder, bits uint8, value uint64) builder.Value {
	t.Helper()
	v, err := b.ConstBits(bits, value)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func labelCheck386(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestLabelArgumentsLoop386(t *testing.T) {
	b := builder.NewBuilder()
	entry, loop, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
	labelCheck386(t, b.Bind(entry))
	labelCheck386(t, b.JumpArgs(loop, labelConst386(t, b, 32, 5)))
	values, err := b.BindParamsBits(loop, []uint8{32})
	labelCheck386(t, err)
	next, err := b.Sub(values[0], labelConst386(t, b, 32, 1))
	labelCheck386(t, err)
	nonzero, err := b.CompareImmediate(next, builder.BranchNotEqual, 0)
	labelCheck386(t, err)
	labelCheck386(t, b.BranchArgs(nonzero, loop, []builder.Value{next}, done, []builder.Value{next}))
	result, err := b.BindParamsBits(done, []uint8{32})
	labelCheck386(t, err)
	labelCheck386(t, b.Return(result[0]))
	if got := compileLabel386(t, b); got != 0 {
		t.Fatalf("loop result = %d, want 0", got)
	}
}

func TestLabelArgumentsParallelCopies386(t *testing.T) {
	for _, tc := range []struct {
		count int
		cycle bool
		want  uint64
	}{
		{count: 3, want: 2211},
		{count: 4, cycle: true, want: 311},
		{count: 8, want: 2211},
		{count: 9, cycle: true, want: 311},
	} {
		t.Run(strconv.Itoa(tc.count), func(t *testing.T) {
			b := builder.NewBuilder()
			entry, loop, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
			labelCheck386(t, b.Bind(entry))
			initial := make([]builder.Value, tc.count)
			initial[0] = labelConst386(t, b, 32, 1)
			for i := 1; i < tc.count-2; i++ {
				initial[i] = labelConst386(t, b, 32, uint64(i))
			}
			if tc.cycle {
				initial[tc.count-3] = labelConst386(t, b, 32, 3)
			}
			initial[tc.count-2] = labelConst386(t, b, 32, 11)
			initial[tc.count-1] = labelConst386(t, b, 32, 22)
			labelCheck386(t, b.JumpArgs(loop, initial...))
			widths := make([]uint8, tc.count)
			for i := range widths {
				widths[i] = 32
			}
			values, err := b.BindParamsBits(loop, widths)
			labelCheck386(t, err)
			zero, err := b.IsZero(values[0])
			labelCheck386(t, err)
			nextCount, err := b.Sub(values[0], labelConst386(t, b, 32, 1))
			labelCheck386(t, err)
			next := append([]builder.Value(nil), values...)
			next[0] = nextCount
			if tc.cycle {
				next[tc.count-3], next[tc.count-2], next[tc.count-1] = values[tc.count-1], values[tc.count-3], values[tc.count-2]
			} else {
				next[tc.count-2], next[tc.count-1] = values[tc.count-1], values[tc.count-2]
			}
			labelCheck386(t, b.BranchArgs(zero, done, []builder.Value{values[tc.count-2], values[tc.count-1]}, loop, next))
			result, err := b.BindParamsBits(done, []uint8{32, 32})
			labelCheck386(t, err)
			left, err := b.Mul(result[0], labelConst386(t, b, 32, 100))
			labelCheck386(t, err)
			answer, err := b.Add(left, result[1])
			labelCheck386(t, err)
			labelCheck386(t, b.Return(answer))
			if got := compileLabel386(t, b); got != tc.want {
				t.Fatalf("permutation result = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestWideLabelArgument386(t *testing.T) {
	b := builder.NewBuilder()
	entry, done := b.NewLabel(), b.NewLabel()
	labelCheck386(t, b.Bind(entry))
	labelCheck386(t, b.JumpArgs(done, labelConst386(t, b, 64, 0x1122334455667788)))
	result, err := b.BindParamsBits(done, []uint8{64})
	labelCheck386(t, err)
	labelCheck386(t, b.Return(result[0]))
	if got := compileLabel386(t, b); got != 0x1122334455667788 {
		t.Fatalf("wide label result = %#x, want %#x", got, uint64(0x1122334455667788))
	}
}

func TestWideLabelArgumentsSwap386(t *testing.T) {
	b := builder.NewBuilder()
	entry, loop, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
	labelCheck386(t, b.Bind(entry))
	labelCheck386(t, b.JumpArgs(loop,
		labelConst386(t, b, 32, 1),
		labelConst386(t, b, 64, 0x1122334455667788),
		labelConst386(t, b, 64, 0x99aabbccddeeff00)))
	values, err := b.BindParamsBits(loop, []uint8{32, 64, 64})
	labelCheck386(t, err)
	zero, err := b.IsZero(values[0])
	labelCheck386(t, err)
	nextCount, err := b.Sub(values[0], labelConst386(t, b, 32, 1))
	labelCheck386(t, err)
	labelCheck386(t, b.BranchArgs(zero, done, []builder.Value{values[1]}, loop,
		[]builder.Value{nextCount, values[2], values[1]}))
	result, err := b.BindParamsBits(done, []uint8{64})
	labelCheck386(t, err)
	labelCheck386(t, b.Return(result[0]))
	if got := compileLabel386(t, b); got != 0x99aabbccddeeff00 {
		t.Fatalf("swapped wide label result = %#x", got)
	}
}

func TestComposite128LabelArgument386(t *testing.T) {
	b := builder.NewBuilder()
	entry, done := b.NewLabel(), b.NewLabel()
	labelCheck386(t, b.Bind(entry))
	wide, err := b.Concat(
		labelConst386(t, b, 64, 0x1122334455667788),
		labelConst386(t, b, 64, 0x99aabbccddeeff00))
	labelCheck386(t, err)
	labelCheck386(t, b.JumpArgs(done, wide))
	results, err := b.BindParamsBits(done, []uint8{128})
	labelCheck386(t, err)
	low, err := b.ExtractBits(results[0], 0, 64)
	labelCheck386(t, err)
	labelCheck386(t, b.Return(low))
	if got := compileLabel386(t, b); got != 0x1122334455667788 {
		t.Fatalf("128-bit label low word = %#x", got)
	}
}

func TestNarrowLabelArguments386(t *testing.T) {
	for _, width := range []uint8{8, 16} {
		b := builder.NewBuilder()
		entry, done := b.NewLabel(), b.NewLabel()
		labelCheck386(t, b.Bind(entry))
		labelCheck386(t, b.JumpArgs(done, labelConst386(t, b, width, 7)))
		result, err := b.BindParamsBits(done, []uint8{width})
		labelCheck386(t, err)
		labelCheck386(t, b.Return(result[0]))
		if got := compileLabel386(t, b); got != 7 {
			t.Fatalf("%d-bit label result = %d, want 7", width, got)
		}
	}
}
