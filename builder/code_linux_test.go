//go:build linux && (arm64 || amd64 || riscv64)

package builder

import (
	"errors"
	"math"
	"testing"
)

func TestNewReturnCallAndClose(t *testing.T) {
	const want = uint64(math.MaxUint64 - 41)
	code, err := testNewReturn(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := code.Call()
	if err != nil {
		t.Fatal(err)
	}
	if uint64(got) != want {
		t.Fatalf("Call() = %d, want %d", got, want)
	}
	if entry, err := code.EntryAddress(); err != nil || entry == 0 {
		t.Fatalf("EntryAddress() = %#x, %v", entry, err)
	}
	if err := code.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := code.EntryAddress(); !errors.Is(err, ErrClosed) {
		t.Fatalf("EntryAddress() after Close() error = %v, want %v", err, ErrClosed)
	}
	if _, err := code.Call(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Call() after Close() error = %v, want %v", err, ErrClosed)
	}
}

func TestNewAddCall(t *testing.T) {
	code, err := testNewAdd()
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()

	got, err := code.Call(20, 22)
	if err != nil {
		t.Fatal(err)
	}
	if got != 42 {
		t.Fatalf("Call(20, 22) = %d, want 42", got)
	}

	got, err = code.Call(math.MaxUint64, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("Call(MaxUint64, 1) = %d, want wrapping result 0", got)
	}
	if _, err := code.Call(1); !errors.Is(err, ErrArity) {
		t.Fatalf("Call(1) error = %v, want %v", err, ErrArity)
	}
}

func TestBuilderComposesScalarOperations(t *testing.T) {
	b := NewBuilder()
	x, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	one, err := b.Const(1)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := b.Add(x, one)
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

	got, err := code.Call(41)
	if err != nil {
		t.Fatal(err)
	}
	if got != 42 {
		t.Fatalf("Call(41) = %d, want 42", got)
	}
}

func TestBuilderRejectsInvalidAndOversizedFunctions(t *testing.T) {
	b := NewBuilder()
	if _, err := testCompile(b); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("Compile() without Return error = %v, want %v", err, ErrInvalidFunction)
	}
	for range backendMaxArgs {
		if _, err := b.Param(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Param(); !errors.Is(err, ErrBackendLimit) {
		t.Fatalf("Param() past the backend limit error = %v, want %v", err, ErrBackendLimit)
	}
}

func TestCallPassesMaximumIntegerArguments(t *testing.T) {
	b := NewBuilder()
	var last Value
	for range backendMaxArgs {
		value, err := b.Param()
		if err != nil {
			t.Fatal(err)
		}
		last = value
	}
	if err := b.Return(last); err != nil {
		t.Fatal(err)
	}
	code, err := testCompile(b)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()

	args := make([]uintptr, backendMaxArgs)
	for i := range args {
		args[i] = uintptr((i + 1) * 10)
	}
	got, err := code.Call(args...)
	if err != nil {
		t.Fatal(err)
	}
	want := uint64(backendMaxArgs * 10)
	if uint64(got) != want {
		t.Fatalf("Call(%d args) = %d, want %d", backendMaxArgs, got, want)
	}
}

func TestBuilderIntegerOperations(t *testing.T) {
	tests := []struct {
		name  string
		build func(*Builder, Value, Value) (Value, error)
		x, y  uint64
		want  uint64
	}{
		{name: "sub", build: (*Builder).Sub, x: 9, y: 4, want: 5},
		{name: "mul-wrap", build: (*Builder).Mul, x: math.MaxUint64, y: 2, want: math.MaxUint64 - 1},
		{name: "and", build: (*Builder).And, x: 0b1100, y: 0b1010, want: 0b1000},
		{name: "or", build: (*Builder).Or, x: 0b1100, y: 0b1010, want: 0b1110},
		{name: "xor", build: (*Builder).Xor, x: 0b1100, y: 0b1010, want: 0b0110},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewBuilder()
			x, err := b.Const(tt.x)
			if err != nil {
				t.Fatal(err)
			}
			y, err := b.Const(tt.y)
			if err != nil {
				t.Fatal(err)
			}
			result, err := tt.build(b, x, y)
			if err != nil {
				t.Fatal(err)
			}
			if err := b.Return(result); err != nil {
				t.Fatal(err)
			}
			code, err := testCompile(b)
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			got, err := code.Call()
			if err != nil {
				t.Fatal(err)
			}
			if uint64(got) != tt.want {
				t.Fatalf("got %#x, want %#x", got, tt.want)
			}
		})
	}
}

func TestBuilderAddScaled(t *testing.T) {
	for _, tc := range []struct {
		name               string
		base, index, scale uint64
	}{
		{name: "zero scale", base: 19, index: 7, scale: 0},
		{name: "unit scale", base: 19, index: 7, scale: 1},
		{name: "address scale two", base: 19, index: 7, scale: 2},
		{name: "address scale four", base: 19, index: 7, scale: 4},
		{name: "address scale eight", base: 19, index: 7, scale: 8},
		{name: "general scale", base: 19, index: 7, scale: 3},
		{name: "large scale", base: 19, index: 7, scale: 0x100000001},
		{name: "wraparound", base: math.MaxUint64 - 2, index: 3, scale: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBuilder()
			base, err := b.Param()
			if err != nil {
				t.Fatal(err)
			}
			index, err := b.Param()
			if err != nil {
				t.Fatal(err)
			}
			result := base.Add(index.MulConst(tc.scale))
			if err := b.Return(result); err != nil {
				t.Fatal(err)
			}
			code, err := testCompile(b)
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			got, err := code.Call(uintptr(tc.base), uintptr(tc.index))
			if err != nil {
				t.Fatal(err)
			}
			if want := tc.base + tc.index*tc.scale; uint64(got) != want {
				t.Fatalf("scaled expression (%#x, %#x, %#x) = %#x, want %#x", tc.base, tc.index, tc.scale, got, want)
			}
		})
	}
}

func TestBuilderSelect(t *testing.T) {
	b := NewBuilder()
	condition, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	ifTrue, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	ifFalse, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.Select(condition, ifTrue, ifFalse)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(result); err != nil {
		t.Fatal(err)
	}
	code, err := testCompile(b)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()

	for _, tt := range []struct {
		condition uint64
		want      uint64
	}{{condition: 0, want: 17}, {condition: 1, want: 42}, {condition: math.MaxUint64, want: 42}} {
		got, err := code.Call(uintptr(tt.condition), 42, 17)
		if err != nil {
			t.Fatal(err)
		}
		if uint64(got) != tt.want {
			t.Fatalf("Select condition %#x = %d, want %d", tt.condition, got, tt.want)
		}
	}
}
