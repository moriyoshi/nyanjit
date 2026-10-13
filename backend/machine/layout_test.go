package machine_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/machine"
)

func TestLayoutInstructionAt(t *testing.T) {
	// OpConst stands in for a zero-size pseudo-instruction such as a label.
	sizes := map[machine.Opcode]int{machine.OpMove: 4, machine.OpConst: 0, machine.OpReturn: 2}
	stream := machine.NewStream(machine.StreamOptions{
		Size:       func(i machine.Instruction) int { return sizes[i.Op] },
		FlagEffect: func(machine.Instruction) bool { return false },
	})
	for _, op := range []machine.Opcode{machine.OpMove, machine.OpConst, machine.OpConst, machine.OpMove, machine.OpReturn, machine.OpConst} {
		stream.Append(machine.Instruction{Op: op})
	}
	layout := stream.Layout()
	if layout.Len() != 6 || layout.PC(0) != 0 || layout.PC(3) != 4 || layout.PC(5) != 10 || layout.PC(6) != 10 {
		t.Fatalf("len %d, PCs %d %d %d %d", layout.Len(), layout.PC(0), layout.PC(3), layout.PC(5), layout.PC(6))
	}
	for _, c := range []struct {
		pc, index int
		ok        bool
	}{
		{0, 0, true},
		{4, 1, true}, // the first of two zero-size instructions at 4
		{8, 4, true},
		{10, 6, true}, // the end, even after a trailing zero-size instruction
		{2, 0, false},
		{11, 0, false},
		{-1, 0, false},
	} {
		index, ok := layout.InstructionAt(c.pc)
		if ok != c.ok || ok && index != c.index {
			t.Errorf("InstructionAt(%d) = %d, %v; want %d, %v", c.pc, index, ok, c.index, c.ok)
		}
	}
	// The layout is a snapshot: later appends do not move it.
	stream.Append(machine.Instruction{Op: machine.OpMove})
	if index, ok := layout.InstructionAt(10); layout.Len() != 6 || !ok || index != 6 {
		t.Fatalf("layout followed the stream: len %d, end %d %v", layout.Len(), index, ok)
	}

	var empty machine.Layout
	if index, ok := empty.InstructionAt(0); empty.Len() != 0 || empty.PC(0) != 0 || !ok || index != 0 {
		t.Fatalf("zero layout: len %d, InstructionAt(0) = %d, %v", empty.Len(), index, ok)
	}
	if _, ok := empty.InstructionAt(4); ok {
		t.Fatal("zero layout found an instruction at 4")
	}

	// A size the target rejects breaks PC order, so no PC maps back.
	sizes[machine.OpConst] = -1
	broken := machine.NewStream(machine.StreamOptions{
		Size:       func(i machine.Instruction) int { return sizes[i.Op] },
		FlagEffect: func(machine.Instruction) bool { return false },
	})
	broken.Append(machine.Instruction{Op: machine.OpMove})
	broken.Append(machine.Instruction{Op: machine.OpConst})
	if _, ok := broken.Layout().InstructionAt(0); ok {
		t.Fatal("layout with a negative size found an instruction")
	}
}
