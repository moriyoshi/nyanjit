package amd64_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/amd64"
)

// FuseLoops reports malformed label tables as machine.ErrInvalidBranch, like
// the other targets' loop fusers.
func TestFuseLoopsInvalidLabels(t *testing.T) {
	stream := machine.NewStream(machine.StreamOptions{})
	if _, _, err := (amd64.Policy{}).FuseLoops(stream, []int{0}, nil); !errors.Is(err, machine.ErrInvalidBranch) {
		t.Fatalf("mismatched labels: %v", err)
	}
	if _, _, err := (amd64.Policy{}).FuseLoops(nil, nil, nil); !errors.Is(err, machine.ErrInvalidBranch) {
		t.Fatalf("nil stream: %v", err)
	}
}

// Fusing DEC RCX; JNZ into LOOP drops the decrement's flag writes, so a later
// read of ZF must keep it even when a CF-only writer such as a rotate comes
// first (security audit 2026-10-25, L13).
func TestFuseLoopsKeepsFlagsReadPastPartialWriters(t *testing.T) {
	size := func(i machine.Instruction) int { return amd64.PhysicalSize(i, abi.StackPlatform, 0) }
	exact := machine.StreamOptions{Size: size, FlagEffect: amd64.FlagEffect, FlagFacts: amd64.FlagFacts, ExactFlagFacts: true}
	for _, tc := range []struct {
		name  string
		after machine.Instruction
		fuse  bool
	}{
		{"rotate", machine.Instruction{Op: machine.OpShift, Arithmetic: op.OpRotateLeft, Bits: 64, A: 2, B: 2, Imm: 3}, false},
		{"add", machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAdd, Bits: 64, A: 2, B: 2, Imm: 3}, true},
	} {
		for name, options := range map[string]machine.StreamOptions{"exact": exact, "size only": {Size: size}} {
			s := machine.NewStream(options)
			for _, i := range []machine.Instruction{
				{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAdd, Bits: 64, A: 2, B: 2, Imm: 1},
				{Op: machine.OpDec, A: 1, B: 1},
				{Op: machine.OpJumpIf, Condition: op.BranchNotEqual, Imm: 0},
				tc.after,
				{Op: machine.OpJumpIf, Condition: op.BranchEqual, Imm: 1},
				{Op: machine.OpReturn},
			} {
				s.Append(i)
			}
			exit := 0
			for n := range 5 {
				exit += s.SizeAt(n)
			}
			if _, _, err := (amd64.Policy{}).FuseLoops(s, []int{0, exit}, []bool{true, true}); err != nil {
				t.Fatalf("%s %s: %v", tc.name, name, err)
			}
			if fused := s.Instructions()[1].Op == machine.OpLoop; fused != tc.fuse && name == "exact" {
				t.Errorf("%s: fused %v, want %v", tc.name, fused, tc.fuse)
			}
			if name == "size only" && s.Instructions()[1].Op == machine.OpLoop && tc.name == "rotate" {
				t.Errorf("rotate on a size-only stream: fused although ZF is read")
			}
		}
	}
}
