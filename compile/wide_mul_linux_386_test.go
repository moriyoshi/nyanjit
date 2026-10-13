//go:build linux && 386

package compile

import (
	"errors"
	"math/rand"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/execution"
)

func TestWideMultiply386(t *testing.T) {
	for _, keepInput := range []bool{false, true} {
		b := builder.NewBuilder()
		a, err := b.ParamBits(64)
		if err != nil {
			t.Fatal(err)
		}
		c, err := b.ParamBits(64)
		if err != nil {
			t.Fatal(err)
		}
		product, err := b.Mul(a, c)
		if err != nil {
			t.Fatal(err)
		}
		if keepInput {
			product, err = b.Add(product, a)
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := b.Return(product); err != nil {
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
		assembly, err := EmitAssembly(program, CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536})
		if err != nil || !strings.Contains(assembly, "mul EBX") {
			t.Fatalf("wide multiply assembly missing MUL: %v\n%s", err, assembly)
		}
		pairs := [][2]uint64{{0, 0}, {1, 1}, {0xffffffffffffffff, 2},
			{0x12345678ffffffff, 0x9abcdef000000002},
			{0x8000000000000000, 0xffffffffffffffff}}
		rng := rand.New(rand.NewSource(386))
		for range 100 {
			pairs = append(pairs, [2]uint64{rng.Uint64(), rng.Uint64()})
		}
		for _, pair := range pairs {
			lo, hi, err := code.CallPair(execution.Words("LL", pair[0], pair[1])...)
			if err != nil {
				t.Fatal(err)
			}
			got := execution.Wide(lo, hi)
			want := pair[0] * pair[1]
			if keepInput {
				want += pair[0]
			}
			if got != want {
				t.Fatalf("keep input %v, %#x * %#x: got %#x, want %#x", keepInput, pair[0], pair[1], got, want)
			}
		}
		if !keepInput {
			if _, err := Compile(program, CompileOptions{ABI: ABIC, Stack: StackNoUse}); !errors.Is(err, ErrStackRequired) {
				t.Fatalf("stackless wide multiply = %v, want ErrStackRequired", err)
			}
		}
	}
}

func TestWideMultiplySharedAndLiveValues386(t *testing.T) {
	for _, shared := range []bool{false, true} {
		b := builder.NewBuilder()
		a, err := b.ParamBits(64)
		if err != nil {
			t.Fatal(err)
		}
		c, err := b.ParamBits(64)
		if err != nil {
			t.Fatal(err)
		}
		live, err := b.Xor(a, c)
		if err != nil {
			t.Fatal(err)
		}
		right := c
		if shared {
			right = a
		}
		product, err := b.Mul(a, right)
		if err != nil {
			t.Fatal(err)
		}
		result, err := b.Add(product, live)
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
		for _, pair := range [][2]uint64{{3, 7}, {0x123456789abcdef0, 0xfedcba9876543210}, {^uint64(0), 42}} {
			lo, hi, err := code.CallPair(execution.Words("LL", pair[0], pair[1])...)
			if err != nil {
				t.Fatal(err)
			}
			got := execution.Wide(lo, hi)
			rightValue := pair[1]
			if shared {
				rightValue = pair[0]
			}
			want := pair[0]*rightValue + (pair[0] ^ pair[1])
			if got != want {
				t.Fatalf("shared %v: got %#x, want %#x", shared, got, want)
			}
		}
		if err := code.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
