package riscv64

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func TestConditionalBranchForms(t *testing.T) {
	policy := NewPolicy(abi.C, abi.StackPlatform)
	short := machine.Instruction{Op: machine.OpJumpIf}
	long := short
	long.Long = true
	far := long
	far.Far = true
	if !policy.BranchFits(short, 0, 4094, 4) || policy.BranchFits(short, 0, 4096, 4) ||
		!policy.BranchFits(long, 0, 4096, 8) || policy.BranchFits(long, 0, 1<<20+8192, 8) ||
		!policy.BranchFits(far, 0, 1<<20+8192, 12) {
		t.Fatal("incorrect conditional branch reach")
	}
	for _, test := range []struct {
		name, first string
		long, far   bool
		words       int
	}{
		{"short", "bltu ", false, false, 1},
		{"long", "bgeu ", true, false, 2},
		{"far", "auipc x30", true, true, 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := NewTextAssembler(abi.StackPlatform, 0)
			a.CompareRegister(10, 11)
			a.SetPC(0)
			a.JumpIfRange(op.BranchBelow, 16, test.long, test.far)
			if got := len(a.Bytes()) / 4; got != test.words {
				t.Fatalf("branch has %d words, want %d", got, test.words)
			}
			if !strings.Contains(a.Text(), test.first) {
				t.Fatalf("branch listing:\n%s", a.Text())
			}
		})
	}
}

func TestFarBranchesRelaxToNearestForm(t *testing.T) {
	policy := NewPolicy(abi.C, abi.StackPlatform)
	for _, test := range []struct {
		name    string
		op      machine.Opcode
		padding int
		words   int
		far     bool
		long    bool
	}{
		{"near conditional", machine.OpJumpIf, 0, 1, false, false},
		{"medium conditional", machine.OpJumpIf, 6000, 2, false, true},
		{"far conditional", machine.OpJumpIf, 1<<20 + 8192, 3, true, true},
		{"near unconditional", machine.OpJump, 0, 1, false, false},
		{"far unconditional", machine.OpJump, 1<<20 + 8192, 2, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			stream := machine.NewStream(machine.StreamOptions{
				Size:       func(ins machine.Instruction) int { return PhysicalSize(ins, abi.StackPlatform, 0) },
				FlagEffect: FlagEffect,
			})
			stream.Append(machine.Instruction{Op: test.op, Imm: 0, Long: true, Far: test.op == machine.OpJumpIf})
			for covered := 0; covered < test.padding; covered += 60 {
				stream.Append(machine.Instruction{Op: machine.OpSymbol})
			}
			labels, err := machine.RelaxBranches(stream, []int{stream.PC()}, []bool{true}, policy.BranchFits)
			if err != nil {
				t.Fatal(err)
			}
			got := stream.Instructions()[0]
			if got.Long != test.long || got.Far != test.far || PhysicalSize(got, abi.StackPlatform, 0) != test.words*4 {
				t.Fatalf("relaxed branch = %+v, want %d words long=%t far=%t", got, test.words, test.long, test.far)
			}
			if _, err := machine.ResolveBranches(stream, labels, []bool{true}, policy.BranchFits); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFarBranchVisitorUsesSelectedForm(t *testing.T) {
	for _, test := range []struct {
		op      machine.Opcode
		wantLen int
	}{
		{machine.OpJump, 8},
		{machine.OpJumpIf, 12},
	} {
		a := NewTextAssembler(abi.StackPlatform, 0)
		a.CompareRegister(10, 11)
		a.SetPC(0)
		ins := machine.Instruction{Op: test.op, Condition: op.BranchEqual, Imm: 1<<20 + 8192, Long: true, Far: test.op == machine.OpJumpIf}
		if err := machine.Visit(ins, a); err != nil {
			t.Fatal(err)
		}
		if got := a.AssembledSize(); got != test.wantLen || !strings.Contains(a.Text(), "auipc x30") || !strings.Contains(a.Text(), "jalr x0") {
			t.Fatalf("far branch %d: size=%d listing:\n%s", test.op, got, a.Text())
		}
	}
}

func TestCompareZeroUsesZeroRegister(t *testing.T) {
	ins := machine.Instruction{Op: machine.OpCompareImmediate, A: 10}
	if got := PhysicalSize(ins, abi.StackPlatform, 0); got != 0 {
		t.Fatalf("zero comparison size = %d, want 0", got)
	}
	a := NewTextAssembler(abi.StackPlatform, 0)
	a.CompareImmediate(10, 0)
	if got := a.AssembledSize(); got != 0 {
		t.Fatalf("zero comparison assembled %d bytes, want 0", got)
	}
	a.SetPC(0)
	a.JumpIfForm(op.BranchEqual, 16, false)
	if got := a.Text(); !strings.Contains(got, "beq x10, x0, 0x10") {
		t.Fatalf("zero comparison branch = %q", got)
	}
}
