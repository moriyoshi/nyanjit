//go:build linux && 386

package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/execution"
)

func TestUnsignedWiden386(t *testing.T) {
	for _, tc := range []struct {
		width uint8
		input uint64
		want  uint64
	}{
		{8, 0x180, 0x80},
		{16, 0x18000, 0x8000},
		{32, 0x180000000, 0x80000000},
	} {
		for _, keepInput := range []bool{false, true} {
			b := builder.NewBuilder()
			input, err := b.ParamBits(tc.width)
			if err != nil {
				t.Fatal(err)
			}
			widened, err := b.CastUnsigned(input, 64)
			if err != nil {
				t.Fatal(err)
			}
			if keepInput {
				again, err := b.CastUnsigned(input, 64)
				if err != nil {
					t.Fatal(err)
				}
				widened, err = b.Add(widened, again)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := b.Return(widened); err != nil {
				t.Fatal(err)
			}
			program, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			stack, budget := StackNoUse, 0
			if keepInput {
				stack, budget = StackPlatform, 65536
			}
			code, err := Compile(program, CompileOptions{ABI: ABIC, Stack: stack, StackBytesAvailable: budget})
			if err != nil {
				t.Fatal(err)
			}
			lo, hi, err := code.CallPair(uintptr(tc.input))
			want := tc.want
			if keepInput {
				want *= 2
			}
			if got := execution.Wide(lo, hi); err != nil || got != want {
				t.Fatalf("CastUnsigned(%#x, %d), keep %v = %#x, %v; want %#x", tc.input, tc.width, keepInput, got, err, want)
			}
			if err := code.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestSignExtend386(t *testing.T) {
	for _, tc := range []struct {
		width uint8
		input uint64
		want  uint64
	}{
		{8, 0x7f, 0x7f}, {8, 0x180, 0xffffffffffffff80},
		{16, 0x7fff, 0x7fff}, {16, 0x18000, 0xffffffffffff8000},
		{32, 0x7fffffff, 0x7fffffff}, {32, 0x180000000, 0xffffffff80000000},
	} {
		for _, keepInput := range []bool{false, true} {
			b := builder.NewBuilder()
			input, err := b.ParamBits(64)
			if err != nil {
				t.Fatal(err)
			}
			result, err := b.SignExtend(input, tc.width)
			if err != nil {
				t.Fatal(err)
			}
			if keepInput {
				result, err = b.Add(result, input)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := b.Return(result); err != nil {
				t.Fatal(err)
			}
			program, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			stack := StackNoUse
			budget := 0
			if keepInput {
				stack, budget = StackPlatform, 65536
			}
			code, err := Compile(program, CompileOptions{ABI: ABIC, Stack: stack, StackBytesAvailable: budget})
			if err != nil {
				t.Fatal(err)
			}
			lo, hi, err := code.CallPair(execution.Words("L", tc.input)...)
			want := tc.want
			if keepInput {
				want += tc.input
			}
			if got := execution.Wide(lo, hi); err != nil || got != want {
				t.Fatalf("SignExtend(%#x, %d), keep %v = %#x, %v; want %#x", tc.input, tc.width, keepInput, got, err, want)
			}
			if err := code.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestSignExtendNarrowSource386(t *testing.T) {
	for _, tc := range []struct {
		width uint8
		input uint64
		want  uint64
	}{{8, 0x80, 0xffffffffffffff80}, {16, 0x8000, 0xffffffffffff8000}, {32, 0x80000000, 0xffffffff80000000}} {
		b := builder.NewBuilder()
		input, err := b.ParamBits(tc.width)
		if err != nil {
			t.Fatal(err)
		}
		result, err := b.SignExtend(input, tc.width)
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
		code, err := Compile(program, CompileOptions{ABI: ABIC, Stack: StackNoUse})
		if err != nil {
			t.Fatal(err)
		}
		lo, hi, err := code.CallPair(uintptr(tc.input))
		if got := execution.Wide(lo, hi); err != nil || got != tc.want {
			t.Fatalf("SignExtend narrow %d = %#x, %v; want %#x", tc.width, got, err, tc.want)
		}
		if err := code.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
