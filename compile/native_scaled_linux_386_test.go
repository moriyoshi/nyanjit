//go:build linux && 386

package compile

import (
	"strconv"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/builder"
)

func TestNativeScaledAddress386(t *testing.T) {
	for _, tc := range []struct {
		scale       uint64
		base, index uintptr
		want        uint64
	}{
		{1, 100, 7, 107},
		{2, 100, 7, 114},
		{4, 100, 7, 128},
		{8, 100, 7, 156},
		{9, 100, 7, 163},
		{13, 100, 7, 191},
		{8, uintptr(^uint32(0) - 3), 2, 12},
		{1 << 32, 123, 456, 123},
		{1<<32 + 2, 123, 456, 1035},
		{0xffffffff, 123, 456, 0xfffffeb3},
	} {
		t.Run(strconv.FormatUint(tc.scale, 10)+"_"+strconv.FormatUint(uint64(tc.base), 16), func(t *testing.T) {
			b := builder.NewBuilder()
			base, err := b.ConstNative(tc.base)
			if err != nil {
				t.Fatal(err)
			}
			index, err := b.ConstNative(tc.index)
			if err != nil {
				t.Fatal(err)
			}
			value := base.Add(index.MulConst(tc.scale))
			if err := b.Return(value); err != nil {
				t.Fatal(err)
			}
			program, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			code, err := compileCode(program, TargetSpec{}, Func{ABI: abi.C, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			lo, hi, err := code.CallPair()
			got := execution.Wide(lo, hi)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("scale %d: got %d, want %d", tc.scale, got, tc.want)
			}
		})
	}
}
