//go:build linux && (amd64 || 386 || arm64 || riscv64 || ppc64 || ppc64le)

package compile_test

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/execution"
	"github.com/moriyoshi/nyanjit/ir"
	"math/bits"
	"testing"
)

func TestWordCarryWidthsAndFullCarryOperands(t *testing.T) {
	for _, width := range []uint8{8, 16, 32, 64} {
		mask := ^uint64(0) >> (64 - width)
		for _, op := range []ir.Opcode{ir.OpCarryAdd, ir.OpBorrowSub, ir.OpAddWithCarry, ir.OpSubWithBorrow} {
			arity := 2
			ops := []ir.Instruction{{Op: ir.OpParam, Bits: width}, {Op: ir.OpParam, Bits: width}}
			if op == ir.OpAddWithCarry || op == ir.OpSubWithBorrow {
				ops = append(ops, ir.Instruction{Op: ir.OpParam, Bits: width})
				arity = 3
			}
			ops = append(ops, ir.Instruction{Op: op, Bits: width, A: 0, B: 1, C: 2})
			code, err := compile.Compile(&ir.Program{Instructions: ops, Arity: arity, Result: len(ops) - 1}, abi.CompileOptions{Stack: abi.StackPlatform, StackBytesAvailable: 65536})
			if err != nil {
				t.Fatal(err)
			}
			for _, a := range []uint64{0, 1, mask, 1 << (width - 1), 0x8123456789abcdef} {
				for _, b := range []uint64{0, 1, mask, 1 << (width - 1), 0x123456789abcdef0} {
					for _, c := range []uint64{0, 1, 2, 3, mask, 1 << 63} {
						x, y := a&mask, b&mask
						want := uint64(0)
						switch op {
						case ir.OpCarryAdd:
							if width == 64 {
								_, want = bits.Add64(x, y, 0)
							} else {
								want = (x + y) >> width
							}
						case ir.OpBorrowSub:
							if x < y {
								want = 1
							}
						case ir.OpAddWithCarry:
							want = (x + y + (c & 1)) & mask
						case ir.OpSubWithBorrow:
							want = (x - y - (c & 1)) & mask
						}
						values := []uint64{a, b}
						if arity == 3 {
							values = append(values, c)
						}
						var args []uintptr
						for _, v := range values {
							if width == 64 {
								args = append(args, execution.Words("L", v)...)
							} else {
								args = append(args, uintptr(v))
							}
						}
						lo, hi, err := code.CallPair(args...)
						got := uint64(lo)
						if width == 64 {
							got = execution.Wide(lo, hi)
						}
						if err != nil || got != want {
							t.Fatalf("width%d opcode%d a%#x b%#x c%#x: got%#x,%v;want%#x", width, op, a, b, c, got, err, want)
						}
					}
				}
			}
			code.Close()
		}
	}
}
