package loong64_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
)

func TestImmediateOutOfRangeRejected(t *testing.T) {
	for _, tc := range []struct {
		op    op.Opcode
		value uint64
	}{
		{op.OpAdd, 2048}, {op.OpAdd, uint64(1) << 63}, {op.OpSub, 2049}, {op.OpAnd, 4096}, {op.OpOr, ^uint64(0)}, {op.OpXor, 4096},
	} {
		a := loong64.NewByteAssembler(abi.StackNoUse, 0)
		a.BinaryImmediate(tc.op, 64, 4, 5, tc.value)
		if !errors.Is(a.Err(), machine.ErrInvalidInstruction) || len(a.Bytes()) != 0 {
			t.Errorf("op%d imm%x error%v bytes%x", tc.op, tc.value, a.Err(), a.Bytes())
		}
	}
	for _, tc := range []struct {
		op    op.Opcode
		value uint64
	}{
		{op.OpAdd, 2047}, {op.OpAdd, ^uint64(0) - 2047}, {op.OpSub, 2048}, {op.OpAnd, 4095}, {op.OpOr, 0}, {op.OpXor, 4095},
	} {
		a := loong64.NewByteAssembler(abi.StackNoUse, 0)
		a.BinaryImmediate(tc.op, 64, 4, 5, tc.value)
		if len(a.Bytes()) != 4 {
			t.Fatal("valid immediate rejected")
		}
	}
}
