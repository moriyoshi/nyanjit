//go:build linux && riscv64

package compile_test

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/target/riscv64"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestRISCV64BinaryImmediateSelectionAndExecution(t *testing.T) {
	type binaryOp func(*builder.Builder, builder.Value, builder.Value) (builder.Value, error)
	for _, test := range []struct {
		name, mnemonic string
		operation      binaryOp
		constant       uint64
		immediate      bool
		want           func(uint64, uint64) uint64
	}{
		{"add upper boundary", "addi", (*builder.Builder).Add, 2047, true, func(x, y uint64) uint64 { return x + y }},
		{"add lower boundary", "addi", (*builder.Builder).Add, ^uint64(2047), true, func(x, y uint64) uint64 { return x + y }},
		{"add outside boundary", "", (*builder.Builder).Add, 2048, false, func(x, y uint64) uint64 { return x + y }},
		{"subtract upper boundary", "addi", (*builder.Builder).Sub, 2048, true, func(x, y uint64) uint64 { return x - y }},
		{"subtract lower boundary", "addi", (*builder.Builder).Sub, ^uint64(2046), true, func(x, y uint64) uint64 { return x - y }},
		{"subtract outside boundary", "", (*builder.Builder).Sub, ^uint64(2047), false, func(x, y uint64) uint64 { return x - y }},
		{"and negative", "andi", (*builder.Builder).And, ^uint64(0), true, func(x, y uint64) uint64 { return x & y }},
		{"or positive", "ori", (*builder.Builder).Or, 127, true, func(x, y uint64) uint64 { return x | y }},
		{"xor negative", "xori", (*builder.Builder).Xor, ^uint64(0), true, func(x, y uint64) uint64 { return x ^ y }},
		{"xor outside boundary", "", (*builder.Builder).Xor, 2048, false, func(x, y uint64) uint64 { return x ^ y }},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := builder.NewBuilder()
			input, err := b.Param()
			if err != nil {
				t.Fatal(err)
			}
			constant, err := b.Const(test.constant)
			if err != nil {
				t.Fatal(err)
			}
			result, err := test.operation(b, input, constant)
			if err != nil {
				t.Fatal(err)
			}
			if err := b.Return(result); err != nil {
				t.Fatal(err)
			}
			program, err := testFinishRaw(b)
			if err != nil {
				t.Fatal(err)
			}
			policy := riscv64.NewPolicy(abi.C, abi.StackNoUse)
			selected, err := compile.SelectProgram(program.Instructions, program.ScopeExits, program.Arity,
				program.Result, nil, abi.MaxArguments(), policy)
			if err != nil {
				t.Fatal(err)
			}
			var immediates int
			for _, ins := range selected.Instructions {
				if ins.Op == machine.SelectedBinaryImmediate {
					immediates++
				}
			}
			if (immediates == 1) != test.immediate {
				t.Fatalf("selected %d binary immediate operations, want immediate=%t", immediates, test.immediate)
			}
			options := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackNoUse}}
			if test.immediate {
				assembly, err := lowerListing(program, compile.TargetSpec{}, options)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(assembly, test.mnemonic+" x10, x10, ") {
					t.Fatalf("immediate instruction missing:\n%s", assembly)
				}
			}
			code, err := nyanjit.Compile(program, compile.TargetSpec{}, options)
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			for _, input := range []uint64{0, 1, 0x123456789abcdef0, ^uint64(0)} {
				got, err := code.Call(uintptr(input))
				want := test.want(input, test.constant)
				if err != nil || uint64(got) != want {
					t.Fatalf("Call(%#x) = %#x, %v; want %#x", input, got, err, want)
				}
			}
		})
	}
}

func TestRISCV64ScaledDisplacementImmediate(t *testing.T) {
	for _, test := range []struct {
		name      string
		scale     uint64
		disp      uint64
		immediate bool
	}{
		{"base only", 0, 31, true},
		{"shifted index", 8, 2047, true},
		{"multiplied index", 7, ^uint64(2047), true},
		{"large displacement", 8, 2048, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := builder.NewBuilder()
			base, err := b.Param()
			if err != nil {
				t.Fatal(err)
			}
			index, err := b.Param()
			if err != nil {
				t.Fatal(err)
			}
			scaled := base.Add(index.MulConst(test.scale))
			displacement, err := b.Const(test.disp)
			if err != nil {
				t.Fatal(err)
			}
			result, err := b.Add(scaled, displacement)
			if err != nil {
				t.Fatal(err)
			}
			if err := b.Return(result); err != nil {
				t.Fatal(err)
			}
			program, err := testFinishRaw(b)
			if err != nil {
				t.Fatal(err)
			}
			policy := riscv64.NewPolicy(abi.C, abi.StackNoUse)
			selected, err := compile.SelectProgram(program.Instructions, program.ScopeExits, program.Arity,
				program.Result, nil, abi.MaxArguments(), policy)
			if err != nil {
				t.Fatal(err)
			}
			var immediates int
			for _, ins := range selected.Instructions {
				if ins.Op == machine.SelectedBinaryImmediate {
					immediates++
				}
			}
			if (immediates == 1) != test.immediate {
				t.Fatalf("selected %d binary immediate operations, want immediate=%t", immediates, test.immediate)
			}
			options := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackNoUse}}
			code, err := nyanjit.Compile(program, compile.TargetSpec{}, options)
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			for _, input := range [][2]uint64{{0, 0}, {23, 11}, {^uint64(0), 3}} {
				got, err := code.Call(uintptr(input[0]), uintptr(input[1]))
				want := input[0] + input[1]*test.scale + test.disp
				if err != nil || uint64(got) != want {
					t.Fatalf("Call(%#x, %#x) = %#x, %v; want %#x", input[0], input[1], got, err, want)
				}
			}
		})
	}
}

func TestRISCV64NarrowBinaryImmediate(t *testing.T) {
	type binaryOp func(*builder.Builder, builder.Value, builder.Value) (builder.Value, error)
	for _, test := range []struct {
		name        string
		width       uint8
		operation   binaryOp
		constant    uint64
		immediate   bool
		encoded     uint64
		needsNarrow bool
		want        func(uint64, uint64) uint64
	}{
		{"8-bit add", 8, (*builder.Builder).Add, 255, true, 255, true, func(x, y uint64) uint64 { return x + y }},
		{"8-bit xor", 8, (*builder.Builder).Xor, 255, true, 255, false, func(x, y uint64) uint64 { return x ^ y }},
		{"16-bit subtract signed literal", 16, (*builder.Builder).Sub, 0xffff, true, ^uint64(0), true, func(x, y uint64) uint64 { return x - y }},
		{"32-bit add signed literal", 32, (*builder.Builder).Add, 0xffffffff, true, ^uint64(0), true, func(x, y uint64) uint64 { return x + y }},
		{"32-bit subtract boundary", 32, (*builder.Builder).Sub, 2048, true, 2048, true, func(x, y uint64) uint64 { return x - y }},
		{"32-bit and signed literal", 32, (*builder.Builder).And, 0xffffffff, true, ^uint64(0), false, func(x, y uint64) uint64 { return x & y }},
		{"32-bit or signed literal", 32, (*builder.Builder).Or, 0xffffffff, true, ^uint64(0), true, func(x, y uint64) uint64 { return x | y }},
		{"32-bit xor signed literal", 32, (*builder.Builder).Xor, 0xffffffff, true, ^uint64(0), true, func(x, y uint64) uint64 { return x ^ y }},
		{"32-bit or positive literal", 32, (*builder.Builder).Or, 2047, true, 2047, false, func(x, y uint64) uint64 { return x | y }},
		{"32-bit add outside boundary", 32, (*builder.Builder).Add, 2048, false, 0, false, func(x, y uint64) uint64 { return x + y }},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := builder.NewBuilder()
			input, err := b.ParamBits(test.width)
			if err != nil {
				t.Fatal(err)
			}
			constant, err := b.ConstBits(test.width, test.constant)
			if err != nil {
				t.Fatal(err)
			}
			result, err := test.operation(b, input, constant)
			if err != nil {
				t.Fatal(err)
			}
			if err := b.Return(result); err != nil {
				t.Fatal(err)
			}
			program, err := testFinishRaw(b)
			if err != nil {
				t.Fatal(err)
			}
			policy := riscv64.NewPolicy(abi.C, abi.StackNoUse)
			selected, err := compile.SelectProgram(program.Instructions, program.ScopeExits, program.Arity,
				program.Result, nil, abi.MaxArguments(), policy)
			if err != nil {
				t.Fatal(err)
			}
			immediateIndex := -1
			for i, ins := range selected.Instructions {
				if ins.Op == machine.SelectedBinaryImmediate {
					if immediateIndex >= 0 {
						t.Fatalf("more than one immediate operation: %+v", selected.Instructions)
					}
					immediateIndex = i
					if ins.Imm != test.encoded || ins.Bits != test.width {
						t.Fatalf("selected immediate = %#x at width %d; want %#x at width %d", ins.Imm, ins.Bits, test.encoded, test.width)
					}
				}
			}
			if (immediateIndex >= 0) != test.immediate {
				t.Fatalf("selected immediate=%t; want %t: %+v", immediateIndex >= 0, test.immediate, selected.Instructions)
			}
			if immediateIndex >= 0 {
				nextIsNarrow := immediateIndex+1 < len(selected.Instructions) &&
					selected.Instructions[immediateIndex+1].Op == machine.SelectedNarrow &&
					selected.Instructions[immediateIndex+1].A == selected.Instructions[immediateIndex].Dst
				if nextIsNarrow != test.needsNarrow {
					t.Fatalf("result narrowing=%t; want %t: %+v", nextIsNarrow, test.needsNarrow, selected.Instructions)
				}
			}
			code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackNoUse}})
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			mask := ^uint64(0) >> (64 - test.width)
			for _, value := range []uint64{0, 1, mask, ^uint64(0), 0x123456789abcdef0} {
				got, err := code.Call(uintptr(value))
				want := test.want(value&mask, test.constant) & mask
				if err != nil || uint64(got) != want {
					t.Fatalf("Call(%#x) = %#x, %v; want %#x", value, got, err, want)
				}
			}
		})
	}
}
