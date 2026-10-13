//go:build linux && (amd64 || arm64 || riscv64)

package builder

import (
	"encoding/binary"
	"math/bits"
	"strconv"
	"testing"
)

func TestNarrowIntegerWidths(t *testing.T) {
	for _, width := range []uint8{8, 16, 32} {
		t.Run(strconv.Itoa(int(width)), func(t *testing.T) {
			b := NewBuilder()
			x, err := b.ParamBits(width)
			if err != nil {
				t.Fatal(err)
			}
			one, err := b.ConstBits(width, 1)
			if err != nil {
				t.Fatal(err)
			}
			sum, err := b.Add(x, one)
			if err != nil {
				t.Fatal(err)
			}
			if sum.Bits() != uint16(width) {
				t.Fatalf("sum width = %d, want %d", sum.Bits(), width)
			}
			if err := b.Return(sum); err != nil {
				t.Fatal(err)
			}
			code, err := testCompile(b)
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			for _, input := range []uint64{0, integerMask(width), ^uint64(0)} {
				got, err := code.Call(uintptr(input))
				if err != nil {
					t.Fatal(err)
				}
				want := (input + 1) & integerMask(width)
				if uint64(got) != want {
					t.Fatalf("width %d, input %#x: got %#x, want %#x", width, input, got, want)
				}
			}
		})
	}
}

func TestValue128NativeMemory(t *testing.T) {
	memory, address := mappedTestMemory(t)
	b := NewBuilder()
	base, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	value, err := b.ParamBits(128)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.StoreBits(base, value); err != nil {
		t.Fatal(err)
	}
	loaded, err := b.LoadBits(base, 128)
	if err != nil {
		t.Fatal(err)
	}
	_, high := testWideParts(t, b, loaded)
	if err := b.Return(high); err != nil {
		t.Fatal(err)
	}
	code, err := testCompile(b)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	const lowWord, highWord = uint64(0x123456789abcdef0), uint64(0xfedcba9876543210)
	got, err := code.Call(address, uintptr(lowWord), uintptr(highWord))
	if err != nil || uint64(got) != highWord || binary.LittleEndian.Uint64(memory[:8]) != lowWord || binary.LittleEndian.Uint64(memory[8:16]) != highWord {
		t.Fatalf("128-bit memory result = %#x, %v; words %#x %#x", got, err,
			binary.LittleEndian.Uint64(memory[:8]), binary.LittleEndian.Uint64(memory[8:16]))
	}
}

func TestTypedLabelParameter(t *testing.T) {
	b := NewBuilder()
	entry, exit := b.NewLabel(), b.NewLabel()
	if err := b.Bind(entry); err != nil {
		t.Fatal(err)
	}
	input, err := b.ParamBits(16)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.JumpArgs(exit, input); err != nil {
		t.Fatal(err)
	}
	parameters, err := b.BindParamsBits(exit, []uint8{16})
	if err != nil {
		t.Fatal(err)
	}
	one, err := b.ConstBits(16, 1)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := b.Add(parameters[0], one)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(sum); err != nil {
		t.Fatal(err)
	}
	code, err := testCompileWithOptions(b, CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	got, err := code.Call(0xffff)
	if err != nil || got != 0 {
		t.Fatalf("typed label result = %#x, %v; want 0", got, err)
	}
}

func TestTypedNativeMemory(t *testing.T) {
	memory, address := mappedTestMemory(t)
	b := NewBuilder()
	base, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	value, err := b.ParamBits(16)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.StoreBits(base, value); err != nil {
		t.Fatal(err)
	}
	loaded, err := b.LoadBits(base, 16)
	if err != nil {
		t.Fatal(err)
	}
	widened, err := b.CastUnsigned(loaded, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(widened); err != nil {
		t.Fatal(err)
	}
	code, err := testCompile(b)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	got, err := code.Call(address, 0x12345)
	if err != nil || got != 0x2345 || memory[0] != 0x45 || memory[1] != 0x23 {
		t.Fatalf("typed memory result = %#x, %v; bytes %#x %#x", got, err, memory[0], memory[1])
	}
}

func TestNarrowViewDoesNotOverwriteLiveWideValue(t *testing.T) {
	b := NewBuilder()
	wide, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	short, err := b.CastUnsigned(wide, 8)
	if err != nil {
		t.Fatal(err)
	}
	widened, err := b.CastUnsigned(short, 64)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := b.Add(wide, widened)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(sum); err != nil {
		t.Fatal(err)
	}
	code, err := testCompile(b)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	const input = uint64(0x123456789abcdef0)
	got, err := code.Call(uintptr(input))
	if err != nil {
		t.Fatal(err)
	}
	if want := input + 0xf0; uint64(got) != want {
		t.Fatalf("got %#x, want %#x", got, want)
	}
}

func TestSignedNarrowPredicate(t *testing.T) {
	b := NewBuilder()
	entry, negative, nonnegative := b.NewLabel(), b.NewLabel(), b.NewLabel()
	if err := b.Bind(entry); err != nil {
		t.Fatal(err)
	}
	value, err := b.ParamBits(8)
	if err != nil {
		t.Fatal(err)
	}
	predicate, err := b.CompareImmediate(value, BranchLess, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Branch(predicate, negative, nonnegative); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(negative); err != nil {
		t.Fatal(err)
	}
	one, err := b.Const(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(one); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(nonnegative); err != nil {
		t.Fatal(err)
	}
	zero, err := b.Const(0)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(zero); err != nil {
		t.Fatal(err)
	}
	code, err := testCompileWithOptions(b, CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, sample := range []struct{ in, want uint64 }{{0xff, 1}, {0x80, 1}, {0x7f, 0}, {0x100, 0}} {
		got, err := code.Call(uintptr(sample.in))
		if err != nil {
			t.Fatal(err)
		}
		if uint64(got) != sample.want {
			t.Fatalf("input %#x: got %d, want %d", sample.in, got, sample.want)
		}
	}
}

func TestValue128Arithmetic(t *testing.T) {
	build := func(operation string, high bool) *Code {
		t.Helper()
		b := NewBuilder()
		x, err := b.ParamBits(128)
		if err != nil {
			t.Fatal(err)
		}
		y, err := b.ParamBits(128)
		if err != nil {
			t.Fatal(err)
		}
		var result Value
		switch operation {
		case "add":
			result, err = b.Add(x, y)
		case "sub":
			result, err = b.Sub(x, y)
		case "mul":
			result, err = b.Mul(x, y)
		}
		if err != nil {
			t.Fatal(err)
		}
		lo, hi := testWideParts(t, b, result)
		value := lo
		if high {
			value = hi
		}
		if err := b.Return(value); err != nil {
			t.Fatal(err)
		}
		code, err := testCompile(b)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = code.Close() })
		return code
	}
	for _, operation := range []string{"add", "sub", "mul"} {
		lowCode, highCode := build(operation, false), build(operation, true)
		pairs := [][4]uint64{
			{0, 0, 0, 0},
			{^uint64(0), 0, 1, 0},
			{0, 1, 1, 0},
			{0xdeadbeef12345678, 0x9182736455463728, 0xfedcba9876543210, 0x1020304050607080},
		}
		seed := uint64(0x9e3779b97f4a7c15)
		for range 32 {
			var pair [4]uint64
			for i := range pair {
				seed = seed*6364136223846793005 + 1442695040888963407
				pair[i] = seed
			}
			pairs = append(pairs, pair)
		}
		for _, pair := range pairs {
			aLo, aHi, bLo, bHi := pair[0], pair[1], pair[2], pair[3]
			var wantLo, wantHi uint64
			switch operation {
			case "add":
				var carry uint64
				wantLo, carry = bits.Add64(aLo, bLo, 0)
				wantHi, _ = bits.Add64(aHi, bHi, carry)
			case "sub":
				var borrow uint64
				wantLo, borrow = bits.Sub64(aLo, bLo, 0)
				wantHi, _ = bits.Sub64(aHi, bHi, borrow)
			case "mul":
				wantHi, wantLo = bits.Mul64(aLo, bLo)
				wantHi += aHi*bLo + aLo*bHi
			}
			gotLo, err := lowCode.Call(uintptr(aLo), uintptr(aHi), uintptr(bLo), uintptr(bHi))
			if err != nil {
				t.Fatal(err)
			}
			gotHi, err := highCode.Call(uintptr(aLo), uintptr(aHi), uintptr(bLo), uintptr(bHi))
			if err != nil {
				t.Fatal(err)
			}
			if uint64(gotLo) != wantLo || uint64(gotHi) != wantHi {
				t.Fatalf("%s(%#x:%#x, %#x:%#x) = %#x:%#x, want %#x:%#x", operation, aHi, aLo, bHi, bLo, gotHi, gotLo, wantHi, wantLo)
			}
		}
	}
}

func TestValue128ShiftsAndComparison(t *testing.T) {
	compileShift := func(left bool, count uint8, high bool) *Code {
		t.Helper()
		b := NewBuilder()
		value, err := b.ParamBits(128)
		if err != nil {
			t.Fatal(err)
		}
		var shifted Value
		if left {
			shifted, err = b.ShiftLeft(value, count)
		} else {
			shifted, err = b.ShiftRight(value, count)
		}
		if err != nil {
			t.Fatal(err)
		}
		lo, hi := testWideParts(t, b, shifted)
		result := lo
		if high {
			result = hi
		}
		if err := b.Return(result); err != nil {
			t.Fatal(err)
		}
		code, err := testCompile(b)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = code.Close() })
		return code
	}
	lo, hi := uint64(0x99aabbccddeeff00), uint64(0x1122334455667788)
	for _, tc := range []struct {
		left           bool
		count          uint8
		wantLo, wantHi uint64
	}{
		{false, 4, lo>>4 | hi<<60, hi >> 4},
		{false, 64, hi, 0},
		{true, 4, lo << 4, hi<<4 | lo>>60},
		{true, 64, 0, lo},
	} {
		lowCode, highCode := compileShift(tc.left, tc.count, false), compileShift(tc.left, tc.count, true)
		gotLo, err := lowCode.Call(uintptr(lo), uintptr(hi))
		if err != nil {
			t.Fatal(err)
		}
		gotHi, err := highCode.Call(uintptr(lo), uintptr(hi))
		if err != nil {
			t.Fatal(err)
		}
		if uint64(gotLo) != tc.wantLo || uint64(gotHi) != tc.wantHi {
			t.Fatalf("shift left=%v count=%d: got %#x:%#x, want %#x:%#x", tc.left, tc.count, gotHi, gotLo, tc.wantHi, tc.wantLo)
		}
	}
	buildCompare := func(condition BranchCondition) *Code {
		t.Helper()
		b := NewBuilder()
		entry, yes, no := b.NewLabel(), b.NewLabel(), b.NewLabel()
		if err := b.Bind(entry); err != nil {
			t.Fatal(err)
		}
		x, err := b.ParamBits(128)
		if err != nil {
			t.Fatal(err)
		}
		y, err := b.ParamBits(128)
		if err != nil {
			t.Fatal(err)
		}
		predicate, err := b.Compare(x, condition, y)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Branch(predicate, yes, no); err != nil {
			t.Fatal(err)
		}
		if err := b.Bind(yes); err != nil {
			t.Fatal(err)
		}
		one, err := b.Const(1)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Return(one); err != nil {
			t.Fatal(err)
		}
		if err := b.Bind(no); err != nil {
			t.Fatal(err)
		}
		zero, err := b.Const(0)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Return(zero); err != nil {
			t.Fatal(err)
		}
		code, err := testCompileWithOptions(b, CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = code.Close() })
		return code
	}
	signed, unsigned := buildCompare(BranchLess), buildCompare(BranchBelow)
	for _, tc := range []struct {
		args                     []uintptr
		wantSigned, wantUnsigned uint64
	}{
		{[]uintptr{0, ^uintptr(0), 0, 0}, 1, 0},
		{[]uintptr{1, 0, 2, 0}, 1, 1},
		{[]uintptr{3, 0, 2, 0}, 0, 0},
	} {
		gotSigned, err := signed.Call(tc.args...)
		if err != nil {
			t.Fatal(err)
		}
		gotUnsigned, err := unsigned.Call(tc.args...)
		if err != nil {
			t.Fatal(err)
		}
		if uint64(gotSigned) != tc.wantSigned || uint64(gotUnsigned) != tc.wantUnsigned {
			t.Fatalf("Compare128(%v) signed=%d unsigned=%d, want %d %d", tc.args, gotSigned, gotUnsigned, tc.wantSigned, tc.wantUnsigned)
		}
	}
}
