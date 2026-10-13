package goregister_test

import (
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func testNewReturn(value uint64) (*compile.Code, error) {
	return compile.Compile(&ir.Program{Instructions: []ir.Instruction{{Op: ir.OpConst, Imm: value}}, Result: 0}, compile.CompileOptions{ABI: compile.ABIC})
}

func testNewAdd() (*compile.Code, error) {
	return compile.Compile(&ir.Program{Instructions: []ir.Instruction{
		{Op: ir.OpParam, Bits: 64}, {Op: ir.OpParam, Bits: 64},
		{Op: ir.OpAdd, Bits: 64, A: 0, B: 1},
	}, Arity: 2, Result: 2}, compile.CompileOptions{ABI: compile.ABIC, Stack: compile.StackPlatform, StackBytesAvailable: 240})
}
