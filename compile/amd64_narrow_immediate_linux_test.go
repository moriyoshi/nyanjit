//go:build linux && amd64

package compile_test

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/target/amd64"
)

func TestAMD64NarrowBinaryImmediates(t *testing.T) {
	type binaryOp func(*builder.Builder, builder.Value, builder.Value) (builder.Value, error)
	for _, test := range []struct {
		name, mnemonic string
		width          uint8
		operation      binaryOp
		constant       uint64
		zeroExtended   bool
		narrow         bool
		want           func(uint64, uint64) uint64
	}{
		{"8-bit add", "add E", 8, (*builder.Builder).Add, 255, false, true, func(x, y uint64) uint64 { return x + y }},
		{"8-bit xor", "xor E", 8, (*builder.Builder).Xor, 255, false, false, func(x, y uint64) uint64 { return x ^ y }},
		{"16-bit subtract", "sub E", 16, (*builder.Builder).Sub, 0xffff, false, true, func(x, y uint64) uint64 { return x - y }},
		{"16-bit or", "or E", 16, (*builder.Builder).Or, 0xffff, false, false, func(x, y uint64) uint64 { return x | y }},
		{"32-bit add wraparound", "add E", 32, (*builder.Builder).Add, 0xffffffff, true, false, func(x, y uint64) uint64 { return x + y }},
		{"32-bit subtract high bit", "sub E", 32, (*builder.Builder).Sub, 0x80000000, true, false, func(x, y uint64) uint64 { return x - y }},
		{"32-bit and full mask", "and E", 32, (*builder.Builder).And, 0xffffffff, true, false, func(x, y uint64) uint64 { return x & y }},
		{"32-bit xor full mask", "xor E", 32, (*builder.Builder).Xor, 0xffffffff, true, false, func(x, y uint64) uint64 { return x ^ y }},
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
				program.Result, nil, abi.MaxArguments(), amd64.NewPolicy(abi.C, abi.StackNoUse))
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
					if ins.ZeroExtended != test.zeroExtended || ins.Imm != test.constant {
						t.Fatalf("selected %+v, want immediate %#x and ZeroExtended=%t", ins, test.constant, test.zeroExtended)
					}
				}
			}
			if index < 0 {
				t.Fatalf("immediate operation missing: %+v", selected.Instructions)
			}
			nextIsNarrow := index+1 < len(selected.Instructions) &&
				selected.Instructions[index+1].Op == machine.SelectedNarrow &&
				selected.Instructions[index+1].A == selected.Instructions[index].Dst
			if nextIsNarrow != test.narrow {
				t.Fatalf("result narrowing=%t, want %t: %+v", nextIsNarrow, test.narrow, selected.Instructions)
			}
			options := abi.CompileOptions{ABI: abi.C, Stack: abi.StackNoUse}
			assembly, err := compile.EmitAssembly(program, options)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(assembly, test.mnemonic) {
				t.Fatalf("immediate instruction missing:\n%s", assembly)
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
