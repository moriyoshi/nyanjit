package arm64

import (
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// The vectors are GNU as 2.42 (aarch64-linux-gnu-as) output, read back with
// aarch64-linux-gnu-objdump -d.
func TestFlagFreeArithmeticEncodings(t *testing.T) {
	type vector struct {
		text      string
		immediate bool
		setFlags  bool
		operation op.Opcode
		width     uint8
		d, n, m   backend.Register
		value     uint64
		word      uint32
	}
	for _, v := range []vector{
		{text: "add x1, x2, x3", operation: op.OpAdd, width: 64, d: 1, n: 2, m: 3, word: 0x8b030041},
		{text: "add x1, x2, x3", operation: op.OpAdd, width: 0, d: 1, n: 2, m: 3, word: 0x8b030041},
		{text: "add w1, w2, w3", operation: op.OpAdd, width: 32, d: 1, n: 2, m: 3, word: 0x0b030041},
		{text: "add w1, w2, w3", operation: op.OpAdd, width: 8, d: 1, n: 2, m: 3, word: 0x0b030041},
		{text: "sub x4, x5, x6", operation: op.OpSub, width: 64, d: 4, n: 5, m: 6, word: 0xcb0600a4},
		{text: "sub w4, w5, w6", operation: op.OpSub, width: 32, d: 4, n: 5, m: 6, word: 0x4b0600a4},
		{text: "sub w4, w5, w6", operation: op.OpSub, width: 16, d: 4, n: 5, m: 6, word: 0x4b0600a4},
		{text: "and x7, x8, x9", operation: op.OpAnd, width: 64, d: 7, n: 8, m: 9, word: 0x8a090107},
		{text: "and w7, w8, w9", operation: op.OpAnd, width: 32, d: 7, n: 8, m: 9, word: 0x0a090107},
		{text: "add x0, x30, x29", operation: op.OpAdd, width: 64, d: 0, n: 30, m: 29, word: 0x8b1d03c0},
		{text: "adds x1, x2, x3", setFlags: true, operation: op.OpAdd, width: 64, d: 1, n: 2, m: 3, word: 0xab030041},
		{text: "subs w4, w5, w6", setFlags: true, operation: op.OpSub, width: 32, d: 4, n: 5, m: 6, word: 0x6b0600a4},
		{text: "ands x7, x8, x9", setFlags: true, operation: op.OpAnd, width: 64, d: 7, n: 8, m: 9, word: 0xea090107},

		{text: "add x1, x2, #0xfff", immediate: true, operation: op.OpAdd, width: 64, d: 1, n: 2, value: 4095, word: 0x913ffc41},
		{text: "add w1, w2, #0xfff", immediate: true, operation: op.OpAdd, width: 32, d: 1, n: 2, value: 4095, word: 0x113ffc41},
		{text: "add x1, x2, #0xfff, lsl #12", immediate: true, operation: op.OpAdd, width: 64, d: 1, n: 2, value: 4095 << 12, word: 0x917ffc41},
		{text: "add w1, w2, #0x1, lsl #12", immediate: true, operation: op.OpAdd, width: 32, d: 1, n: 2, value: 4096, word: 0x11400441},
		{text: "sub x3, x4, #0x11", immediate: true, operation: op.OpSub, width: 64, d: 3, n: 4, value: 17, word: 0xd1004483},
		{text: "sub x3, x4, #0x11", immediate: true, operation: op.OpAdd, width: 64, d: 3, n: 4, value: ^uint64(16), word: 0xd1004483},
		{text: "sub w3, w4, #0x11", immediate: true, operation: op.OpSub, width: 32, d: 3, n: 4, value: 17, word: 0x51004483},
		{text: "sub x3, x4, #0xfff, lsl #12", immediate: true, operation: op.OpSub, width: 64, d: 3, n: 4, value: 4095 << 12, word: 0xd17ffc83},
		{text: "add x0, x0, #0x7", immediate: true, operation: op.OpAdd, width: 64, d: 0, n: 0, value: 7, word: 0x91001c00},
		{text: "sub x0, x30, #0x1", immediate: true, operation: op.OpSub, width: 64, d: 0, n: 30, value: 1, word: 0xd10007c0},
		{text: "add w1, wsp, #0x2", immediate: true, operation: op.OpAdd, width: 32, d: 1, n: 31, value: 2, word: 0x11000be1},
		{text: "and x5, x6, #0xff00", immediate: true, operation: op.OpAnd, width: 64, d: 5, n: 6, value: 0xff00, word: 0x92781cc5},
		{text: "and x5, x6, #0xff00ff00ff00ff00", immediate: true, operation: op.OpAnd, width: 64, d: 5, n: 6, value: 0xff00ff00ff00ff00, word: 0x92089cc5},
		{text: "and w5, w6, #0x80", immediate: true, operation: op.OpAnd, width: 32, d: 5, n: 6, value: 0x80, word: 0x121900c5},
		{text: "and w5, w6, #0x80", immediate: true, operation: op.OpAnd, width: 8, d: 5, n: 6, value: 0x80, word: 0x121900c5},
		{text: "and w5, w6, #0xff", immediate: true, operation: op.OpAnd, width: 16, d: 5, n: 6, value: 0xff, word: 0x12001cc5},
		{text: "and w5, w6, #0xff00ff", immediate: true, operation: op.OpAnd, width: 32, d: 5, n: 6, value: 0x00ff00ff, word: 0x12009cc5},
		{text: "and x1, xzr, #0x1", immediate: true, operation: op.OpAnd, width: 64, d: 1, n: 31, value: 1, word: 0x924003e1},
		{text: "eor x1, x2, #0xff", immediate: true, operation: op.OpXor, width: 64, d: 1, n: 2, value: 0xff, word: 0xd2401c41},
		{text: "orr w1, w2, #0x80", immediate: true, operation: op.OpOr, width: 32, d: 1, n: 2, value: 0x80, word: 0x32190041},
		{text: "adds x1, x2, #0xfff", immediate: true, setFlags: true, operation: op.OpAdd, width: 64, d: 1, n: 2, value: 4095, word: 0xb13ffc41},
		{text: "subs w3, w4, #0x11", immediate: true, setFlags: true, operation: op.OpSub, width: 32, d: 3, n: 4, value: 17, word: 0x71004483},
		{text: "ands x5, x6, #0xff00", immediate: true, setFlags: true, operation: op.OpAnd, width: 64, d: 5, n: 6, value: 0xff00, word: 0xf2781cc5},
		{text: "ands w5, w6, #0x80", immediate: true, setFlags: true, operation: op.OpAnd, width: 32, d: 5, n: 6, value: 0x80, word: 0x721900c5},
		{text: "eor x1, x2, #0xff", immediate: true, setFlags: true, operation: op.OpXor, width: 64, d: 1, n: 2, value: 0xff, word: 0xd2401c41},
		{text: "orr w1, w2, #0x80", immediate: true, setFlags: true, operation: op.OpOr, width: 32, d: 1, n: 2, value: 0x80, word: 0x32190041},
	} {
		ins := machine.Instruction{Op: machine.OpBinary, Arithmetic: v.operation, Bits: v.width, A: v.d, B: v.n, C: v.m, PreserveFlags: !v.setFlags}
		if v.immediate {
			ins.Op, ins.C, ins.Imm = machine.OpBinaryImmediate, 0, v.value
		}
		e := NewByteAssembler(abi.StackNoUse, 0)
		if err := machine.Visit(ins, e); err != nil {
			t.Fatalf("%s: %v", v.text, err)
		}
		if len(e.words) != 1 || e.words[0] != v.word {
			t.Errorf("%s: words %#x, want %#08x", v.text, e.words, v.word)
		}
		if got := PhysicalSize(ins, abi.StackNoUse, 0); got != 4 {
			t.Errorf("%s: size %d", v.text, got)
		}
		facts := FlagFacts(ins)
		if writes := v.setFlags && v.operation != op.OpXor && v.operation != op.OpOr; (facts.Modified != 0) != writes {
			t.Errorf("%s: modified %v, want writes=%t", v.text, facts.Modified, writes)
		}
		text := NewTextAssembler(abi.StackNoUse, 0)
		if err := machine.Visit(ins, text); err != nil {
			t.Fatalf("%s: text: %v", v.text, err)
		}
		mnemonic, _, _ := strings.Cut(v.text, " ")
		if got, _, _ := strings.Cut(strings.TrimSpace(text.Text()[6:]), " "); got != mnemonic {
			t.Errorf("%s: text %q", v.text, text.Text())
		}
	}
	// Without the S bit register 31 is SP as an immediate destination.
	for _, operation := range []op.Opcode{op.OpAdd, op.OpSub, op.OpAnd} {
		e := NewByteAssembler(abi.StackNoUse, 0)
		ins := machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: operation, Bits: 64, A: 31, B: 1, Imm: 1, PreserveFlags: true}
		if err := machine.Visit(ins, e); !errors.Is(err, machine.ErrInvalidInstruction) || len(e.words) != 0 {
			t.Errorf("op%d to XZR without flags: %v, %#x", operation, err, e.words)
		}
	}
}

func deadFlagStream(code ...machine.Instruction) *machine.Stream {
	s := machine.NewStream(machine.StreamOptions{
		Size:           func(i machine.Instruction) int { return PhysicalSize(i, abi.StackNoUse, 0) },
		FlagFacts:      FlagFacts,
		ExactFlagFacts: true,
	})
	for _, i := range code {
		s.Append(i)
	}
	return s
}

func TestElideDeadFlags(t *testing.T) {
	add := machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 64, A: 0, B: 1, C: 2}
	subImm := machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpSub, Bits: 32, A: 0, B: 0, Imm: 1}
	andImm := machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAnd, Bits: 64, A: 3, B: 0, Imm: 0xff}
	or := machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpOr, Bits: 64, A: 0, B: 1, C: 2}
	dec := machine.Instruction{Op: machine.OpDec, A: 4, B: 4}
	cmp := machine.Instruction{Op: machine.OpCompareImmediate, A: 0, Imm: 0, Bits: 64}
	ret := machine.Instruction{Op: machine.OpReturn}
	jumpIf := func(label uint64) machine.Instruction {
		return machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchNotEqual, Imm: label}
	}
	jump := func(label uint64) machine.Instruction { return machine.Instruction{Op: machine.OpJump, Imm: label} }
	toXZR := andImm
	toXZR.A = 31
	for _, tc := range []struct {
		name   string
		code   []machine.Instruction
		labels []int // label byte PCs; every label is bound
		want   []bool
	}{
		{"returned", []machine.Instruction{add, subImm, andImm, dec, ret}, nil, []bool{true, true, true, true, false}},
		{"end of body", []machine.Instruction{add}, nil, []bool{true}},
		{"branch reads", []machine.Instruction{add, jumpIf(0), ret}, []int{8}, []bool{false, false, false}},
		{"compare redefines", []machine.Instruction{subImm, cmp, jumpIf(0), ret}, []int{12}, []bool{true, false, false, false}},
		{"later writer", []machine.Instruction{add, andImm, jumpIf(0), ret}, []int{12}, []bool{true, false, false, false}},
		{"select reads", []machine.Instruction{andImm, {Op: machine.OpSelectFlags, A: 0, C: 1, D: 2, Condition: op.BranchEqual}, ret}, nil, []bool{false, false, false}},
		{"carry reads", []machine.Instruction{add, {Op: machine.OpWordCarry, Arithmetic: op.OpAdd, Bits: 64, A: 3, B: 4, C: 5, CarryMode: machine.CarryFlags}, ret}, nil, []bool{false, false, false}},
		{"carry bit reads", []machine.Instruction{subImm, {Op: machine.OpCarryBit, Arithmetic: op.OpSub, Bits: 64, A: 3, CarryMode: machine.CarryFlags}, ret}, nil, []bool{false, false, false}},
		{"carry operands redefine", []machine.Instruction{add, {Op: machine.OpWordCarry, Arithmetic: op.OpAdd, Bits: 64, A: 3, B: 4, C: 5, D: 6, E: 7, CarryMode: machine.CarryOperands}, ret}, nil, []bool{true, false, false}},
		{"literal data may read", []machine.Instruction{add, {Op: machine.OpLiteralData, Data: []byte{0, 0, 0, 0}}}, nil, []bool{false, false}},
		// The flags reach the reader through a jump to a label.
		{"read across jump", []machine.Instruction{dec, jump(0), ret, jumpIf(1), ret}, []int{12, 20}, []bool{false, false, false, false, false}},
		// A zero test does not read NZCV, so the decrement's flags die.
		{"branch zero", []machine.Instruction{dec, {Op: machine.OpBranchZero, A: 4, Bits: 64, Condition: op.BranchNotEqual, Imm: 0}, ret}, []int{0}, []bool{true, false, false}},
		// A loop back edge carries the flags to a reader at its head.
		{"loop head reads", []machine.Instruction{jumpIf(1), add, jump(0), ret}, []int{0, 12}, []bool{false, false, false, false}},
		{"not a candidate", []machine.Instruction{or, toXZR, ret}, nil, []bool{false, false, false}},
	} {
		s := deadFlagStream(tc.code...)
		bound := make([]bool, len(tc.labels))
		for i := range bound {
			bound[i] = true
		}
		pc := s.PC()
		labels, err := (Policy{}).ElideDeadFlags(s, tc.labels, bound)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(labels) != len(tc.labels) || s.PC() != pc {
			t.Fatalf("%s: layout moved: %v, pc %d -> %d", tc.name, labels, pc, s.PC())
		}
		for n, i := range s.Instructions() {
			if i.PreserveFlags != tc.want[n] {
				t.Errorf("%s: instruction %d PreserveFlags=%t, want %t", tc.name, n, i.PreserveFlags, tc.want[n])
			}
			if i.PreserveFlags && s.ModifiedFlagsAt(n) != 0 {
				t.Errorf("%s: instruction %d still records flag writes", tc.name, n)
			}
		}
	}
	if _, err := (Policy{}).ElideDeadFlags(deadFlagStream(add, jump(0), ret), nil, nil); !errors.Is(err, machine.ErrInvalidBranch) {
		t.Fatalf("unknown label: %v", err)
	}
}
