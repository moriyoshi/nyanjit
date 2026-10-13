package amd64

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"testing"
)

func TestIncrementPreservesCarryFact(t *testing.T) {
	for _, op := range []machine.Opcode{machine.OpInc, machine.OpDec} {
		s := machine.NewStream(machine.StreamOptions{Size: func(machine.Instruction) int { return 1 }, FlagEffect: func(i machine.Instruction) bool { return FlagFacts(i).Modified != 0 }, FlagFacts: FlagFacts})
		s.Append(machine.Instruction{Op: machine.OpBinary, Arithmetic: ir.OpAdd, Bits: 64, A: 0, B: 1, C: 2})
		s.BindFlagValue(3, 1, 2, ir.OpAdd, false)
		s.Append(machine.Instruction{Op: op, A: 4, B: 4})
		s.BindFlagValue(5, 4, -1, ir.OpAdd, true)
		f := s.Flags()
		if !f.Carry.MatchesValue(3) || !f.Zero.MatchesValue(5) || f.Modified&machine.FlagCarry != 0 {
			t.Fatalf("partial flags: %+v", f)
		}
	}
}
func TestUndefinedMultiplyFlags(t *testing.T) {
	f := FlagFacts(machine.Instruction{Op: machine.OpBinary, Arithmetic: ir.OpMul, Bits: 64})
	if f.Zero.Valid || f.Sign.Valid || f.Carry.Valid {
		t.Fatal(f)
	}
}

func TestBMI2PreservesFlags(t *testing.T) {
	p := abi.CPUProfile{Extensions: []string{"bmi2"}}
	for _, ins := range []machine.Instruction{{Op: machine.OpMulHigh, Bits: 64}, {Op: machine.OpShift, Arithmetic: ir.OpRotateRight, Bits: 64, Imm: 3}} {
		if f := FlagFactsWithCPU(ins, p); f.Modified != 0 {
			t.Fatalf("BMI2 flags: %+v", f)
		}
	}
	if f := FlagFactsWithCPU(machine.Instruction{Op: machine.OpShiftBy, Arithmetic: ir.OpRotateRight, Bits: 64}, p); f.Modified != machine.FlagCarry {
		t.Fatalf("rotate flags: %+v", f)
	}
}
