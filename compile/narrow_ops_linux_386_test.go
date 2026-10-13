//go:build linux && 386

package compile

import (
	"math/bits"
	"strconv"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/builder"
)

func TestNarrowArithmeticAndShifts386(t *testing.T) {
	for _, width := range []uint8{8, 16} {
		for _, tc := range []struct {
			name          string
			want8, want16 uint64
		}{
			{"add", 0x84, 0x8004},
			{"sub", 0x7e, 0x7ffe},
			{"mul", 0x83, 0x8003},
			{"shl", 0x02, 0x0002},
			{"shr", 0x40, 0x4000},
			{"sar", 0xc0, 0xc000},
			{"rol", 0x03, 0x0003},
			{"ror", 0xc0, 0xc000},
		} {
			t.Run(strconv.Itoa(int(width))+"/"+tc.name, func(t *testing.T) {
				b := builder.NewBuilder()
				leftValue := uint64(0x81)
				if width == 16 {
					leftValue = 0x8001
				}
				left, err := b.ConstBits(width, leftValue)
				if err != nil {
					t.Fatal(err)
				}
				right, err := b.ConstBits(width, 3)
				if err != nil {
					t.Fatal(err)
				}
				var result builder.Value
				switch tc.name {
				case "add":
					result, err = b.Add(left, right)
				case "sub":
					result, err = b.Sub(left, right)
				case "mul":
					result, err = b.Mul(left, right)
				case "shl":
					result, err = b.ShiftLeft(left, 1)
				case "shr":
					result, err = b.ShiftRight(left, 1)
				case "sar":
					result, err = b.ShiftRightArithmetic(left, 1)
				case "rol":
					result, err = b.RotateLeft(left, 1)
				case "ror":
					result, err = b.RotateRight(left, 1)
				}
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
				want := tc.want8
				if width == 16 {
					want = tc.want16
				}
				if got != want {
					t.Fatalf("got %#x, want %#x", got, want)
				}
			})
		}
	}
}

func TestWideShiftsAndRotations386(t *testing.T) {
	for _, operation := range []string{"shl", "shr", "sar", "rol", "ror"} {
		for _, count := range []uint8{0, 1, 7, 31, 32, 33, 63} {
			t.Run(operation+"/"+strconv.Itoa(int(count)), func(t *testing.T) {
				b := builder.NewBuilder()
				input, err := b.ParamBits(64)
				if err != nil {
					t.Fatal(err)
				}
				var shifted builder.Value
				switch operation {
				case "shl":
					shifted, err = b.ShiftLeft(input, count)
				case "shr":
					shifted, err = b.ShiftRight(input, count)
				case "sar":
					shifted, err = b.ShiftRightArithmetic(input, count)
				case "rol":
					shifted, err = b.RotateLeft(input, count)
				case "ror":
					shifted, err = b.RotateRight(input, count)
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := b.Return(shifted); err != nil {
					t.Fatal(err)
				}
				program, err := b.Finish()
				if err != nil {
					t.Fatal(err)
				}
				code, err := compileCode(program, TargetSpec{}, Func{ABI: abi.C})
				if err != nil {
					t.Fatal(err)
				}
				defer code.Close()
				for _, input := range []uint64{0, 1, 0x8123456789abcdef, 0xfedcba9876543210} {
					lo, hi, err := code.CallPair(execution.Words(input)...)
					if err != nil {
						t.Fatal(err)
					}
					got := execution.Wide(lo, hi)
					var want uint64
					switch operation {
					case "shl":
						want = input << count
					case "shr":
						want = input >> count
					case "sar":
						want = uint64(int64(input) >> count)
					case "rol":
						want = bits.RotateLeft64(input, int(count))
					case "ror":
						want = bits.RotateLeft64(input, -int(count))
					}
					if got != want {
						t.Fatalf("input %#x: got %#x, want %#x", input, got, want)
					}
				}
			})
		}
	}
}
