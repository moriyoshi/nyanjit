//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64 || ppc64le)

package compile_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestOperandWidthMismatchReviewCompile(t *testing.T) {
	p := &ir.Program{Arity: 1, Result: 2, Instructions: []ir.Instruction{{Op: ir.OpParam, Bits: 8}, {Op: ir.OpConst, Bits: 16, Imm: 0x7e4}, {Op: ir.OpXor, Bits: 8, A: 0, B: 1}}}
	if _, err := compile.Prepare(p, abi.CompileOptions{}); !errors.Is(err, compile.ErrInvalidFunction) {
		t.Fatalf("compiled mismatched operands: %v", err)
	}
	// Explicitly truncate the constant; unlike the repro, every XOR input is 8 bits.
	p.Instructions[2] = ir.Instruction{Op: ir.OpCastUnsigned, Bits: 8, A: 1}
	p.Instructions = append(p.Instructions, ir.Instruction{Op: ir.OpXor, Bits: 8, A: 0, B: 2})
	p.Result = 3
	code, err := compile.Compile(p, abi.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	if got, err := code.Call(1); err != nil || got != 0xe5 {
		t.Fatalf("explicitly narrowed XOR returned %#x,%v; want 0xe5", got, err)
	}
}
