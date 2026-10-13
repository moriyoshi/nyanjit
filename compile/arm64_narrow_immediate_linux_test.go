//go:build linux && arm64

package compile_test

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/target/arm64"
)

func TestARM64NarrowAndLogicalImmediates(t *testing.T) {
	type binaryOp func(*builder.Builder, builder.Value, builder.Value) (builder.Value, error)
	for _, test := range []struct {
		name, mnemonic string
		width          uint8
		operation      binaryOp
		constant       uint64
		immediate      bool
		zeroExtended   bool
		narrow         bool
		want           func(uint64, uint64) uint64
	}{
		{"8-bit add", "adds W", 8, (*builder.Builder).Add, 255, true, false, true, func(x, y uint64) uint64 { return x + y }},
		// Register SUB preserves narrow no-borrow; flipped ADDS W cannot.
		{"16-bit subtract signed literal", "subs X", 16, (*builder.Builder).Sub, 0xffff, false, false, true, func(x, y uint64) uint64 { return x - y }},
		{"32-bit add signed literal", "subs W", 32, (*builder.Builder).Add, 0xffffffff, true, true, false, func(x, y uint64) uint64 { return x + y }},
		{"32-bit shifted subtract", "subs W", 32, (*builder.Builder).Sub, 4096, true, true, false, func(x, y uint64) uint64 { return x - y }},
		{"32-bit add outside boundary", "", 32, (*builder.Builder).Add, 4097, false, false, false, func(x, y uint64) uint64 { return x + y }},
		{"8-bit and bitmask", "ands W", 8, (*builder.Builder).And, 0x80, true, false, false, func(x, y uint64) uint64 { return x & y }},
		{"16-bit xor bitmask", "eor W", 16, (*builder.Builder).Xor, 0xffff, true, false, false, func(x, y uint64) uint64 { return x ^ y }},
		{"32-bit repeated bitmask", "eor W", 32, (*builder.Builder).Xor, 0x00ff00ff, true, true, false, func(x, y uint64) uint64 { return x ^ y }},
		{"32-bit full mask in X form", "orr X", 32, (*builder.Builder).Or, 0xffffffff, true, true, false, func(x, y uint64) uint64 { return x | y }},
		{"64-bit repeated bitmask", "ands X", 64, (*builder.Builder).And, 0xff00ff00ff00ff00, true, false, false, func(x, y uint64) uint64 { return x & y }},
		{"64-bit high bit", "eor X", 64, (*builder.Builder).Xor, 0x8000000000000000, true, false, false, func(x, y uint64) uint64 { return x ^ y }},
		{"64-bit zero mask", "", 64, (*builder.Builder).And, 0, false, false, false, func(x, y uint64) uint64 { return x & y }},
		{"64-bit full mask", "", 64, (*builder.Builder).Or, ^uint64(0), false, false, false, func(x, y uint64) uint64 { return x | y }},
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
			program, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			selected, err := machine.SelectProgram(program.Instructions, program.ScopeExits, program.Arity,
				program.Result, nil, abi.MaxArguments(), arm64.NewPolicy(abi.C, abi.StackNoUse))
			if err != nil {
				t.Fatal(err)
			}
			index := -1
			for i, ins := range selected.Instructions {
				if ins.Op == machine.SelectedBinaryImmediate {
					if index >= 0 {
						t.Fatalf("multiple immediate operations: %+v", selected.Instructions)
					}
					index = i
					if ins.ZeroExtended != test.zeroExtended {
						t.Fatalf("ZeroExtended=%t, want %t", ins.ZeroExtended, test.zeroExtended)
					}
				}
			}
			if (index >= 0) != test.immediate {
				t.Fatalf("selected immediate=%t, want %t: %+v", index >= 0, test.immediate, selected.Instructions)
			}
			if index >= 0 {
				nextIsNarrow := index+1 < len(selected.Instructions) &&
					selected.Instructions[index+1].Op == machine.SelectedNarrow &&
					selected.Instructions[index+1].A == selected.Instructions[index].Dst
				if nextIsNarrow != test.narrow {
					t.Fatalf("result narrowing=%t, want %t: %+v", nextIsNarrow, test.narrow, selected.Instructions)
				}
			}
			options := abi.CompileOptions{ABI: abi.C, Stack: abi.StackNoUse}
			if test.immediate {
				assembly, err := compile.EmitAssembly(program, options)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(assembly, test.mnemonic) {
					t.Fatalf("immediate instruction missing:\n%s", assembly)
				}
			}
			code, err := compile.Compile(program, options)
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
