//go:build linux && (amd64 || 386 || arm64 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"fmt"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func variableShift(b *nyanjit.Builder, op ir.Opcode, value, count nyanjit.Value) nyanjit.Value {
	switch op {
	case ir.OpShiftLeftBy:
		return b.ShiftLeftBy(value, count)
	case ir.OpShiftRightBy:
		return b.ShiftRightBy(value, count)
	case ir.OpShiftRightArithmeticBy:
		return b.ShiftRightArithmeticBy(value, count)
	case ir.OpRotateLeftBy:
		return b.RotateLeftBy(value, count)
	case ir.OpRotateRightBy:
		return b.RotateRightBy(value, count)
	default:
		panic("unexpected shift")
	}
}

func variableShiftWant(op ir.Opcode, width uint8, value, count uint64) uint64 {
	mask := ^uint64(0) >> (64 - width)
	value &= mask
	if op == ir.OpRotateLeftBy || op == ir.OpRotateRightBy {
		count %= uint64(width)
	}
	switch op {
	case ir.OpShiftLeftBy:
		return value << count & mask
	case ir.OpShiftRightBy:
		return value >> count
	case ir.OpShiftRightArithmeticBy:
		return uint64((int64(value<<(64-width))>>(64-width))>>count) & mask
	case ir.OpRotateLeftBy:
		return (value<<count | value>>(uint64(width)-count)) & mask
	case ir.OpRotateRightBy:
		return (value>>count | value<<(uint64(width)-count)) & mask
	default:
		panic("unexpected shift")
	}
}

func TestVariableShiftWidthsCountsAndLiveInputs(t *testing.T) {
	for _, width := range []uint8{8, 16, 32, 64} {
		for _, countWidth := range []uint8{8, 16, 32, 64} {
			for _, op := range []ir.Opcode{ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy} {
				for _, live := range []int{0, 1, 2, 3} {
					t.Run(fmt.Sprintf("%d/count%d/op%d/live%d", width, countWidth, op, live), func(t *testing.T) {
						b := nyanjit.NewBuilder()
						value, count := b.ParamBits(width), b.ParamBits(countWidth)
						result := variableShift(b, op, value, count)
						if live&1 != 0 {
							result = b.Xor(result, value)
						}
						if live&2 != 0 {
							result = b.Xor(result, b.CastUnsigned(count, width))
						}
						b.Return(result)
						program, err := testFinishRaw(b)
						if err != nil {
							t.Fatal(err)
						}
						found := false
						for _, ins := range program.Instructions {
							found = found || ins.Op == op
						}
						if !found {
							t.Fatal("Builder expanded the variable shift before instruction selection")
						}
						code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
						if err != nil {
							t.Fatal(err)
						}
						defer code.Close()
						mask, countMask := ^uint64(0)>>(64-width), ^uint64(0)>>(64-countWidth)
						for _, input := range []uint64{0, 1, 1 << (width - 1), mask, 0x8123456789abcdef} {
							for _, c := range []uint64{0, 1, uint64(width - 1), uint64(width), uint64(width + 1), 127, 128, 255, 256, 1 << 32, 1 << 63, ^uint64(0)} {
								want := variableShiftWant(op, width, input, c&countMask)
								if live&1 != 0 {
									want ^= input & mask
								}
								if live&2 != 0 {
									want ^= c & countMask & mask
								}
								args := []uintptr{uintptr(input)}
								if width == 64 {
									args = execution.Words(input)
								}
								if countWidth == 64 {
									args = append(args, execution.Words(c)...)
								} else {
									args = append(args, uintptr(c))
								}
								lo, hi, err := code.CallPair(args...)
								got := uint64(lo)
								if width == 64 {
									got = execution.Wide(lo, hi)
								}
								if err != nil || got != want {
									t.Fatalf("value=%#x count=%#x: got %#x,%v; want %#x", input, c, got, err, want)
								}
							}
						}
					})
				}
			}
		}
	}
}
