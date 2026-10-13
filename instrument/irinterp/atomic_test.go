package irinterp_test

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/instrument/irinterp"
	"github.com/moriyoshi/nyanjit/ir"
)

// atomicMemory is on the heap: the interpreter holds its address as a word,
// which a moving goroutine stack would invalidate.
var atomicMemory = make([]uint64, 2)

func TestAtomics(t *testing.T) {
	memory := atomicMemory
	address := uint64(uintptr(unsafe.Pointer(&memory[0])))
	program := func(op ir.Opcode, bits uint8) *ir.Program {
		ins := ir.Instruction{Op: op, Bits: bits, A: 0, B: 1}
		if op == ir.OpAtomicCompareSwap {
			ins.C = 2
		}
		return &ir.Program{Instructions: []ir.Instruction{
			{Op: ir.OpParam, Bits: 64}, {Op: ir.OpParam, Bits: bits}, {Op: ir.OpParam, Bits: bits}, ins, {Op: ir.OpFence},
		}, Arity: 3, Result: 3}
	}
	memory[0] = 0x1122334455667788
	// A byte at offset 2 of the word: add wraps within the byte.
	if r := run(t, program(ir.OpAtomicAdd, 8), []uint64{address + 2, 0xf0, 0}, nil); r.Lo != 0x66 || memory[0] != 0x1122334455567788 {
		t.Fatalf("add: old %#x, memory %#x", r.Lo, memory[0])
	}
	if r := run(t, program(ir.OpAtomicCompareSwap, 16), []uint64{address + 6, 0x1122, 0xabcd}, nil); r.Lo != 0x1122 || memory[0] != 0xabcd334455567788 {
		t.Fatalf("successful compare-and-swap: old %#x, memory %#x", r.Lo, memory[0])
	}
	if r := run(t, program(ir.OpAtomicCompareSwap, 32), []uint64{address, 1, 2}, nil); r.Lo != 0x55567788 || memory[0] != 0xabcd334455567788 {
		t.Fatalf("failed compare-and-swap: old %#x, memory %#x", r.Lo, memory[0])
	}
	if r := run(t, program(ir.OpAtomicSwap, 64), []uint64{address + 8, 9, 0}, nil); r.Lo != 0 || memory[1] != 9 {
		t.Fatalf("swap: old %#x, memory %#x", r.Lo, memory[1])
	}
	// A misaligned address is invalid: the result is unspecified.
	if _, err := irinterp.Run(program(ir.OpAtomicXor, 32), []uint64{address + 2, 1, 0}, nil); !errors.Is(err, irinterp.ErrUnsupported) {
		t.Fatalf("misaligned: %v, want ErrUnsupported", err)
	}
}
