//go:build linux && 386

package compile_test

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
)

func Test386BinaryImmediateSelectionAndExecution(t *testing.T) {
	type binaryOp func(*builder.Builder, builder.Value, builder.Value) (builder.Value, error)
	for _, test := range []struct {
		name, mnemonic string
		operation      binaryOp
		constant       uint64
		want           func(uint64, uint64) uint64
	}{
		{"add wraparound", "add", (*builder.Builder).Add, 0x80000000, func(x, y uint64) uint64 { return x + y }},
		{"subtract wraparound", "sub", (*builder.Builder).Sub, 0xffffffff, func(x, y uint64) uint64 { return x - y }},
		{"and", "and", (*builder.Builder).And, 0x8000ffff, func(x, y uint64) uint64 { return x & y }},
		{"or", "or", (*builder.Builder).Or, 0xffff0000, func(x, y uint64) uint64 { return x | y }},
		{"xor", "xor", (*builder.Builder).Xor, 0x5555aaaa, func(x, y uint64) uint64 { return x ^ y }},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := builder.NewBuilder()
			input, err := b.ParamBits(32)
			if err != nil {
				t.Fatal(err)
			}
			constant, err := b.ConstBits(32, test.constant)
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
				program.Result, nil, abi.MaxArguments(), i386.NewPolicy(abi.C, abi.StackNoUse))
			if err != nil {
				t.Fatal(err)
			}
			var immediates int
			for _, ins := range selected.Instructions {
				if ins.Op == machine.SelectedBinaryImmediate && ins.Bits == 32 && ins.Imm == test.constant {
					immediates++
				}
			}
			if immediates != 1 {
				t.Fatalf("selected %d matching immediate operations; want one: %+v", immediates, selected.Instructions)
			}
			fn := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackNoUse}}
			assembly, err := lowerListing(program, compile.TargetSpec{}, fn)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(assembly, test.mnemonic+" EAX, 0x") {
				t.Fatalf("immediate instruction missing:\n%s", assembly)
			}
			code, err := nyanjit.Compile(program, compile.TargetSpec{}, fn)
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			for _, value := range []uint64{0, 1, 0xffffffff, 0x12345678, ^uint64(0)} {
				lo, hi, err := code.CallPair(uintptr(value))
				got := execution.Wide(lo, hi)
				want := test.want(value&0xffffffff, test.constant) & 0xffffffff
				if err != nil || got != want {
					t.Fatalf("Call(%#x) = %#x, %v; want %#x", value, got, err, want)
				}
			}
		})
	}
}
