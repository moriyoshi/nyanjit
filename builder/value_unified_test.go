//go:build linux && (amd64 || arm64)

package builder

import (
	"encoding/binary"
	"testing"
)

func testConstWide(b *Builder, low, high uint64) (Value, error) {
	var data [16]byte
	binary.LittleEndian.PutUint64(data[:8], low)
	binary.LittleEndian.PutUint64(data[8:], high)
	return b.ConstBytes(128, data[:])
}

func testWideParts(t *testing.T, b *Builder, value Value) (Value, Value) {
	t.Helper()
	low, err := b.ExtractBits(value, 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	high, err := b.ExtractBits(value, 64, 64)
	if err != nil {
		t.Fatal(err)
	}
	return low, high
}

func TestWideValueScopeAndLabels(t *testing.T) {
	b := NewBuilder()
	entry, exit := b.NewLabel(), b.NewLabel()
	if err := b.Bind(entry); err != nil {
		t.Fatal(err)
	}
	if err := b.Enter(); err != nil {
		t.Fatal(err)
	}
	x, err := testConstWide(b, ^uint64(0), 7)
	if err != nil {
		t.Fatal(err)
	}
	if x.Bits() != 128 {
		t.Fatalf("width = %d", x.Bits())
	}
	if err := b.Leave(x); err != nil {
		t.Fatal(err)
	}
	one, err := b.ConstBits(128, 1)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := b.Add(x, one)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.JumpArgs(exit, sum); err != nil {
		t.Fatal(err)
	}
	params, err := b.BindParamsBits(exit, []uint8{128})
	if err != nil {
		t.Fatal(err)
	}
	if len(params) != 1 || params[0].Bits() != 128 {
		t.Fatalf("label params = %v", params)
	}
	_, high := testWideParts(t, b, params[0])
	if err := b.Return(high); err != nil {
		t.Fatal(err)
	}
	if _, err := testEmitAssembly(b, CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536}); err != nil {
		t.Fatal(err)
	}
}

func TestWideValueGenericOperations(t *testing.T) {
	b := NewBuilder()
	x, err := b.ParamBits(128)
	if err != nil {
		t.Fatal(err)
	}
	y, err := testConstWide(b, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := b.Add(x, y)
	if err != nil {
		t.Fatal(err)
	}
	shifted, err := b.ShiftLeft(sum, 65)
	if err != nil {
		t.Fatal(err)
	}
	count, err := b.Const(1)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := b.RotateRightBy(shifted, count)
	if err != nil {
		t.Fatal(err)
	}
	cond, err := b.Const(1)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := b.Select(cond, rotated, x)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Bits() != 128 {
		t.Fatalf("selected width = %d", selected.Bits())
	}
	if _, err := b.Compare(selected, BranchAbove, x); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CompareImmediate(selected, BranchLess, -1); err != nil {
		t.Fatal(err)
	}
	if err := b.Return(selected); err != nil {
		t.Fatal(err)
	}
	if _, err := testEmitAssembly(b, CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536}); err != nil {
		t.Fatal(err)
	}
}

func TestWideValueBranchArguments(t *testing.T) {
	b := NewBuilder()
	entry, yes, no := b.NewLabel(), b.NewLabel(), b.NewLabel()
	if err := b.Bind(entry); err != nil {
		t.Fatal(err)
	}
	input, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	wide, err := testConstWide(b, 3, 5)
	if err != nil {
		t.Fatal(err)
	}
	predicate, err := b.CompareImmediate(input, BranchEqual, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.BranchArgs(predicate, yes, []Value{wide}, no, []Value{wide}); err != nil {
		t.Fatal(err)
	}
	yesArgs, err := b.BindParamsBits(yes, []uint8{128})
	if err != nil {
		t.Fatal(err)
	}
	_, high := testWideParts(t, b, yesArgs[0])
	if err := b.Return(high); err != nil {
		t.Fatal(err)
	}
	noArgs, err := b.BindParamsBits(no, []uint8{128})
	if err != nil {
		t.Fatal(err)
	}
	low, _ := testWideParts(t, b, noArgs[0])
	if err := b.Return(low); err != nil {
		t.Fatal(err)
	}
	code, err := testCompileWithOptions(b, CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, tc := range []struct{ in, want uint64 }{{0, 5}, {1, 3}} {
		got, err := code.Call(uintptr(tc.in))
		if err != nil || uint64(got) != tc.want {
			t.Fatalf("input %d: got %d, %v; want %d", tc.in, got, err, tc.want)
		}
	}
}

func TestConcatAndExtractAcrossComponentBoundary(t *testing.T) {
	b := NewBuilder()
	first, err := b.ConstBits(32, 0x11223344)
	if err != nil {
		t.Fatal(err)
	}
	middle, err := b.Const(0x5566778899aabbcc)
	if err != nil {
		t.Fatal(err)
	}
	last, err := b.ConstBits(32, 0xddeeff00)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := b.Concat(first, middle, last)
	if err != nil {
		t.Fatal(err)
	}
	if joined.Bits() != 128 {
		t.Fatalf("joined width = %d", joined.Bits())
	}
	sliced, err := b.ExtractBits(joined, 48, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(sliced); err != nil {
		t.Fatal(err)
	}
	code, err := testCompile(b)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	got, err := code.Call()
	if err != nil || got != 0xff005566778899aa {
		t.Fatalf("extracted bits = %#x, %v; want %#x", got, err, uint64(0xff005566778899aa))
	}
}
