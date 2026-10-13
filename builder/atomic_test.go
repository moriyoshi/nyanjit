package builder

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func TestAtomicBuilderEmitsOrderedOperations(t *testing.T) {
	b := NewBuilder()
	address, _ := b.Param()
	x, _ := b.ConstBits(16, 7)
	y, _ := b.ConstBits(16, 9)
	if _, err := b.AtomicLoad(address, 32); err != nil {
		t.Fatal(err)
	}
	if err := b.AtomicStore(address, x); err != nil {
		t.Fatal(err)
	}
	for _, f := range []func(Value, Value) (Value, error){b.AtomicAdd, b.AtomicAnd, b.AtomicOr, b.AtomicXor, b.AtomicSwap} {
		if _, err := f(address, x); err != nil {
			t.Fatal(err)
		}
	}
	old, err := b.AtomicCompareSwap(address, x, y)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Fence(); err != nil {
		t.Fatal(err)
	}
	if old.Type() != ir.Scalar(16) {
		t.Fatalf("compare-and-swap result %+v", old.Type())
	}
	wide, err := b.CastUnsigned(old, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(wide); err != nil {
		t.Fatal(err)
	}
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	var ops []ir.Opcode
	for _, ins := range p.Instructions() {
		if ins.Op >= ir.OpAtomicLoad && ins.Op <= ir.OpFence {
			ops = append(ops, ins.Op)
			if ins.Imm != uint64(ir.MemoryOrderSequential) {
				t.Errorf("%v: order %d", ins.Op, ins.Imm)
			}
		}
	}
	want := []ir.Opcode{ir.OpAtomicLoad, ir.OpAtomicStore, ir.OpAtomicAdd, ir.OpAtomicAnd, ir.OpAtomicOr, ir.OpAtomicXor, ir.OpAtomicSwap, ir.OpAtomicCompareSwap, ir.OpFence}
	if len(ops) != len(want) {
		t.Fatalf("emitted %v, want %v", ops, want)
	}
	for i := range want {
		if ops[i] != want[i] {
			t.Fatalf("emitted %v, want %v", ops, want)
		}
	}
}

func TestAtomicBuilderRejectsShapes(t *testing.T) {
	b := NewBuilder()
	address, _ := b.Param()
	narrow, _ := b.ConstBits(16, 1)
	word, _ := b.ConstBits(32, 1)
	if _, err := b.AtomicLoad(address, 12); !errors.Is(err, ErrInvalidFunction) {
		t.Errorf("12-bit load: %v", err)
	}
	if _, err := b.AtomicCompareSwap(address, narrow, word); !errors.Is(err, ErrInvalidFunction) {
		t.Errorf("mixed compare-and-swap widths: %v", err)
	}
	if NativeBits == 64 {
		if _, err := b.AtomicAdd(word, word); !errors.Is(err, ErrInvalidFunction) {
			t.Errorf("32-bit address: %v", err)
		}
	}
	if err := b.AtomicStore(address, Value{}); !errors.Is(err, ErrInvalidFunction) {
		t.Errorf("invalid operand: %v", err)
	}
}

// Atomics may change the memory an indexed view was checked against, as a
// call may.
func TestIndexedDataExpiresAtAtomic(t *testing.T) {
	b := NewBuilder()
	root, err := SliceParam[uint64](b)
	if err != nil {
		t.Fatal(err)
	}
	bad := b.NewLabel()
	i, _ := b.ConstNative(0)
	elem, err := root.Index(i, bad)
	if err != nil {
		t.Fatal(err)
	}
	address, _ := b.ConstNative(0x1000)
	if err := b.Fence(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.AtomicLoad(address, 64); err != nil {
		t.Fatal(err)
	}
	if _, err := elem.Load(); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("expired load accepted: %v", err)
	}
}

// Identical atomics are distinct operations: none is merged with another.
func TestAtomicBuilderKeepsRepeats(t *testing.T) {
	b := NewBuilder()
	address, _ := b.Param()
	one, _ := b.Const(1)
	first, _ := b.AtomicAdd(address, one)
	second, _ := b.AtomicAdd(address, one)
	third, _ := b.AtomicLoad(address, 64)
	fourth, _ := b.AtomicLoad(address, 64)
	if first.id == second.id || third.id == fourth.id {
		t.Fatal("repeated atomics share a value")
	}
	sum, _ := b.Add(first, second)
	sum, _ = b.Add(sum, third)
	sum, _ = b.Add(sum, fourth)
	if err := b.Return(sum); err != nil {
		t.Fatal(err)
	}
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, ins := range p.Instructions() {
		if ins.Op == ir.OpAtomicAdd || ins.Op == ir.OpAtomicLoad {
			count++
		}
	}
	if count != 4 {
		t.Fatalf("%d atomics, want 4", count)
	}
}
