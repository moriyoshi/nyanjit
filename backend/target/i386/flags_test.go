package i386_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
)

// TestFlagFacts lists the modified flags and the widths of the result facts
// of each kind of form; zero is no fact. backend/target checks every
// documented form against its decoded encoding.
func TestFlagFacts(t *testing.T) {
	const all = machine.FlagSign | machine.FlagZero | machine.FlagCarry
	binary := func(o op.Opcode, w uint8) machine.Instruction {
		return machine.Instruction{Op: machine.OpBinary, Arithmetic: o, Bits: w, A: 0, B: 0, C: 1}
	}
	for _, tt := range []struct {
		name                string
		ins                 machine.Instruction
		modified            machine.FlagMask
		sign, zero, carry   uint8
		carryCondition, any bool
	}{
		{name: "add", ins: binary(op.OpAdd, 32), modified: all, sign: 32, zero: 32, carry: 32, carryCondition: true},
		{name: "sub", ins: binary(op.OpSub, 0), modified: all, sign: 32, zero: 32, carry: 32, carryCondition: true},
		{name: "and", ins: binary(op.OpAnd, 32), modified: all, sign: 32, zero: 32},
		// IMUL r32: CF and OF are the signed overflow; S and Z undefined.
		{name: "mul", ins: binary(op.OpMul, 32), modified: all, carry: 32},
		// AND A, mask ends a narrow form: the register's sign, the narrow
		// zero. A 32-bit zero would let a later narrowing alias the
		// source's zero.
		{name: "add-8", ins: binary(op.OpAdd, 8), modified: all, sign: 32, zero: 8},
		{name: "mul-16", ins: binary(op.OpMul, 16), modified: all, sign: 32, zero: 16},
		{name: "add-immediate", ins: machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAdd, Bits: 32, Imm: 5}, modified: all, sign: 32, zero: 32, carry: 32, carryCondition: true},
		{name: "or-immediate-8", ins: machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpOr, Bits: 8, Imm: 5}, modified: all},
		{name: "inc", ins: machine.Instruction{Op: machine.OpInc}, modified: machine.FlagSign | machine.FlagZero, sign: 32, zero: 32},
		{name: "dec", ins: machine.Instruction{Op: machine.OpDec}, modified: machine.FlagSign | machine.FlagZero, sign: 32, zero: 32},
		{name: "narrow-8", ins: machine.Instruction{Op: machine.OpNarrow, Imm: 8}, modified: all, sign: 32, zero: 8},
		{name: "narrow-32", ins: machine.Instruction{Op: machine.OpNarrow, Imm: 32}},
		{name: "sign-extend-16", ins: machine.Instruction{Op: machine.OpSignExtend, Imm: 16}, modified: all, sign: 32, zero: 32},
		{name: "sign-extend-32", ins: machine.Instruction{Op: machine.OpSignExtend, Imm: 32}},
		// A pair's high-word ADC: the pair's sign and 64-bit carry, no zero.
		{name: "pair-adc", ins: machine.Instruction{Op: machine.OpBinaryCarry, Arithmetic: op.OpAdd}, modified: all, sign: 64, carry: 64, carryCondition: true},
		{name: "pair-sbb", ins: machine.Instruction{Op: machine.OpBinaryCarry, Arithmetic: op.OpSub}, modified: all, sign: 64, carry: 64, carryCondition: true},
		{name: "word-carry", ins: machine.Instruction{Op: machine.OpWordCarry, Arithmetic: op.OpAdd, Bits: 32, CarryMode: machine.CarryFlags}, modified: all, sign: 32, zero: 32},
		{name: "carry-bit-flags", ins: machine.Instruction{Op: machine.OpCarryBit, CarryMode: machine.CarryFlags}},
		{name: "carry-bit", ins: machine.Instruction{Op: machine.OpCarryBit, Arithmetic: op.OpAdd, CarryMode: machine.CarryOperands}, modified: all},
		{name: "sar", ins: machine.Instruction{Op: machine.OpShift, Arithmetic: op.OpShiftRightArithmetic, Bits: 32, Imm: 31}, modified: all, sign: 32},
		{name: "shl", ins: machine.Instruction{Op: machine.OpShift, Arithmetic: op.OpShiftLeft, Bits: 32, Imm: 3}, modified: all},
		{name: "shl-0", ins: machine.Instruction{Op: machine.OpShift, Arithmetic: op.OpShiftLeft, Bits: 32}},
		// The AND mask after a narrow shift, even by a count of zero; SAR
		// sign-extends first and masks nothing.
		{name: "shl-8", ins: machine.Instruction{Op: machine.OpShift, Arithmetic: op.OpShiftLeft, Bits: 8, Imm: 3}, modified: all, sign: 32, zero: 8},
		{name: "shr-16-0", ins: machine.Instruction{Op: machine.OpShift, Arithmetic: op.OpShiftRight, Bits: 16}, modified: all, sign: 32, zero: 16},
		{name: "sar-8", ins: machine.Instruction{Op: machine.OpShift, Arithmetic: op.OpShiftRightArithmetic, Bits: 8, Imm: 3}, modified: all},
		{name: "rol", ins: machine.Instruction{Op: machine.OpShift, Arithmetic: op.OpRotateLeft, Bits: 32, Imm: 3}, modified: machine.FlagCarry},
		{name: "pair-shl-32", ins: machine.Instruction{Op: machine.OpShift, Arithmetic: op.OpShiftLeft, Bits: 64, Imm: 32}},
		{name: "pair-sar-32", ins: machine.Instruction{Op: machine.OpShift, Arithmetic: op.OpShiftRightArithmetic, Bits: 64, Imm: 32}, modified: all},
		{name: "rol-cl", ins: machine.Instruction{Op: machine.OpShiftBy, Arithmetic: op.OpRotateLeft, Bits: 32}, modified: machine.FlagCarry},
		{name: "rol16", ins: machine.Instruction{Op: machine.OpReverseBytes, Bits: 16}, modified: machine.FlagCarry},
		{name: "bswap", ins: machine.Instruction{Op: machine.OpReverseBytes, Bits: 32}},
		{name: "cmpxchg8b", ins: machine.Instruction{Op: machine.OpAtomic, Arithmetic: op.OpAtomicSwap, Bits: 64}, modified: machine.FlagZero},
		{name: "cvtsi2sd", ins: machine.Instruction{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromSigned, Bits: 64, Imm: 32}},
		{name: "cvttsd2si", ins: machine.Instruction{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 32, Disp: 64, Imm: uint64(op.ConversionNative)}},
		{name: "compare", ins: machine.Instruction{Op: machine.OpCompareRegister}, modified: all},
		// Calls and INT 0x80 clobber the flags, as abi reports.
		{name: "call-c", ins: machine.Instruction{Op: machine.OpCallC}, modified: all},
		{name: "call-go", ins: machine.Instruction{Op: machine.OpCallGo}, modified: all},
		{name: "syscall", ins: machine.Instruction{Op: machine.OpSyscall}, modified: all},
		{name: "tail-call", ins: machine.Instruction{Op: machine.OpTailCall}, modified: all},
		{name: "move", ins: machine.Instruction{Op: machine.OpMove, A: 0, B: 1}},
		{name: "lea", ins: machine.Instruction{Op: machine.OpScaled, A: 0, B: 1, C: 2, Imm: 4}},
		// PreserveFlags forms are LEA.
		{name: "add-lea", ins: machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 32, A: 0, B: 1, C: 2, PreserveFlags: true}},
		{name: "sub-immediate-lea", ins: machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpSub, Bits: 32, A: 0, B: 1, Imm: 8, PreserveFlags: true}},
		{name: "inc-lea", ins: machine.Instruction{Op: machine.OpInc, A: 0, B: 1, PreserveFlags: true}},
		{name: "dec-lea", ins: machine.Instruction{Op: machine.OpDec, A: 0, B: 1, PreserveFlags: true}},
	} {
		f := i386.FlagFacts(tt.ins)
		width := func(fact machine.FlagFact) uint8 {
			if !fact.Valid {
				return 0
			}
			return fact.Width
		}
		if f.Modified != tt.modified || width(f.Sign) != tt.sign || width(f.Zero) != tt.zero || width(f.Carry) != tt.carry ||
			f.Carry.ConditionValid != tt.carryCondition || tt.carryCondition && f.Carry.Condition != op.BranchBelow {
			t.Errorf("%s: %+v", tt.name, f)
		}
		if i386.FlagEffect(tt.ins) != (tt.modified != 0) {
			t.Errorf("%s: FlagEffect %v", tt.name, i386.FlagEffect(tt.ins))
		}
	}
}

// TestIncrementKeepsCarry: INC and DEC replace the sign and zero facts and
// keep an ADD's carry fact.
func TestIncrementKeepsCarry(t *testing.T) {
	for _, operation := range []machine.Opcode{machine.OpInc, machine.OpDec} {
		s := machine.NewStream(machine.StreamOptions{Size: func(machine.Instruction) int { return 1 }, FlagEffect: i386.FlagEffect, FlagFacts: i386.FlagFacts, ExactFlagFacts: true})
		s.Append(machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 32, A: 0, B: 0, C: 1})
		s.BindFlagValue(3, 1, 2, op.OpAdd, false)
		s.Append(machine.Instruction{Op: operation, A: 4, B: 4})
		s.BindFlagValue(5, 4, -1, op.OpAdd, true)
		f := s.Flags()
		if !f.Carry.MatchesValue(3) || !f.Zero.MatchesValue(5) || !f.Sign.MatchesValue(5) || f.Modified&machine.FlagCarry != 0 {
			t.Fatalf("%v: %+v", operation, f)
		}
	}
}

// TestNarrowZeroAliases: narrowing a 32-bit sum gives a fresh zero fact; it
// keeps the sum's only when the sum was already that narrow.
func TestNarrowZeroAliases(t *testing.T) {
	for _, tt := range []struct {
		bits  uint8
		alias bool
	}{{32, false}, {8, true}} {
		s := machine.NewStream(machine.StreamOptions{Size: func(machine.Instruction) int { return 1 }, FlagEffect: i386.FlagEffect, FlagFacts: i386.FlagFacts, ExactFlagFacts: true})
		s.Append(machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: tt.bits, A: 0, B: 0, C: 1})
		s.BindFlagValue(3, 1, 2, op.OpAdd, false)
		s.Append(machine.Instruction{Op: machine.OpNarrow, A: 0, B: 0, Imm: 8})
		s.BindFlagValue(4, 3, -1, op.OpCastUnsigned, false)
		if f := s.Flags(); !f.Zero.MatchesValue(4) || f.Zero.MatchesValue(3) != tt.alias {
			t.Fatalf("%d-bit sum: %+v", tt.bits, f.Zero)
		}
	}
}

// TestPairFlags: after a pair's ADD and ADC the flags give the pair's sign
// and carry, which stay the pair's: its low word, a 32-bit narrowing, does
// not inherit the 64-bit sign.
func TestPairFlags(t *testing.T) {
	s := machine.NewStream(machine.StreamOptions{Size: func(machine.Instruction) int { return 1 }, FlagEffect: i386.FlagEffect, FlagFacts: i386.FlagFacts, ExactFlagFacts: true})
	s.Append(machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 32, A: 0, B: 0, C: 1})
	s.Append(machine.Instruction{Op: machine.OpReload, A: 1})
	s.Append(machine.Instruction{Op: machine.OpBinaryCarry, Arithmetic: op.OpAdd, A: 2, B: 2, C: 1})
	s.BindFlagValue(5, 3, 4, op.OpAdd, false)
	s.AliasFlagValue(6, 5, 32)
	f := s.Flags()
	if !f.Sign.MatchesValue(5) || !f.Carry.MatchesValue(5) || f.Zero.Valid || f.Sign.MatchesValue(6) || f.Carry.MatchesValue(6) {
		t.Fatalf("pair flags: %+v", f)
	}
}
