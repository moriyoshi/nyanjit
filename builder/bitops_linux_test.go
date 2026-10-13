//go:build linux && (amd64 || arm64 || riscv64)

package builder

import (
	"encoding/binary"
	"math/big"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func applyScalarShift(b *Builder, operation op, value Value, count uint8) (Value, error) {
	switch operation {
	case opShiftLeft:
		return b.ShiftLeft(value, count)
	case opShiftRight:
		return b.ShiftRight(value, count)
	case opShiftRightArithmetic:
		return b.ShiftRightArithmetic(value, count)
	case opRotateLeft:
		return b.RotateLeft(value, count)
	case opRotateRight:
		return b.RotateRight(value, count)
	default:
		return Value{}, ErrInvalidFunction
	}
}

func applyScalarShiftBy(b *Builder, operation op, value, count Value) (Value, error) {
	switch operation {
	case opShiftLeft:
		return b.ShiftLeftBy(value, count)
	case opShiftRight:
		return b.ShiftRightBy(value, count)
	case opShiftRightArithmetic:
		return b.ShiftRightArithmeticBy(value, count)
	case opRotateLeft:
		return b.RotateLeftBy(value, count)
	case opRotateRight:
		return b.RotateRightBy(value, count)
	default:
		return Value{}, ErrInvalidFunction
	}
}

func scalarShiftWant(operation op, width uint8, value, count uint64) uint64 {
	mask := integerMask(width)
	value &= mask
	if operation == opRotateLeft || operation == opRotateRight {
		count %= uint64(width)
	} else if count >= uint64(width) {
		if operation == opShiftRightArithmetic && value&(uint64(1)<<(width-1)) != 0 {
			return mask
		}
		return 0
	}
	switch operation {
	case opShiftLeft:
		return value << count & mask
	case opShiftRight:
		return value >> count
	case opShiftRightArithmetic:
		return uint64((int64(value<<(64-width))>>(64-width))>>count) & mask
	case opRotateLeft:
		return (value<<count | value>>(uint64(width)-count)) & mask
	case opRotateRight:
		return (value>>count | value<<(uint64(width)-count)) & mask
	default:
		panic("unexpected test shift")
	}
}

func TestScalarShiftsAndRotations(t *testing.T) {
	operations := []op{opShiftLeft, opShiftRight, opShiftRightArithmetic, opRotateLeft, opRotateRight}
	for _, width := range []uint8{8, 16, 32, 64} {
		for _, operation := range operations {
			t.Run(strconv.Itoa(int(width))+"/"+strconv.Itoa(int(operation)), func(t *testing.T) {
				build := func(immediate *uint8) *Code {
					t.Helper()
					b := NewBuilder()
					value, err := b.ParamBits(width)
					if err != nil {
						t.Fatal(err)
					}
					var result Value
					if immediate == nil {
						count, err := b.Param()
						if err != nil {
							t.Fatal(err)
						}
						result, err = applyScalarShiftBy(b, operation, value, count)
					} else {
						result, err = applyScalarShift(b, operation, value, *immediate)
					}
					if err != nil {
						t.Fatal(err)
					}
					if err := b.Return(result); err != nil {
						t.Fatal(err)
					}
					code, err := testCompileWithOptions(b, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = code.Close() })
					return code
				}
				values := []uint64{0, 1, uint64(1) << (width - 1), integerMask(width), ^uint64(0)}
				counts := []uint64{0, 1, uint64(width - 1), uint64(width), uint64(width + 1), 255, 256, ^uint64(0)}
				variable := build(nil)
				for _, count := range counts {
					for _, value := range values {
						got, err := variable.Call(uintptr(value), uintptr(count))
						if err != nil {
							t.Fatal(err)
						}
						if want := scalarShiftWant(operation, width, value, count); uint64(got) != want {
							t.Fatalf("variable width=%d op=%d value=%#x count=%#x: got %#x, want %#x", width, operation, value, count, got, want)
						}
					}
					if count > 255 {
						continue
					}
					immediate := uint8(count)
					code := build(&immediate)
					for _, value := range values {
						got, err := code.Call(uintptr(value))
						if err != nil {
							t.Fatal(err)
						}
						if want := scalarShiftWant(operation, width, value, count); uint64(got) != want {
							t.Fatalf("immediate width=%d op=%d value=%#x count=%d: got %#x, want %#x", width, operation, value, count, got, want)
						}
					}
				}
			})
		}
	}
}

func applyWideShift(b *Builder, operation op, value Value, count uint8) (Value, error) {
	switch operation {
	case opShiftLeft:
		return b.ShiftLeft(value, count)
	case opShiftRight:
		return b.ShiftRight(value, count)
	case opShiftRightArithmetic:
		return b.ShiftRightArithmetic(value, count)
	case opRotateLeft:
		return b.RotateLeft(value, count)
	case opRotateRight:
		return b.RotateRight(value, count)
	default:
		return Value{}, ErrInvalidFunction
	}
}

func applyWideShiftBy(b *Builder, operation op, value Value, count Value) (Value, error) {
	switch operation {
	case opShiftLeft:
		return b.ShiftLeftBy(value, count)
	case opShiftRight:
		return b.ShiftRightBy(value, count)
	case opShiftRightArithmetic:
		return b.ShiftRightArithmeticBy(value, count)
	case opRotateLeft:
		return b.RotateLeftBy(value, count)
	case opRotateRight:
		return b.RotateRightBy(value, count)
	default:
		return Value{}, ErrInvalidFunction
	}
}

func wideShiftWant(operation op, lo, hi, count uint64) (uint64, uint64) {
	value := new(big.Int).SetUint64(hi)
	value.Lsh(value, 64)
	value.Or(value, new(big.Int).SetUint64(lo))
	modulus := new(big.Int).Lsh(big.NewInt(1), 128)
	mask := new(big.Int).Sub(new(big.Int).Set(modulus), big.NewInt(1))
	result := new(big.Int).Set(value)
	if operation == opRotateLeft || operation == opRotateRight {
		count %= 128
	} else if count >= 128 {
		if operation == opShiftRightArithmetic && hi>>63 != 0 {
			return ^uint64(0), ^uint64(0)
		}
		return 0, 0
	}
	switch operation {
	case opShiftLeft:
		result.Lsh(result, uint(count))
	case opShiftRight:
		result.Rsh(result, uint(count))
	case opShiftRightArithmetic:
		if hi>>63 != 0 {
			result.Sub(result, modulus)
		}
		result.Rsh(result, uint(count))
	case opRotateLeft:
		left := new(big.Int).Lsh(new(big.Int).Set(value), uint(count))
		right := new(big.Int).Rsh(new(big.Int).Set(value), uint(128-count))
		result.Or(left, right)
	case opRotateRight:
		right := new(big.Int).Rsh(new(big.Int).Set(value), uint(count))
		left := new(big.Int).Lsh(new(big.Int).Set(value), uint(128-count))
		result.Or(left, right)
	}
	result.And(result, mask)
	return result.Uint64(), new(big.Int).Rsh(result, 64).Uint64()
}

func TestWideShiftsAndRotations(t *testing.T) {
	memory, address := mappedTestMemory(t)
	operations := []op{opShiftLeft, opShiftRight, opShiftRightArithmetic, opRotateLeft, opRotateRight}
	for _, operation := range operations {
		t.Run(strconv.Itoa(int(operation)), func(t *testing.T) {
			build := func(immediate *uint8) *Code {
				t.Helper()
				b := NewBuilder()
				value, err := b.ParamBits(128)
				if err != nil {
					t.Fatal(err)
				}
				var result Value
				if immediate == nil {
					count, err := b.Param()
					if err != nil {
						t.Fatal(err)
					}
					result, err = applyWideShiftBy(b, operation, value, count)
				} else {
					result, err = applyWideShift(b, operation, value, *immediate)
				}
				if err != nil {
					t.Fatal(err)
				}
				output, err := b.Param()
				if err != nil {
					t.Fatal(err)
				}
				if err := b.StoreBits(output, result); err != nil {
					t.Fatal(err)
				}
				low, _ := testWideParts(t, b, result)
				if err := b.Return(low); err != nil {
					t.Fatal(err)
				}
				code, err := testCompileWithOptions(b, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = code.Close() })
				return code
			}
			variable := build(nil)
			for _, count := range []uint64{0, 1, 63, 64, 65, 127, 128, 129, 255, 256, ^uint64(0)} {
				for _, sample := range [][2]uint64{{0, 0}, {1, 0}, {0x99aabbccddeeff00, 0x1122334455667788}, {^uint64(0), ^uint64(0)}, {0, 1 << 63}} {
					lo, hi := sample[0], sample[1]
					got, err := variable.Call(uintptr(lo), uintptr(hi), uintptr(count), address)
					if err != nil {
						t.Fatal(err)
					}
					wantLo, wantHi := wideShiftWant(operation, lo, hi, count)
					if uint64(got) != wantLo || binary.LittleEndian.Uint64(memory[8:16]) != wantHi {
						t.Fatalf("variable op=%d value=%#x:%#x count=%#x: got %#x:%#x, want %#x:%#x", operation, hi, lo, count, binary.LittleEndian.Uint64(memory[8:16]), got, wantHi, wantLo)
					}
				}
				if count > 255 {
					continue
				}
				immediate := uint8(count)
				code := build(&immediate)
				for _, sample := range [][2]uint64{{0x99aabbccddeeff00, 0x1122334455667788}, {^uint64(0), ^uint64(0)}, {0, 1 << 63}} {
					lo, hi := sample[0], sample[1]
					got, err := code.Call(uintptr(lo), uintptr(hi), address)
					if err != nil {
						t.Fatal(err)
					}
					wantLo, wantHi := wideShiftWant(operation, lo, hi, count)
					if uint64(got) != wantLo || binary.LittleEndian.Uint64(memory[8:16]) != wantHi {
						t.Fatalf("immediate op=%d value=%#x:%#x count=%d: got %#x:%#x, want %#x:%#x", operation, hi, lo, count, binary.LittleEndian.Uint64(memory[8:16]), got, wantHi, wantLo)
					}
				}
			}
		})
	}
}

func TestShiftAssemblyForms(t *testing.T) {
	build := func(width uint8, operation op) string {
		t.Helper()
		b := NewBuilder()
		value, err := b.ParamBits(width)
		if err != nil {
			t.Fatal(err)
		}
		result, err := applyScalarShift(b, operation, value, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Return(result); err != nil {
			t.Fatal(err)
		}
		assembly, err := testListing(b, compile.TargetSpec{Architecture: runtime.GOARCH}, compile.Func{ABI: abi.C})
		if err != nil {
			t.Fatal(err)
		}
		return assembly
	}
	wide := build(64, opRotateRight)
	if runtime.GOARCH == "riscv64" {
		if !strings.Contains(wide, "slli ") || !strings.Contains(wide, "srli ") || !strings.Contains(wide, "or ") {
			t.Fatalf("64-bit rotate lacks shift/or sequence:\n%s", wide)
		}
	} else if !strings.Contains(wide, "ror ") {
		t.Fatalf("64-bit rotate lacks native ror:\n%s", wide)
	}
	narrow := build(8, opRotateLeft)
	if runtime.GOARCH == "amd64" {
		if !strings.Contains(narrow, "rol ") {
			t.Fatalf("8-bit rotate lacks native rol:\n%s", narrow)
		}
	} else if runtime.GOARCH == "riscv64" {
		if !strings.Contains(narrow, "slli ") || !strings.Contains(narrow, "srli ") || !strings.Contains(narrow, "or ") {
			t.Fatalf("8-bit rotate lacks shift/or sequence:\n%s", narrow)
		}
	} else if !strings.Contains(narrow, "lsl ") || !strings.Contains(narrow, "lsr ") || !strings.Contains(narrow, "orr ") {
		t.Fatalf("8-bit rotate lacks arm64 narrowing sequence:\n%s", narrow)
	}
}
