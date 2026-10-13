package ir_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func TestAtomicValidation(t *testing.T) {
	param := func(bits uint8) ir.Instruction { return ir.Instruction{Op: ir.OpParam, Bits: bits} }
	load := func(result ...ir.Instruction) []ir.Instruction { return result }
	cases := []struct {
		name string
		ins  []ir.Instruction
		ok   bool
	}{
		{"load", load(param(64), ir.Instruction{Op: ir.OpAtomicLoad, Bits: 16, A: 0}), true},
		{"load 32-bit address", load(param(32), ir.Instruction{Op: ir.OpAtomicLoad, Bits: 64, A: 0}), true},
		{"load 16-bit address", load(param(16), ir.Instruction{Op: ir.OpAtomicLoad, Bits: 16, A: 0}), false},
		{"load 12 bits", load(param(64), ir.Instruction{Op: ir.OpAtomicLoad, Bits: 12, A: 0}), false},
		{"load undefined order", load(param(64), ir.Instruction{Op: ir.OpAtomicLoad, Bits: 32, A: 0, Imm: 1}), false},
		{"load aligned flag", load(param(64), ir.Instruction{Op: ir.OpAtomicLoad, Bits: 32, A: 0, Aligned: true}), false},
		{"load little endian", load(param(64), ir.Instruction{Op: ir.OpAtomicLoad, Bits: 32, A: 0, ByteOrder: ir.ByteOrderLittleEndian}), false},
		{"load with B", load(param(64), param(64), ir.Instruction{Op: ir.OpAtomicLoad, Bits: 64, A: 0, B: 1}), false},
		{"add", load(param(64), param(8), ir.Instruction{Op: ir.OpAtomicAdd, Bits: 8, A: 0, B: 1}), true},
		{"xor mixed widths", load(param(64), param(64), ir.Instruction{Op: ir.OpAtomicXor, Bits: 32, A: 0, B: 1}), false},
		{"swap later operand", load(param(64), ir.Instruction{Op: ir.OpAtomicSwap, Bits: 64, A: 0, B: 1}), false},
		{"cas", load(param(64), param(32), param(32), ir.Instruction{Op: ir.OpAtomicCompareSwap, Bits: 32, A: 0, B: 1, C: 2}), true},
		{"cas replacement width", load(param(64), param(32), param(64), ir.Instruction{Op: ir.OpAtomicCompareSwap, Bits: 32, A: 0, B: 1, C: 2}), false},
		{"store result", load(param(64), param(16), ir.Instruction{Op: ir.OpAtomicStore, Bits: 16, A: 0, B: 1}, ir.Instruction{Op: ir.OpAdd, Bits: 16, A: 2, B: 2}), false},
		{"store typed", load(param(64), param(16), ir.Instruction{Op: ir.OpAtomicStore, Bits: 16, A: 0, B: 1, Type: ir.Scalar(16)}, param(64)), false},
		{"fence", load(param(64), ir.Instruction{Op: ir.OpFence}, param(64)), true},
		{"fence width", load(param(64), ir.Instruction{Op: ir.OpFence, Bits: 64}, param(64)), false},
		{"fence order", load(param(64), ir.Instruction{Op: ir.OpFence, Imm: 1}, param(64)), false},
		{"fence operand", load(param(64), param(64), ir.Instruction{Op: ir.OpFence, A: 1}, param(64)), false},
	}
	for _, c := range cases {
		arity := 0
		for _, ins := range c.ins {
			if ins.Op == ir.OpParam {
				arity++
			}
		}
		_, err := ir.Seal(&ir.Program{Instructions: c.ins, Arity: arity, Result: len(c.ins) - 1})
		if c.ok && err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if !c.ok && !errors.Is(err, ir.ErrInvalidProgram) {
			t.Errorf("%s: err = %v, want ErrInvalidProgram", c.name, err)
		}
	}
	// A store and a fence define no value.
	for _, op := range []ir.Opcode{ir.OpAtomicStore, ir.OpFence} {
		if op.ProducesValue() {
			t.Errorf("%v produces a value", op)
		}
	}
	if !ir.MemoryOrderSequential.Valid() || ir.MemoryOrder(1).Valid() {
		t.Error("MemoryOrder.Valid")
	}
}
