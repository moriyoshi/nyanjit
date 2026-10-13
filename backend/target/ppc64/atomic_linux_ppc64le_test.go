//go:build linux && ppc64le

package ppc64

import (
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// atomicCode builds f(address, operand, replacement) = old value around one
// OpAtomic in r7 (A), r8 (B), r9 (C) and r10 (D). Under QEMU these runs are
// coverage, not a proof of the memory model.
func atomicCode(t testing.TB, o op.Opcode, w uint8) *execution.Code {
	t.Helper()
	e := atomicEmitter(t, abi.PPC64Power8)
	b := NewByteAssembler()
	pc := 0
	for _, ins := range []machine.Instruction{
		{Op: machine.OpMove, A: 8, B: 3}, {Op: machine.OpMove, A: 9, B: 4}, {Op: machine.OpMove, A: 10, B: 5},
		{Op: machine.OpAtomic, Arithmetic: o, Bits: w, A: 7, B: 8, C: 9, D: 10},
		{Op: machine.OpMove, A: 3, B: 7}, {Op: machine.OpReturn},
	} {
		sequence, err := e.Expand(ins, pc)
		if err != nil {
			t.Fatal(err)
		}
		for _, native := range sequence {
			if err := b.Visit(native); err != nil {
				t.Fatal(err)
			}
		}
		pc += 4 * len(sequence)
	}
	code, err := execution.NewCode(b.Bytes(), execution.CodeOptions{ABI: abi.C, Arity: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func readWidth(p unsafe.Pointer, w uint8) uint64 {
	switch w {
	case 8:
		return uint64(*(*uint8)(p))
	case 16:
		return uint64(*(*uint16)(p))
	case 32:
		return uint64(*(*uint32)(p))
	}
	return *(*uint64)(p)
}

func writeWidth(p unsafe.Pointer, w uint8, v uint64) {
	switch w {
	case 8:
		*(*uint8)(p) = uint8(v)
	case 16:
		*(*uint16)(p) = uint16(v)
	case 32:
		*(*uint32)(p) = uint32(v)
	default:
		*(*uint64)(p) = v
	}
}

// TestAtomicExecutes checks every operation and width at every aligned
// offset against a Go reference: the old value, the stored value and the
// untouched neighbouring bytes, for compare-and-swap success and failure.
func TestAtomicExecutes(t *testing.T) {
	random := rand.New(rand.NewSource(1))
	for o := op.OpAtomicLoad; o <= op.OpAtomicCompareSwap; o++ {
		for _, w := range []uint8{8, 16, 32, 64} {
			code := atomicCode(t, o, w)
			mask := ^uint64(0) >> (64 - w)
			for n := 0; n < 100; n++ {
				var memory [3]uint64
				for i := range memory {
					memory[i] = random.Uint64()
				}
				offset := uintptr(random.Intn(8/(int(w)/8))) * uintptr(w/8)
				x, y := random.Uint64()&mask, random.Uint64()&mask
				address := unsafe.Add(unsafe.Pointer(&memory[1]), offset)
				old := readWidth(address, w)
				if o == op.OpAtomicCompareSwap && n%2 == 1 {
					x = old
				}
				before := memory
				got, err := code.Call(uintptr(address), uintptr(x), uintptr(y))
				runtime.KeepAlive(&memory)
				if err != nil {
					t.Fatal(err)
				}
				want, stored := old, old
				switch o {
				case op.OpAtomicStore:
					want, stored = uint64(got), x
				case op.OpAtomicAdd:
					stored = (old + x) & mask
				case op.OpAtomicAnd:
					stored = old & x
				case op.OpAtomicOr:
					stored = old | x
				case op.OpAtomicXor:
					stored = old ^ x
				case op.OpAtomicSwap:
					stored = x
				case op.OpAtomicCompareSwap:
					if old == x {
						stored = y
					}
				}
				if uint64(got) != want {
					t.Fatalf("%v/%d at +%d: old %#x, want %#x", o, w, offset, uint64(got), want)
				}
				writeWidth(unsafe.Add(unsafe.Pointer(&before[1]), offset), w, stored)
				if memory != before {
					t.Fatalf("%v/%d at +%d: memory %#x, want %#x", o, w, offset, memory, before)
				}
			}
			code.Close()
		}
	}
}

// TestAtomicCounterStress adds to one counter from several goroutines.
func TestAtomicCounterStress(t *testing.T) {
	const goroutines, iterations = 4, 20000
	for _, w := range []uint8{8, 16, 32, 64} {
		code := atomicCode(t, op.OpAtomicAdd, w)
		var memory [1]uint64
		var group sync.WaitGroup
		for g := 0; g < goroutines; g++ {
			group.Add(1)
			go func() {
				defer group.Done()
				for i := 0; i < iterations; i++ {
					if _, err := code.Call(uintptr(unsafe.Pointer(&memory[0])), 1, 0); err != nil {
						t.Error(err)
						return
					}
				}
			}()
		}
		group.Wait()
		code.Close()
		mask := ^uint64(0) >> (64 - w)
		if got := readWidth(unsafe.Pointer(&memory[0]), w); got != goroutines*iterations&mask {
			t.Fatalf("w=%d: %d, want %d", w, got, goroutines*iterations&mask)
		}
	}
}

// TestAtomicSpinLock guards a plain counter with a JIT compare-and-swap lock.
func TestAtomicSpinLock(t *testing.T) {
	const goroutines, iterations = 4, 10000
	cas := atomicCode(t, op.OpAtomicCompareSwap, 32)
	store := atomicCode(t, op.OpAtomicStore, 32)
	defer cas.Close()
	defer store.Close()
	var lock [1]uint64
	counter := 0
	var group sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		group.Add(1)
		go func() {
			defer group.Done()
			address := uintptr(unsafe.Pointer(&lock[0]))
			for i := 0; i < iterations; i++ {
				for {
					old, err := cas.Call(address, 0, 1)
					if err != nil {
						t.Error(err)
						return
					}
					if old == 0 {
						break
					}
					runtime.Gosched()
				}
				counter++
				if _, err := store.Call(address, 0, 0); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	group.Wait()
	if counter != goroutines*iterations {
		t.Fatalf("counter %d, want %d", counter, goroutines*iterations)
	}
}
