package builder

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func testNewReturn(value uint64) (*execution.Code, error) {
	return compileCode(&ir.Program{Instructions: []ir.Instruction{{Op: ir.OpConst, Imm: value}}, Result: 0}, compile.TargetSpec{}, compile.Func{ABI: abi.C})
}

func testNewAdd() (*execution.Code, error) {
	return compileCode(&ir.Program{Instructions: []ir.Instruction{
		{Op: ir.OpParam, Bits: 64}, {Op: ir.OpParam, Bits: 64},
		{Op: ir.OpAdd, Bits: 64, A: 0, B: 1},
	}, Arity: 2, Result: 2}, compile.TargetSpec{}, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 240}})
}
