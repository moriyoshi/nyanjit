package riscv64

import (
	"fmt"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func TestTextAssemblerListsEveryEmittedWord(t *testing.T) {
	a := NewTextAssembler(abi.StackPlatform, 10)
	a.Constant(10, 0x123456789abcdef0)
	a.Symbol(11, "data")
	a.Binary(op.OpMul, 64, 12, 10, 11)
	a.Binary(op.OpAnd, 64, 12, 10, 11)
	a.Binary(op.OpOr, 64, 12, 10, 11)
	a.Binary(op.OpXor, 64, 12, 10, 11)
	a.Binary(op.OpSub, 64, 12, 10, 11)
	a.Narrow(12, 10, 8)
	a.SignExtend(12, 10, 16)
	a.Shift(op.OpRotateRight, 64, 12, 10, 7)
	a.Shift(op.OpShiftRightArithmetic, 32, 12, 10, 3)
	a.Scaled(13, 10, 12, 8, 0)
	a.SelectValue(14, 12, 10, 11)
	a.Load8(15, 10)
	a.Store8(10, 15)
	a.LoadAddress(15, 10, 12, 8, 4096)
	a.StoreAddress(10, 12, 15, 8, 4096)
	a.LoadOffset(15, 10, ^uint64(4095))
	a.StoreOffset(10, 15, ^uint64(4095))
	a.SaveCFloat(8, 0)
	a.RestoreCFloat(8, 0)
	a.CallC()
	a.CallGo()
	a.Syscall()
	a.FrameEnter(32)
	a.FrameLeave(32)
	a.CompareRegister(10, 11)
	a.SetPC(a.AssembledSize())
	a.JumpIf(op.BranchBelow, 0)
	a.SetPC(a.AssembledSize())
	a.Jump(0)
	a.CompareImmediate(10, 42)
	a.Ret()

	listing := strings.TrimSuffix(a.Text(), "\n")
	lines := strings.Split(listing, "\n")
	if len(lines) != len(a.Bytes())/4 {
		t.Fatalf("listing has %d lines for %d instruction words", len(lines), len(a.Bytes())/4)
	}
	for index, line := range lines {
		if !strings.HasPrefix(line, fmt.Sprintf("%04x  ", index*4)) {
			t.Fatalf("line %d has wrong PC: %q", index, line)
		}
		if strings.Contains(line, ".word ") {
			t.Fatalf("instruction is undecoded: %q", line)
		}
	}
	for _, fragment := range []string{"# symbol data", "mul ", "bgeu ", "jal ", "fsd ", "fld ", "lbu ", "sb ", "ecall", "ret"} {
		if !strings.Contains(listing, fragment) {
			t.Fatalf("listing is missing %q:\n%s", fragment, listing)
		}
	}
}
