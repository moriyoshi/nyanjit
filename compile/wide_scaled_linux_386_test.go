//go:build linux && 386

package compile

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/builder"
)

func TestWideScaled386(t *testing.T) {
	for _, scale := range []uint64{0, 1, 2, 3, 4, 5, 8, 9, 13, 16, 32, 64, 1 << 31, 1 << 63, 0xffffffff, ^uint64(0)} {
		for _, keepBase := range []bool{false, true} {
			b := builder.NewBuilder()
			base, err := b.ParamBits(64)
			if err != nil {
				t.Fatal(err)
			}
			index, err := b.ParamBits(64)
			if err != nil {
				t.Fatal(err)
			}
			result := base.Add(index.MulConst(scale))
			if keepBase {
				result, err = b.Add(result, base)
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
			code, err := compileCode(program, TargetSpec{}, Func{ABI: abi.C, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
			if err != nil {
				t.Fatalf("scale %d keepBase %v: %v", scale, keepBase, err)
			}
			if scale == 32 || scale == 64 || scale == 1<<63 {
				assembly, err := lowerListing(program, TargetSpec{}, Func{ABI: abi.C, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
				if err != nil || !strings.Contains(assembly, "shl") || strings.Contains(assembly, "mul") {
					t.Fatalf("scale %d assembly used multiplication: %v\n%s", scale, err, assembly)
				}
			}
			if scale == ^uint64(0) {
				assembly, err := lowerListing(program, TargetSpec{}, Func{ABI: abi.C, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
				if err != nil || !strings.Contains(assembly, "sbb") || strings.Contains(assembly, "mul") {
					t.Fatalf("negative-one scale did not use subtraction: %v\n%s", err, assembly)
				}
			}
			pairs := [][2]uint64{{0, 0}, {1, 1}, {0xfffffffffffffffe, 3},
				{0x12345678ffffffff, 0x9abcdef000000002}}
			rng := rand.New(rand.NewSource(int64(scale) + 386))
			for range 32 {
				pairs = append(pairs, [2]uint64{rng.Uint64(), rng.Uint64()})
			}
			for _, pair := range pairs {
				lo, hi, err := code.CallPair(execution.Words(pair[0], pair[1])...)
				got := execution.Wide(lo, hi)
				want := pair[0] + pair[1]*scale
				if keepBase {
					want += pair[0]
				}
				if err != nil || got != want {
					t.Fatalf("scale %d keepBase %v, base %#x index %#x: got %#x, err %v, want %#x", scale, keepBase, pair[0], pair[1], got, err, want)
				}
			}
			if err := code.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}
