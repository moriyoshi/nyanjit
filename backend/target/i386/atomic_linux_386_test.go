//go:build linux && 386

package i386_test

import (
	"sync"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
)

// buildAtomic returns a C entry of five words running ins. Up to 32 bits
// the first three arguments go to B, C and D and the entry returns A; at 64
// bits the address and the two pairs go to slots Slot..Slot+4 and the entry
// returns EDX:EAX (zero for a store).
func buildAtomic(t *testing.T, ins machine.Instruction) func(args ...uintptr) uint64 {
	t.Helper()
	e := i386.NewByteAssembler(abi.StackPlatform)
	e.FrameEnter(expansionFrame)
	if ins.Bits == 64 {
		for n := range 5 {
			e.IncomingWord(0, 4+4*n)
			e.Spill(0, ins.Slot+n)
		}
	} else {
		for n, r := range []backend.Register{ins.B, ins.C, ins.D} {
			e.IncomingWord(r, 4+4*n)
		}
	}
	if err := machine.Visit(ins, e); err != nil {
		t.Fatalf("%+v: %v", ins, err)
	}
	switch {
	case ins.Arithmetic == op.OpAtomicStore:
		e.Constant(0, 0)
		e.Constant(2, 0)
	case ins.Bits != 64:
		e.Move(0, ins.A)
		e.Constant(2, 0)
	}
	e.FrameLeave(expansionFrame)
	e.Ret()
	if e.Err() != nil {
		t.Fatalf("%+v: %v", ins, e.Err())
	}
	code, err := execution.NewCode(e.Bytes(), execution.CodeOptions{ABI: abi.C, Arity: 5, ArgumentWords: 5}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { code.Close() })
	return func(args ...uintptr) uint64 {
		for len(args) < 5 {
			args = append(args, 0)
		}
		lo, hi, err := code.CallPair(args...)
		if err != nil {
			t.Fatal(err)
		}
		return execution.Wide(lo, hi)
	}
}

// aligned8 returns n words at an 8-aligned address: a []uint64 is only
// 4-aligned on 386, and 64-bit atomics need natural alignment.
// The words stay on the heap (atomicHeap): JIT code reaches them through a
// uintptr, so they must not live on a stack that may move.
func aligned8(n int) []uint64 {
	buf := make([]byte, 8*n+8)
	atomicHeap = append(atomicHeap, buf)
	skip := (8 - uintptr(unsafe.Pointer(&buf[0]))%8) % 8
	return unsafe.Slice((*uint64)(unsafe.Pointer(&buf[skip])), n)
}

var atomicHeap [][]byte

// callAtomic passes address, x and y in the order buildAtomic expects at width w.
func callAtomic(run func(...uintptr) uint64, w uint8, address uintptr, x, y uint64) uint64 {
	if w == 64 {
		return run(address, uintptr(x), uintptr(x>>32), uintptr(y), uintptr(y>>32))
	}
	return run(address, uintptr(x), uintptr(y))
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

func TestAtomicExecution(t *testing.T) {
	memory := aligned8(4)
	base := uintptr(unsafe.Pointer(&memory[1]))
	sets := [][4]backend.Register{{3, 6, 7, 1}, {1, 3, 6, 7}, {7, 5, 1, 3}, {6, 6, 7, 1}, {7, 6, 7, 1}, {1, 6, 7, 1}}
	for o := op.OpAtomicLoad; o <= op.OpAtomicCompareSwap; o++ {
		for _, w := range []uint8{8, 16, 32, 64} {
			mask := ^uint64(0) >> (64 - w)
			values := []uint64{0, 1, 2, mask, mask - 1, mask >> 1, mask>>1 + 1, 0xdeadbeefcafef00d & mask, 0x0123456789abcdef & mask, 0xffffffff & mask, 0x100000000 & mask}
			var runs []func(...uintptr) uint64
			if w == 64 {
				runs = append(runs, buildAtomic(t, machine.Instruction{Op: machine.OpAtomic, Arithmetic: o, Bits: 64, A: 0, HighA: 2, Slot: 1}))
			} else {
				for _, r := range sets {
					runs = append(runs, buildAtomic(t, machine.Instruction{Op: machine.OpAtomic, Arithmetic: o, Bits: w, A: r[0], B: r[1], C: r[2], D: r[3]}))
				}
			}
			for n, run := range runs {
				for offset := uintptr(0); offset < 8; offset += uintptr(w / 8) {
					shift := 8 * offset
					for _, old := range values {
						for _, x := range values {
							y := ^x & mask
							if o == op.OpAtomicCompareSwap && x == values[1] {
								x = old // a successful swap
							}
							fill := uint64(0xa5a5a5a5a5a5a5a5)
							memory[0], memory[1], memory[2] = fill, fill&^(mask<<shift)|old<<shift, fill
							got := callAtomic(run, w, base+offset, x, y)
							next, result := atomicReference(o, w, old, x, y)
							want := fill&^(mask<<shift) | next<<shift
							if got != result || memory[1] != want || memory[0] != fill || memory[2] != fill {
								t.Fatalf("%v/%d set %d at +%d: old %#x, x %#x, y %#x: got %#x memory %#x, want %#x memory %#x",
									o, w, n, offset, old, x, y, got, memory[1], result, want)
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
// compare-and-swap; at 64 bits both use CMPXCHG8B.
func TestAtomicCounterStress(t *testing.T) {
	const workers, rounds = 4, 5000
	counter := aligned8(2)
	address := uintptr(unsafe.Pointer(&counter[0]))
	for _, w := range []uint8{8, 16, 32, 64} {
		counter[0], counter[1] = 0, 0
		var add, cas, load func(...uintptr) uint64
		if w == 64 {
			add = buildAtomic(t, machine.Instruction{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 64, A: 0, HighA: 2})
			cas = buildAtomic(t, machine.Instruction{Op: machine.OpAtomic, Arithmetic: op.OpAtomicCompareSwap, Bits: 64, A: 0, HighA: 2})
			load = buildAtomic(t, machine.Instruction{Op: machine.OpAtomic, Arithmetic: op.OpAtomicLoad, Bits: 64, A: 0, HighA: 2})
		} else {
			add = buildAtomic(t, machine.Instruction{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: w, A: 3, B: 6, C: 7})
			cas = buildAtomic(t, machine.Instruction{Op: machine.OpAtomic, Arithmetic: op.OpAtomicCompareSwap, Bits: w, A: 3, B: 6, C: 7, D: 1})
			load = buildAtomic(t, machine.Instruction{Op: machine.OpAtomic, Arithmetic: op.OpAtomicLoad, Bits: w, A: 3, B: 6})
		}
		mask := ^uint64(0) >> (64 - w)
		// Operands that carry into the high word exercise the pair arithmetic.
		step := uint64(3)
		if w == 64 {
			step = 0x1_0000_0003
		}
		var wg sync.WaitGroup
		for range workers {
			wg.Add(2)
			go func() {
				defer wg.Done()
				for range rounds {
					callAtomic(add, w, address, step, 0)
				}
			}()
			go func() {
				defer wg.Done()
				for range rounds {
					for {
						old := callAtomic(load, w, address, 0, 0)
						if callAtomic(cas, w, address, old, (old+5)&mask) == old {
							break
						}
					}
				}
			}()
		}
		wg.Wait()
		if want := uint64(workers*rounds) * (step + 5) & mask; counter[0] != want || counter[1] != 0 {
			t.Fatalf("%d bits: counter %#x, want %#x", w, counter[0], want)
		}
	}
}
