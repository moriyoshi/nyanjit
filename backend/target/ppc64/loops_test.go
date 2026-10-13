package ppc64_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
)

func flagStream(t *testing.T) *machine.Stream {
	t.Helper()
	profile, _ := abi.NewPPC64Profile("ppc64le", abi.C)
	emitter, err := ppc64.NewEmitterForCPU(profile, abi.StackNoUse, false, abi.PPC64Power9)
	if err != nil {
		t.Fatal(err)
	}
	return machine.NewStream(machine.StreamOptions{Size: emitter.Size, FlagEffect: emitter.FlagEffect, FlagFacts: emitter.FlagFacts})
}

func TestPPC64CTRLoopSplitsEntryAndBackedge(t *testing.T) {
	stream := flagStream(t)
	stream.Append(machine.Instruction{Op: machine.OpConst, A: 3, Imm: 5})
	stream.Append(machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAdd, A: 4, B: 4, Bits: 64, Imm: 1})
	stream.Append(machine.Instruction{Op: machine.OpDec, A: 3, B: 3})
	stream.Append(machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchNotEqual, Imm: 0})
	stream.Append(machine.Instruction{Op: machine.OpReturn})
	labels, bound, err := ppc64.Policy{}.FuseLoops(stream, []int{4}, []bool{true})
	if err != nil {
		t.Fatal(err)
	}
	insns := stream.Instructions()
	if len(insns) != 6 || insns[1].Op != machine.OpSetCount || insns[3].Op != machine.OpLoop ||
		insns[4].Op != machine.OpConst || insns[4].A != 3 || insns[4].Imm != 0 {
		t.Fatalf("CTR stream=%+v", insns)
	}
	if len(labels) != 2 || len(bound) != 2 || labels[0] != 4 || labels[1] != 8 || insns[3].Imm != 1 {
		t.Fatalf("entry/backedge labels=%v bound=%v stream=%+v", labels, bound, insns)
	}
}

func TestPPC64CTRLoopSafetyBarriers(t *testing.T) {
	for _, barrier := range []machine.Instruction{
		{Op: machine.OpMove, A: 4, B: 3},
		{Op: machine.OpConst, A: 3, Imm: 9},
		{Op: machine.OpCallC},
		{Op: machine.OpCallGo},
		{Op: machine.OpSetCount, A: 4},
		{Op: machine.OpSpill, A: 3},
		{Op: machine.OpSyscall},
	} {
		stream := flagStream(t)
		stream.Append(barrier)
		stream.Append(machine.Instruction{Op: machine.OpDec, A: 3, B: 3})
		stream.Append(machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchNotEqual, Imm: 0})
		stream.Append(machine.Instruction{Op: machine.OpReturn})
		_, _, err := ppc64.Policy{}.FuseLoops(stream, []int{0}, []bool{true})
		if err != nil {
			t.Fatal(err)
		}
		for _, ins := range stream.Instructions() {
			if ins.Op == machine.OpLoop || ins.Op == machine.OpSetCount && barrier.Op != machine.OpSetCount {
				t.Fatalf("unsafe barrier %+v fused: %+v", barrier, stream.Instructions())
			}
		}
	}
}

func TestPPC64CTRLoopPreservesLiveFlagsThroughPartialWriter(t *testing.T) {
	stream := flagStream(t)
	stream.Append(machine.Instruction{Op: machine.OpDec, A: 3, B: 3})
	stream.Append(machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchNotEqual, Imm: 0})
	// AND rewrites CR0 but preserves XER. The loop's final CA is still used.
	stream.Append(machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAnd, A: 4, B: 4, C: 5, Bits: 64})
	stream.Append(machine.Instruction{Op: machine.OpJumpIf, Condition: machine.BranchCarry, Imm: 1})
	stream.Append(machine.Instruction{Op: machine.OpReturn})
	exit := stream.PC()
	stream.Append(machine.Instruction{Op: machine.OpReturn})
	_, _, err := ppc64.Policy{}.FuseLoops(stream, []int{0, exit}, []bool{true, true})
	if err != nil {
		t.Fatal(err)
	}
	if stream.Instructions()[0].Op != machine.OpDec {
		t.Fatalf("partial CR writer hid live XER carry: %+v", stream.Instructions())
	}
}

func TestPPC64CTRLoopRejectsSideEntryAndEarlyExit(t *testing.T) {
	for _, sideEntry := range []bool{false, true} {
		stream := flagStream(t)
		stream.Append(machine.Instruction{Op: machine.OpConst, A: 3, Imm: 5})
		if sideEntry {
			stream.Append(machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchEqual, Imm: 1})
		}
		header := stream.PC()
		stream.Append(machine.Instruction{Op: machine.OpMove, A: 4, B: 5})
		middle := stream.PC()
		if !sideEntry {
			stream.Append(machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchEqual, Imm: 1})
		} else {
			stream.Append(machine.Instruction{Op: machine.OpMove, A: 4, B: 5})
		}
		stream.Append(machine.Instruction{Op: machine.OpDec, A: 3, B: 3})
		stream.Append(machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchNotEqual, Imm: 0})
		exit := stream.PC()
		stream.Append(machine.Instruction{Op: machine.OpReturn})
		second := exit
		if sideEntry {
			second = middle
		}
		_, _, err := ppc64.Policy{}.FuseLoops(stream, []int{header, second}, []bool{true, true})
		if err != nil {
			t.Fatal(err)
		}
		for _, ins := range stream.Instructions() {
			if ins.Op == machine.OpLoop {
				t.Fatalf("sideEntry=%t fused unsafe CFG: %+v", sideEntry, stream.Instructions())
			}
		}
	}
}

func TestPPC64CTRNestedLoopsUseOneDedicatedCounter(t *testing.T) {
	stream := flagStream(t)
	stream.Append(machine.Instruction{Op: machine.OpConst, A: 3, Imm: 2})
	outer := stream.PC()
	stream.Append(machine.Instruction{Op: machine.OpConst, A: 4, Imm: 3})
	inner := stream.PC()
	stream.Append(machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAdd, Bits: 64, A: 5, B: 5, Imm: 1})
	stream.Append(machine.Instruction{Op: machine.OpDec, A: 4, B: 4})
	stream.Append(machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchNotEqual, Imm: 1})
	stream.Append(machine.Instruction{Op: machine.OpDec, A: 3, B: 3})
	stream.Append(machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchNotEqual, Imm: 0})
	stream.Append(machine.Instruction{Op: machine.OpReturn})
	_, _, err := ppc64.Policy{}.FuseLoops(stream, []int{outer, inner}, []bool{true, true})
	if err != nil {
		t.Fatal(err)
	}
	loops, outerDec := 0, false
	for _, ins := range stream.Instructions() {
		if ins.Op == machine.OpLoop {
			loops++
		}
		outerDec = outerDec || ins.Op == machine.OpDec && ins.A == 3
	}
	if loops != 1 || !outerDec {
		t.Fatalf("nested loops reused the same CTR simultaneously: %+v", stream.Instructions())
	}
}

func TestPPC64CTRLoopRejectsLiveDecrementCarryBeforeCompare(t *testing.T) {
	stream := flagStream(t)
	stream.Append(machine.Instruction{Op: machine.OpDec, A: 3, B: 3})
	stream.Append(machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAnd, Bits: 64, A: 4, B: 4, C: 5})
	stream.Append(machine.Instruction{Op: machine.OpJumpIf, Condition: machine.BranchCarry, Imm: 1})
	skip := stream.PC()
	stream.Append(machine.Instruction{Op: machine.OpMove, A: 4, B: 5})
	stream.Append(machine.Instruction{Op: machine.OpCompareImmediate, Bits: 64, A: 3, Imm: 0})
	stream.Append(machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchNotEqual, Imm: 0})
	stream.Append(machine.Instruction{Op: machine.OpReturn})
	_, _, err := ppc64.Policy{}.FuseLoops(stream, []int{0, skip}, []bool{true, true})
	if err != nil {
		t.Fatal(err)
	}
	for _, ins := range stream.Instructions() {
		if ins.Op == machine.OpLoop {
			t.Fatal("CTR fusion removed live decrement carry")
		}
	}
}
