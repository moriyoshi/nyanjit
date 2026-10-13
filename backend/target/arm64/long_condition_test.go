package arm64

import (
	"encoding/binary"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func TestLongConditionalBranch(t *testing.T) {
	policy := NewPolicy(abi.C, abi.StackNoUse)
	for condition := op.BranchEqual; condition <= machine.BranchNotSign; condition++ {
		for _, delta := range []int{-(1 << 27), -(1 << 20) - 4, 1 << 20, (1 << 27) - 4} {
			ins := machine.Instruction{Op: machine.OpJumpIf, Condition: condition, Long: true}
			source := 1 << 28
			target := source + 4 + delta
			if !policy.BranchFits(ins, source, target, 8) {
				t.Fatalf("long branch cannot reach delta%d", delta)
			}
			a := NewByteAssembler(abi.StackNoUse, 0)
			a.SetPC(source)
			a.JumpIfForm(condition, target, true)
			code := a.Bytes()
			if len(code) != PhysicalSize(ins, abi.StackNoUse, 0) {
				t.Fatalf("wrong size: %d", len(code))
			}
			skip := binary.LittleEndian.Uint32(code[:4])
			jump := binary.LittleEndian.Uint32(code[4:])
			short := NewByteAssembler(abi.StackNoUse, 0)
			short.SetPC(source)
			short.JumpIf(condition^1, source+8)
			if skip != binary.LittleEndian.Uint32(short.Bytes()) || jump != 0x14000000|uint32(delta/4)&0x3ffffff {
				t.Fatalf("wrong long encoding %x", code)
			}
		}
	}
	for _, delta := range []int{-(1 << 27) - 4, 1 << 27} {
		if policy.BranchFits(machine.Instruction{Op: machine.OpJumpIf, Long: true}, 0, 4+delta, 8) {
			t.Fatalf("out-of-range long accepted: %d", delta)
		}
	}
}
