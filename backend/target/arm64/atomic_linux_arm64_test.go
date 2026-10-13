//go:build linux && arm64

package arm64

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
	"golang.org/x/sys/cpu"
)

// atomicCode builds f(address, operand, replacement) = old value around one
// OpAtomic in X9 (A), X10 (B), X11 (C) and X12 (D).
func atomicCode(t testing.TB, o op.Opcode, w uint8, lse bool) *execution.Code {
	t.Helper()
	b := NewByteAssembler(abi.StackNoUse, 0)
	b.lse = lse
	b.Move(10, 0)
	b.Move(11, 1)
	b.Move(12, 2)
	if err := machine.Visit(machine.Instruction{Op: machine.OpAtomic, Arithmetic: o, Bits: w, A: 9, B: 10, C: 11, D: 12}, b); err != nil {
		t.Fatal(err)
	}
	b.Move(0, 9)
	b.Ret()
	if b.Err() != nil {
		t.Fatal(b.Err())
	}
	code, err := execution.NewCode(b.Bytes(), execution.CodeOptions{ABI: abi.C, Arity: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func atomicPaths(t *testing.T) []bool {
	if cpu.ARM64.HasATOMICS {
		return []bool{false, true}
	}
	t.Log("host lacks FEAT_LSE; testing the exclusive forms only")
	return []bool{false}
}

// TestAtomicExecutes checks every operation and width, at every aligned
// offset of a guarded word, against a Go reference: the returned old value,
// the stored value and the untouched neighbouring bytes.
func TestAtomicExecutes(t *testing.T) {
	random := rand.New(rand.NewSource(1))
	for _, lse := range atomicPaths(t) {
		for o := op.OpAtomicLoad; o <= op.OpAtomicCompareSwap; o++ {
			for _, w := range atomicWidths {
				code := atomicCode(t, o, w, lse)
				mask := ^uint64(0) >> (64 - w)
				edges := []uint64{0, 1, mask, mask >> 1, mask>>1 + 1, 0x5a5a5a5a5a5a5a5a & mask}
				for n := 0; n < 200; n++ {
					var memory [3]uint64
					for i := range memory {
						memory[i] = random.Uint64()
					}
					offset := uintptr(random.Intn(8/(int(w)/8))) * uintptr(w/8)
					x, y := random.Uint64()&mask, random.Uint64()&mask
					if n < len(edges)*len(edges) {
						x, y = edges[n%len(edges)], edges[n/len(edges)]
					}
					address := unsafe.Add(unsafe.Pointer(&memory[1]), offset)
					old := readWidth(address, w)
					if o == op.OpAtomicCompareSwap && n%2 == 1 {
						x = old // even rounds fail unless equal by chance
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
						want, stored = uint64(got), x // A is not written; ignore it
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
						t.Fatalf("lse=%v %v/%d at +%d: old %#x, want %#x", lse, o, w, offset, uint64(got), want)
					}
					writeWidth(unsafe.Add(unsafe.Pointer(&before[1]), offset), w, stored)
					if memory != before {
						t.Fatalf("lse=%v %v/%d at +%d x=%#x y=%#x: memory %#x, want %#x", lse, o, w, offset, x, y, memory, before)
					}
				}
				code.Close()
			}
		}
	}
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

// TestAtomicCounterStress has goroutines add to one counter of each width,
// with neighbours in the same word updated concurrently by other goroutines.
func TestAtomicCounterStress(t *testing.T) {
	const goroutines, iterations = 8, 100000
	for _, lse := range atomicPaths(t) {
		for _, w := range atomicWidths {
			code := atomicCode(t, op.OpAtomicAdd, w, lse)
			var memory [2]uint64
			lanes := 8 / int(w/8)
			var group sync.WaitGroup
			for g := 0; g < goroutines; g++ {
				group.Add(1)
				go func(g int) {
					defer group.Done()
					address := unsafe.Add(unsafe.Pointer(&memory[0]), (g%lanes)*int(w/8))
					for i := 0; i < iterations; i++ {
						if _, err := code.Call(uintptr(address), 1, 0); err != nil {
							t.Error(err)
							return
						}
					}
				}(g)
			}
			group.Wait()
			code.Close()
			mask := ^uint64(0) >> (64 - w)
			for lane := 0; lane < lanes; lane++ {
				count := uint64(goroutines/lanes) * iterations
				if lanes > goroutines {
					count = 0
					if lane < goroutines {
						count = iterations
					}
				}
				if got := readWidth(unsafe.Add(unsafe.Pointer(&memory[0]), lane*int(w/8)), w); got != count&mask {
					t.Fatalf("lse=%v w=%d lane %d: %d, want %d", lse, w, lane, got, count&mask)
				}
			}
		}
	}
}

// TestAtomicSpinLock guards a plain counter with a compare-and-swap lock
// acquired by JIT code and released by a JIT atomic store.
func TestAtomicSpinLock(t *testing.T) {
	const goroutines, iterations = 8, 50000
	for _, lse := range atomicPaths(t) {
		for _, w := range []uint8{8, 32, 64} {
			cas := atomicCode(t, op.OpAtomicCompareSwap, w, lse)
			store := atomicCode(t, op.OpAtomicStore, w, lse)
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
			cas.Close()
			store.Close()
			if counter != goroutines*iterations {
				t.Fatalf("lse=%v w=%d: counter %d, want %d", lse, w, counter, goroutines*iterations)
			}
		}
	}
}
