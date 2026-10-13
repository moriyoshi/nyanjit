package amd64

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func deadFlagStream(code ...machine.Instruction) *machine.Stream {
	s := machine.NewStream(machine.StreamOptions{
		Size:           func(i machine.Instruction) int { return PhysicalSize(i, abi.StackPlatform, 0) },
		FlagEffect:     FlagEffect,
		FlagFacts:      FlagFacts,
		ExactFlagFacts: true,
	})
	for _, i := range code {
		s.Append(i)
	}
	return s
}

func TestElideDeadFlagsSelectsLEA(t *testing.T) {
	add := machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 64, A: 0, B: 7, C: 6}
	sub := machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpSub, Bits: 64, A: 1, B: 7, Imm: 8}
	inc := machine.Instruction{Op: machine.OpInc, A: 2, B: 6}
	carry := machine.Instruction{Op: machine.OpCarryBit, Bits: 64, Arithmetic: op.OpAdd, CarryMode: machine.CarryFlags, A: 3}
	compare := machine.Instruction{Op: machine.OpCompareRegister, Bits: 64, A: 7, B: 6}
	branch := machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchEqual, Imm: 0}
	ret := machine.Instruction{Op: machine.OpReturn}
	for _, tc := range []struct {
		name  string
		code  []machine.Instruction
		want  []bool
		label int // index of the instruction label 0 names, or -1
	}{
		{"return", []machine.Instruction{add, sub, ret}, []bool{true, true, false}, -1},
		{"branch reads", []machine.Instruction{add, branch, ret}, []bool{false, false, false}, 2},
		{"overwritten", []machine.Instruction{add, compare, branch, ret}, []bool{true, false, false, false}, 3},
		// INC leaves CF alone, so a carry read behind it does not need it.
		{"carry passes inc", []machine.Instruction{compare, inc, carry, ret}, []bool{false, true, false, false}, -1},
		{"carry read", []machine.Instruction{sub, carry, ret}, []bool{false, false, false}, -1},
		{"opaque bytes", []machine.Instruction{add, {Op: machine.OpLiteralData, Data: []byte{0x90}}, ret}, []bool{false, false, false}, -1},
		{"same register", []machine.Instruction{{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAdd, Bits: 64, A: 7, B: 7, Imm: 8}, ret}, []bool{false, false}, -1},
		// The back edge carries the loop header's flag read to the add.
		{"back edge", []machine.Instruction{branch, add, {Op: machine.OpJump, Imm: 0}}, []bool{false, false, false}, 0},
		{"label after", []machine.Instruction{add, compare, branch, add, ret}, []bool{true, false, false, true, false}, 4},
		// A jump leaves the straight line; the whole graph finds the return.
		{"jump to return", []machine.Instruction{add, {Op: machine.OpJump, Imm: 0}, {Op: machine.OpLiteralData, Data: []byte{0x90}}, ret}, []bool{true, false, false, false}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := deadFlagStream(tc.code...)
			labels, bound := []int{0}, []bool{false}
			if tc.label >= 0 {
				pc := 0
				for _, i := range tc.code[:tc.label] {
					pc += s.Size(i)
				}
				labels[0], bound[0] = pc, true
			}
			moved, err := Policy{}.ElideDeadFlags(s, labels, bound)
			if err != nil {
				t.Fatal(err)
			}
			pc := 0
			for n, i := range s.Instructions() {
				if i.PreserveFlags != tc.want[n] {
					t.Errorf("instruction %d: PreserveFlags = %v, want %v", n, i.PreserveFlags, tc.want[n])
				}
				if n == tc.label && moved[0] != pc {
					t.Errorf("label at %d, want %d", moved[0], pc)
				}
				pc += s.SizeAt(n)
			}
			if pc != s.PC() {
				t.Errorf("stream PC %d, sizes sum to %d", s.PC(), pc)
			}
		})
	}
}

func TestElideDeadFlagsRejectsUnboundTargets(t *testing.T) {
	s := deadFlagStream(
		machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 64, A: 0, B: 7, C: 6},
		machine.Instruction{Op: machine.OpJump, Imm: 0},
	)
	if _, err := (Policy{}).ElideDeadFlags(s, []int{0}, []bool{false}); err != machine.ErrInvalidBranch {
		t.Fatalf("unbound target: %v", err)
	}
}

func TestLEAEncodings(t *testing.T) {
	for _, tc := range []struct {
		ins  machine.Instruction
		want []byte
	}{
		{machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 64, A: 0, B: 7, C: 6}, []byte{0x48, 0x8d, 0x04, 0x37}},                                   // lea rax, [rdi+rsi]
		{machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 32, A: 0, B: 5, C: 1}, []byte{0x8d, 0x04, 0x29}},                                         // lea eax, [rcx+rbp]
		{machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 64, A: 9, B: 4, C: 13}, []byte{0x4e, 0x8d, 0x0c, 0x2c}},                                  // lea r9, [rsp+r13]
		{machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAdd, Bits: 64, A: 0, B: 12, Imm: 7}, []byte{0x49, 0x8d, 0x44, 0x24, 0x07}},                 // lea rax, [r12+7]
		{machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpSub, Bits: 16, A: 2, B: 13, Imm: 0x200}, []byte{0x41, 0x8d, 0x95, 0x00, 0xfe, 0xff, 0xff}}, // lea edx, [r13-0x200]
		{machine.Instruction{Op: machine.OpDec, A: 1, B: 5}, []byte{0x48, 0x8d, 0x4d, 0xff}},                                                                            // lea rcx, [rbp-1]
	} {
		tc.ins.PreserveFlags = true
		a := NewByteAssembler(abi.StackPlatform, 0)
		if err := machine.Visit(tc.ins, a); err != nil || a.Err() != nil {
			t.Fatalf("%+v: %v %v", tc.ins, err, a.Err())
		}
		if got := a.Bytes(); string(got) != string(tc.want) {
			t.Errorf("%+v: % x, want % x", tc.ins, got, tc.want)
		}
		if size := PhysicalSize(tc.ins, abi.StackPlatform, 0); size != len(tc.want) {
			t.Errorf("%+v: size %d, want %d", tc.ins, size, len(tc.want))
		}
		if f := FlagFacts(tc.ins); f.Modified != 0 {
			t.Errorf("%+v: LEA modifies flags %v", tc.ins, f.Modified)
		}
	}
}

// The pass must not trust a stream's options for flag liveness: a size-only
// stream records no flag writes, and a legacy one records INC as writing CF
// (security audit 2026-10-25, F20).
func TestElideDeadFlagsIgnoresInexactStreams(t *testing.T) {
	size := func(i machine.Instruction) int { return PhysicalSize(i, abi.StackPlatform, 0) }
	streams := map[string]machine.StreamOptions{
		"size only": {Size: size},
		"legacy":    {Size: size, FlagEffect: FlagEffect},
		"exact":     {Size: size, FlagEffect: FlagEffect, FlagFacts: FlagFacts, ExactFlagFacts: true},
	}
	add := machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 64, A: 0, B: 7, C: 6}
	branch := machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchEqual, Imm: 0}
	inc := machine.Instruction{Op: machine.OpInc, A: 2, B: 2}
	carry := machine.Instruction{Op: machine.OpCarryBit, Bits: 64, Arithmetic: op.OpAdd, CarryMode: machine.CarryFlags, A: 3}
	ret := machine.Instruction{Op: machine.OpReturn}
	for name, options := range streams {
		for _, code := range [][]machine.Instruction{{add, branch, ret}, {add, inc, carry, ret}} {
			s := machine.NewStream(options)
			for _, i := range code {
				s.Append(i)
			}
			labels, bound := []int{0}, []bool{false}
			if code[1].Op == machine.OpJumpIf {
				labels[0], bound[0] = s.SizeAt(0)+s.SizeAt(1), true
			}
			if _, err := (Policy{}).ElideDeadFlags(s, labels, bound); err != nil {
				t.Fatal(err)
			}
			if s.Instructions()[0].PreserveFlags {
				t.Errorf("%s: add before %v became LEA although its flags are read", name, code[1].Op)
			}
		}
	}
}
