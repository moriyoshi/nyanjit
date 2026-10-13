package arm64

import (
	"slices"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

var atomicWidths = []uint8{8, 16, 32, 64}

func atomicForm(o op.Opcode, w uint8) machine.Instruction {
	return machine.Instruction{Op: machine.OpAtomic, Arithmetic: o, Bits: w, A: 3, B: 4, C: 5, D: 6}
}

// atomicWords is the expected word count of each expansion.
func atomicWords(o op.Opcode, lse bool) int {
	switch {
	case o == op.OpAtomicLoad || o == op.OpAtomicStore:
		return 1
	case lse && (o == op.OpAtomicAnd || o == op.OpAtomicCompareSwap):
		return 2
	case lse:
		return 1
	case o == op.OpAtomicSwap:
		return 4
	case o == op.OpAtomicCompareSwap:
		return 6
	}
	return 5
}

func TestAtomicFormsAssemble(t *testing.T) {
	for _, lse := range []bool{false, true} {
		for o := op.OpAtomicLoad; o <= op.OpAtomicCompareSwap; o++ {
			for _, w := range atomicWidths {
				ins := atomicForm(o, w)
				b := NewByteAssembler(abi.StackPlatform, 0)
				b.lse = lse
				if err := machine.Visit(ins, b); err != nil {
					t.Fatalf("lse=%v %v/%d: %v", lse, o, w, err)
				}
				if got, want := len(b.Bytes()), 4*atomicWords(o, lse); got != want || atomicSize(ins, lse) != got {
					t.Fatalf("lse=%v %v/%d: %d bytes, size %d, want %d", lse, o, w, got, atomicSize(ins, lse), want)
				}
				if !lse && PhysicalSize(ins, abi.StackPlatform, 0) != len(b.Bytes()) {
					t.Fatalf("%v/%d: PhysicalSize disagrees", o, w)
				}
				text := NewTextAssembler(abi.StackPlatform, 0)
				text.lse = lse
				if err := machine.Visit(ins, text); err != nil {
					t.Fatal(err)
				}
				if lines := strings.Count(text.Text(), "\n"); lines != atomicWords(o, lse) || text.AssembledSize() != len(b.Bytes()) {
					t.Fatalf("lse=%v %v/%d: text %q", lse, o, w, text.Text())
				}
				if flags := FlagFacts(ins).Modified != 0; flags != (o == op.OpAtomicCompareSwap) || FlagEffect(ins) != flags {
					t.Fatalf("%v/%d: flag effect %v", o, w, flags)
				}
			}
		}
	}
}

func TestAtomicRejects(t *testing.T) {
	cases := []machine.Instruction{
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 12, A: 3, B: 4, C: 5},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 32, A: 3, B: 4, C: 5, Imm: 1},
		{Op: machine.OpAtomic, Arithmetic: op.OpAdd, Bits: 32, A: 3, B: 4, C: 5},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 32, A: 3, B: 3, C: 5},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 32, A: 5, B: 4, C: 5},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicCompareSwap, Bits: 32, A: 6, B: 4, C: 5, D: 6},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicLoad, Bits: 64, A: 3, B: 16},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicSwap, Bits: 64, A: 17, B: 4, C: 5},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicStore, Bits: 64, B: 4, C: 31},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicLoad, Bits: 64, A: 3, B: backend.VectorRegister(1)},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicLoad, Bits: 64, A: 3, B: 4, ByteOrder: op.ByteOrderBigEndian},
	}
	for _, ins := range cases {
		for _, lse := range []bool{false, true} {
			b := NewByteAssembler(abi.StackPlatform, 0)
			b.lse = lse
			if err := machine.Visit(ins, b); err == nil || atomicSize(ins, lse) != 0 {
				t.Errorf("lse=%v %+v assembled", lse, ins)
			}
		}
	}
	// A store reads no destination, so A may name an input.
	store := machine.Instruction{Op: machine.OpAtomic, Arithmetic: op.OpAtomicStore, Bits: 8, A: 4, B: 4, C: 5}
	if PhysicalSize(store, abi.StackPlatform, 0) != 4 {
		t.Error("store with an ignored A was rejected")
	}
}

func TestAtomicImplicitWrites(t *testing.T) {
	want := map[op.Opcode][]backend.Register{
		op.OpAtomicLoad: nil, op.OpAtomicStore: nil, op.OpAtomicAdd: {16, 17}, op.OpAtomicXor: {16, 17},
		op.OpAtomicSwap: {16}, op.OpAtomicCompareSwap: {16},
	}
	for o, regs := range want {
		if got := ImplicitWrites(atomicForm(o, 32)); !slices.Equal(got, regs) {
			t.Errorf("%v: implicit writes %v, want %v", o, got, regs)
		}
	}
	if !(Policy{}).CanAtomic(op.OpAtomicCompareSwap, 8) || (Policy{}).CanAtomic(op.OpFence, 64) || (Policy{}).CanAtomic(op.OpAtomicAdd, 128) {
		t.Error("CanAtomic")
	}
}
