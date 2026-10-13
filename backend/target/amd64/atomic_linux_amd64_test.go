//go:build linux && amd64

package amd64

import (
	"sync"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// atomicRegisterSets place the address, operand and replacement, which the
// C entry receives in RDI, RSI and RDX, in B, C and D (moves in order), and
// the result in A. They cover the argument registers themselves, extended
// registers, an 8-bit SIL result and A equal to each input.
var atomicRegisterSets = []struct {
	a, b, c, d backend.Register
	moves      [][2]backend.Register // dst, src
}{
	{a: 1, b: 7, c: 6, d: 2},
	{a: 10, b: 8, c: 9, d: 1, moves: [][2]backend.Register{{8, 7}, {9, 6}, {1, 2}}},
	{a: 6, b: 1, c: 8, d: 7, moves: [][2]backend.Register{{1, 7}, {8, 6}, {7, 2}}},
	// A is written last, so it may be an input.
	{a: 7, b: 7, c: 6, d: 2},
	{a: 6, b: 7, c: 6, d: 2},
	{a: 2, b: 7, c: 6, d: 2},
}

// buildAtomic returns a C entry f(address, operand, replacement) running ins
// and returning A (0 for a store).
func buildAtomic(t *testing.T, ins machine.Instruction, moves [][2]backend.Register) *execution.Code {
	t.Helper()
	e := NewByteAssembler(abi.StackNoUse, 0)
	for _, m := range moves {
		e.Move(m[0], m[1])
	}
	if err := machine.Visit(ins, e); err != nil {
		t.Fatalf("%+v: %v", ins, err)
	}
	if ins.Arithmetic == op.OpAtomicStore {
		e.Constant(0, 0)
	} else {
		e.Move(0, ins.A)
	}
	e.Ret()
	if e.Err() != nil {
		t.Fatalf("%+v: %v", ins, e.Err())
	}
	code, err := execution.NewCode(e.Bytes(), execution.CodeOptions{ABI: abi.C, Arity: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { code.Close() })
	return code
}

// atomicReference returns the new memory value and the result of o.
func atomicReference(o op.Opcode, w uint8, old, x, y uint64) (uint64, uint64) {
	mask := ^uint64(0) >> (64 - w)
	switch o {
	case op.OpAtomicLoad:
		return old, old
	case op.OpAtomicStore:
		return x & mask, 0
	case op.OpAtomicAdd:
		return (old + x) & mask, old
	case op.OpAtomicAnd:
		return old & x, old
	case op.OpAtomicOr:
		return old | x, old
	case op.OpAtomicXor:
		return old ^ x, old
	case op.OpAtomicSwap:
		return x, old
	}
	if old == x {
		return y, old
	}
	return old, old
}

// atomicHeap keeps test memory on the heap: JIT code reaches it through a
// uintptr, so it must not live on a stack that may move, and Go must not
// keep its contents in registers across a call.
var atomicHeap []*[4]uint64

func atomicTestMemory() *[4]uint64 {
	m := new([4]uint64)
	atomicHeap = append(atomicHeap, m)
	return m
}

func TestAtomicExecution(t *testing.T) {
	memory := atomicTestMemory()
	base := uintptr(unsafe.Pointer(&memory[1]))
	for o := op.OpAtomicLoad; o <= op.OpAtomicCompareSwap; o++ {
		for _, w := range []uint8{8, 16, 32, 64} {
			mask := ^uint64(0) >> (64 - w)
			values := []uint64{0, 1, 2, mask, mask - 1, mask >> 1, mask>>1 + 1, 0xdeadbeefcafef00d & mask, 0x0123456789abcdef & mask}
			for _, r := range atomicRegisterSets {
				ins := machine.Instruction{Op: machine.OpAtomic, Arithmetic: o, Bits: w, A: r.a, B: r.b, C: r.c, D: r.d}
				code := buildAtomic(t, ins, r.moves)
				for offset := uintptr(0); offset < 8; offset += uintptr(w / 8) {
					shift := 8 * offset
					for _, old := range values {
						for _, x := range values {
							y := ^x & mask
							if o == op.OpAtomicCompareSwap && x == values[1] {
								x = old // a successful swap
							}
							fill := uint64(0xa5a5a5a5a5a5a5a5)
							*memory = [4]uint64{fill, fill&^(mask<<shift) | old<<shift, fill, fill}
							got, err := code.Call(base+offset, uintptr(x), uintptr(y))
							if err != nil {
								t.Fatal(err)
							}
							next, result := atomicReference(o, w, old, x, y)
							want := fill&^(mask<<shift) | next<<shift
							if uint64(got) != result || memory[1] != want || memory[0] != fill || memory[2] != fill {
								t.Fatalf("%v/%d regs %+v at +%d: old %#x, x %#x, y %#x: got %#x memory %#x, want %#x memory %#x",
									o, w, r, offset, old, x, y, got, memory[1], result, want)
							}
						}
					}
				}
			}
		}
	}
}

// TestAtomicCounterStress adds from several goroutines at once and checks
// that no increment is lost, then builds a lock-free increment from
// compare-and-swap.
func TestAtomicCounterStress(t *testing.T) {
	const workers, rounds = 4, 5000
	call := func(code *execution.Code, args ...uintptr) uintptr {
		r, err := code.Call(args...)
		if err != nil {
			panic(err)
		}
		return r
	}
	for _, w := range []uint8{8, 16, 32, 64} {
		counter := atomicTestMemory()
		address := uintptr(unsafe.Pointer(&counter[0]))
		add := buildAtomic(t, machine.Instruction{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: w, A: 1, B: 7, C: 6}, nil)
		cas := buildAtomic(t, machine.Instruction{Op: machine.OpAtomic, Arithmetic: op.OpAtomicCompareSwap, Bits: w, A: 1, B: 7, C: 6, D: 2}, nil)
		load := buildAtomic(t, machine.Instruction{Op: machine.OpAtomic, Arithmetic: op.OpAtomicLoad, Bits: w, A: 1, B: 7}, nil)
		mask := ^uint64(0) >> (64 - w)
		var wg sync.WaitGroup
		for range workers {
			wg.Add(2)
			go func() {
				defer wg.Done()
				for range rounds {
					call(add, address, 3, 0)
				}
			}()
			go func() {
				defer wg.Done()
				for range rounds {
					for {
						old := call(load, address, 0, 0)
						got := call(cas, address, old, uintptr((uint64(old)+5)&mask))
						if got == old {
							break
						}
					}
				}
			}()
		}
		wg.Wait()
		if want := uint64(workers*rounds*8) & mask; counter[0] != want || counter[1] != 0 {
			t.Fatalf("%d bits: counter %#x, want %#x", w, counter[0], want)
		}
	}
}
