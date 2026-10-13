//go:build linux && amd64

package compile_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/target/amd64"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestAMD64BinaryImmediateSelectionAndExecution(t *testing.T) {
	type binaryOp func(*builder.Builder, builder.Value, builder.Value) (builder.Value, error)
	for _, test := range []struct {
		name, mnemonic string
		operation      binaryOp
		constant       uint64
		immediate      bool
		want           func(uint64, uint64) uint64
	}{
		{"add imm8 upper", "add", (*builder.Builder).Add, 127, true, func(x, y uint64) uint64 { return x + y }},
		{"add imm8 lower", "add", (*builder.Builder).Add, ^uint64(127), true, func(x, y uint64) uint64 { return x + y }},
		{"subtract imm32", "sub", (*builder.Builder).Sub, 0x7fffffff, true, func(x, y uint64) uint64 { return x - y }},
		{"and negative", "and", (*builder.Builder).And, ^uint64(127), true, func(x, y uint64) uint64 { return x & y }},
		{"or positive", "or", (*builder.Builder).Or, 0x12345678, true, func(x, y uint64) uint64 { return x | y }},
		{"xor negative", "xor", (*builder.Builder).Xor, ^uint64(0x7fffffff), true, func(x, y uint64) uint64 { return x ^ y }},
		{"positive imm32 sign bit", "", (*builder.Builder).Add, 0x80000000, false, func(x, y uint64) uint64 { return x + y }},
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
			selected, err := compile.SelectProgram(program.Instructions, program.ScopeExits, program.Arity,
				program.Result, nil, abi.MaxArguments(), amd64.NewPolicy(abi.C, abi.StackNoUse))
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
				t.Fatalf("selected %d immediate operations; want immediate=%t", immediates, test.immediate)
			}
			fn := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackNoUse}}
			if test.immediate {
				assembly, err := lowerListing(program, compile.TargetSpec{}, fn)
				if err != nil {
					t.Fatal(err)
				}
				forms := []string{test.mnemonic + " RDI, ", test.mnemonic + " RAX, "}
				if test.mnemonic == "add" || test.mnemonic == "sub" {
					// Unread flags let LEA carry the immediate as its displacement.
					forms = append(forms, "lea RAX, [RDI ")
				}
				if !slices.ContainsFunc(forms, func(form string) bool { return strings.Contains(assembly, form) }) {
					t.Fatalf("immediate instruction missing:\n%s", assembly)
				}
			}
			code, err := nyanjit.Compile(program, compile.TargetSpec{}, fn)
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			for _, value := range []uint64{0, 1, 0x123456789abcdef0, ^uint64(0)} {
				got, err := code.Call(uintptr(value))
				want := test.want(value, test.constant)
				if err != nil || uint64(got) != want {
					t.Fatalf("Call(%#x) = %#x, %v; want %#x", value, got, err, want)
				}
			}
		})
	}
}
