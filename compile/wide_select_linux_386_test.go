//go:build linux && 386

package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/execution"
)

func TestWideSelect386(t *testing.T) {
	for _, conditionBits := range []uint8{32, 64} {
		b := builder.NewBuilder()
		condition, err := b.ParamBits(conditionBits)
		if err != nil {
			t.Fatal(err)
		}
		ifTrue, err := b.ParamBits(64)
		if err != nil {
			t.Fatal(err)
		}
		ifFalse, err := b.ParamBits(64)
		if err != nil {
			t.Fatal(err)
		}
		selected, err := b.Select(condition, ifTrue, ifFalse)
		if err != nil {
			t.Fatal(err)
		}
		result, err := b.Add(selected, ifTrue)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Return(result); err != nil {
			t.Fatal(err)
		}
		program, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		code, err := Compile(program, CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536})
		if err != nil {
			t.Fatal(err)
		}
		for _, values := range [][3]uint64{
			{0, 0x1234567887654321, 0xfedcba9876543210},
			{1, 0x1234567887654321, 0xfedcba9876543210},
			{0x100000000, 0xffffffffffffffff, 7},
			{0x8000000000000000, 0x0102030405060708, 0x1122334455667788},
		} {
			args := execution.Words("LLL", values[:]...)
			if conditionBits == 32 {
				args = append([]uintptr{uintptr(values[0])}, execution.Words("LL", values[1], values[2])...)
			}
			lo, hi, err := code.CallPair(args...)
			if err != nil {
				t.Fatal(err)
			}
			got := execution.Wide(lo, hi)
			selectedValue := values[2]
			conditionValue := values[0]
			if conditionBits == 32 {
				conditionValue = uint64(uint32(conditionValue))
			}
			if conditionValue != 0 {
				selectedValue = values[1]
			}
			want := selectedValue + values[1]
			if got != want {
				t.Fatalf("conditionBits %d values %#v: got %#x, want %#x", conditionBits, values, got, want)
			}
		}
		if err := code.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWideConditionScalarSelect386(t *testing.T) {
	b := builder.NewBuilder()
	condition, err := b.ParamBits(64)
	if err != nil {
		t.Fatal(err)
	}
	ifTrue, err := b.ConstBits(32, 11)
	if err != nil {
		t.Fatal(err)
	}
	ifFalse, err := b.ConstBits(32, 22)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := b.Select(condition, ifTrue, ifFalse)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(selected); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := Compile(program, CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, tc := range []struct{ condition, want uint64 }{{0, 22}, {1, 11}, {0x100000000, 11}} {
		lo, hi, err := code.CallPair(execution.Words("L", tc.condition)...)
		if got := execution.Wide(lo, hi); err != nil || got != tc.want {
			t.Fatalf("condition %#x: got %#x, err %v, want %#x", tc.condition, got, err, tc.want)
		}
	}
}
