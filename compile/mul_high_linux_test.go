//go:build linux && (amd64 || 386 || arm64 || riscv64 || ppc64 || ppc64le)

package compile_test

import (
	"math/bits"
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/execution"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestUnsignedHighMultiplyWidthsAndAliasing(t *testing.T) {
	for _, width := range []uint8{8, 16, 32, 64} {
		for _, live := range []int{0, 1, 2, 3} {
			ops := []ir.Instruction{{Op: ir.OpParam, Bits: width}, {Op: ir.OpParam, Bits: width}, {Op: ir.OpMulHigh, Bits: width, A: 0, B: 1}}
			result := 2
			for i := 0; i < 2; i++ {
				if live&(1<<i) != 0 {
					ops = append(ops, ir.Instruction{Op: ir.OpXor, Bits: width, A: result, B: i})
					result = len(ops) - 1
				}
			}
			code, err := compile.Compile(&ir.Program{Instructions: ops, Arity: 2, Result: result}, abi.CompileOptions{Stack: abi.StackPlatform, StackBytesAvailable: 65536})
			if err != nil {
				t.Fatal(err)
			}
			mask := ^uint64(0) >> (64 - width)
			for _, a := range []uint64{0, 1, mask, 1 << (width - 1), 0x123456789abcdef} {
				for _, b := range []uint64{0, 1, mask, 1 << (width - 1), 0xfedcba9876543210} {
					x, y := a&mask, b&mask
					high, _ := bits.Mul64(x, y)
					if width < 64 {
						high = x * y >> width
					}
					if live&1 != 0 {
						high ^= x
					}
					if live&2 != 0 {
						high ^= y
					}
					args := []uintptr{uintptr(a), uintptr(b)}
					if width == 64 {
						args = execution.Words("LL", a, b)
					}
					lo, hi, err := code.CallPair(args...)
					got := uint64(lo)
					if width == 64 {
						got = execution.Wide(lo, hi)
					}
					if err != nil || got != high {
						t.Fatalf("width%d live%d (%#x,%#x): got %#x,%v; want %#x", width, live, a, b, got, err, high)
					}
				}
			}
			code.Close()
		}
	}
}
