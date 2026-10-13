package ir

import (
	"errors"
	"testing"
)

func TestSecurityUnusedSourceFields(t *testing.T) {
	for _, ins := range []Instruction{
		{Op: OpParam, Bits: 64, Imm: 1},
		{Op: OpConst, Bits: 64, A: 1},
		{Op: OpConst, Bits: 64, B: 1},
		{Op: OpConst, Bits: 64, C: 1},
		{Op: OpConst, Bits: 64, EntryReg: 4},
		{Op: OpConst, Bits: 64, Args: []int{0}},
		{Op: OpConst, Bits: 64, Data: []byte{0}},
		{Op: OpVZero, Type: Vec(2, 64), Bits: 64, Contract: true},
		{Op: OpVZero, Type: Vec(2, 64), Bits: 64, ByteOrder: ByteOrderBigEndian},
	} {
		p := &Program{Instructions: []Instruction{ins}, Result: 0}
		if ins.Op == OpParam {
			p.Arity = 1
		}
		if err := p.Validate(); !errors.Is(err, ErrInvalidProgram) {
			t.Fatalf("unused fields accepted: %+v: %v", ins, err)
		}
	}
}
