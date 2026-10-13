//go:build linux && loong64

package loong64

import (
	"encoding/binary"
	"runtime"
	"sync"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// atomicCode builds f(address, c, d) = the OpAtomic result.
func atomicCode(t *testing.T, operation op.Opcode, w uint8) *execution.Code {
	t.Helper()
	ins := atomicForm(operation, w)
	a := NewByteAssembler(abi.StackNoUse, 0)
	a.Move(ins.B, 4)
	a.Move(ins.C, 5)
	a.Move(ins.D, 6)
	if err := machine.Visit(ins, a); err != nil {
		t.Fatal(err)
	}
	a.Move(4, ins.A)
	a.Ret()
	if a.Err() != nil {
		t.Fatal(a.Err())
	}
	code, err := execution.NewCode(a.Bytes(), execution.CodeOptions{ABI: abi.C, Arity: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// atomicSink makes test memory escape to the heap: generated code holds only
// its address, which a stack copy would leave behind.
var atomicSink [][]uint64

func atomicMemory(words int) []uint64 {
	memory := make([]uint64, words)
	atomicSink = append(atomicSink, memory)
	return memory
}

// TestAtomicExecutes runs every operation and width at every aligned offset
// of a 64-bit word against a Go reference, checking the old value and every
// byte of the surrounding memory.
func TestAtomicExecutes(t *testing.T) {
	for _, operation := range atomicOperations {
		for _, w := range []uint8{8, 16, 32, 64} {
			code := atomicCode(t, operation, w)
			m := ^uint64(0) >> (64 - w)
			values := []uint64{0, 1, 2, m, m - 1, m >> 1, m>>1 + 1, 0x5a5a5a5a5a5a5a5a & m, 0xa5a5a5a5a5a5a5a5 & m}
			memory := atomicMemory(2)
			bytes := unsafe.Slice((*byte)(unsafe.Pointer(&memory[0])), 16)
			for offset := 0; offset < 8; offset += int(w / 8) {
				for _, old := range values {
					for _, c := range values {
						for _, d := range []uint64{c ^ m, 7 & m} {
							if operation == op.OpAtomicCompareSwap && d == 7&m {
								c = old // the success case
							}
							for i := range bytes {
								bytes[i] = byte(0x11 * (i + 1))
							}
							field := bytes[8+offset : 8+offset+int(w/8)]
							put := func(v uint64) {
								var b [8]byte
								binary.LittleEndian.PutUint64(b[:], v)
								copy(field, b[:])
							}
							get := func() uint64 {
								var b [8]byte
								copy(b[:], field)
								return binary.LittleEndian.Uint64(b[:])
							}
							put(old)
							before := append([]byte(nil), bytes...)
							got, err := code.Call(uintptr(unsafe.Pointer(&field[0])), uintptr(c), uintptr(d))
							runtime.KeepAlive(memory)
							if err != nil {
								t.Fatal(err)
							}
							want := referenceAtomic(operation, w, old, c, d)
							if operation != op.OpAtomicStore && uint64(got) != old {
								t.Fatalf("%v/%d offset %d old %#x c %#x d %#x: returned %#x", operation, w, offset, old, c, d, got)
							}
							if now := get(); now != want {
								t.Fatalf("%v/%d offset %d old %#x c %#x d %#x: memory %#x, want %#x", operation, w, offset, old, c, d, now, want)
							}
							for i := range bytes {
								if (i < 8+offset || i >= 8+offset+int(w/8)) && bytes[i] != before[i] {
									t.Fatalf("%v/%d offset %d: byte %d changed", operation, w, offset, i)
								}
							}
						}
					}
				}
			}
			code.Close()
		}
	}
}

// TestAtomicCountersUnderContention adds to neighbouring counters of one
// word from several goroutines, so the masked loops retry on each other.
func TestAtomicCountersUnderContention(t *testing.T) {
	const goroutines, iterations = 4, 20000
	for _, w := range []uint8{8, 16, 32, 64} {
		code := atomicCode(t, op.OpAtomicAdd, w)
		load := atomicCode(t, op.OpAtomicLoad, w)
		memory := atomicMemory(1)
		base := uintptr(unsafe.Pointer(&memory[0]))
		counters := 8 / int(w/8)
		var group sync.WaitGroup
		for g := range goroutines {
			group.Add(1)
			go func() {
				defer group.Done()
				for i := range iterations {
					slot := (i + g) % counters
					if _, err := code.Call(base+uintptr(slot*int(w/8)), 1, 0); err != nil {
						t.Error(err)
						return
					}
				}
			}()
		}
		group.Wait()
		m := ^uint64(0) >> (64 - w)
		for slot := range counters {
			got, err := load.Call(base+uintptr(slot*int(w/8)), 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			if want := uint64(goroutines*iterations/counters) & m; uint64(got) != want {
				t.Fatalf("%d-bit counter %d: %d, want %d", w, slot, got, want)
			}
		}
		runtime.KeepAlive(memory)
		code.Close()
		load.Close()
	}
}

// TestAtomicSpinLock guards a plain counter with a CAS lock released by an
// atomic store, at each width, with the lock next to busy bytes.
func TestAtomicSpinLock(t *testing.T) {
	const goroutines, iterations = 4, 5000
	for _, w := range []uint8{8, 16, 32, 64} {
		cas := atomicCode(t, op.OpAtomicCompareSwap, w)
		store := atomicCode(t, op.OpAtomicStore, w)
		add := atomicCode(t, op.OpAtomicAdd, 8)
		memory := atomicMemory(1)
		lock := uintptr(unsafe.Pointer(&memory[0]))
		neighbour := lock + 7 // a byte the lock never covers below 64 bits
		counter := 0
		var group sync.WaitGroup
		for range goroutines {
			group.Add(1)
			go func() {
				defer group.Done()
				for range iterations {
					for {
						old, err := cas.Call(lock, 0, 1)
						if err != nil {
							t.Error(err)
							return
						}
						if old == 0 {
							break
						}
					}
					counter++
					if w < 64 {
						add.Call(neighbour, 1, 0)
					}
					store.Call(lock, 0, 0)
				}
			}()
		}
		group.Wait()
		if counter != goroutines*iterations {
			t.Fatalf("%d-bit lock: counter %d, want %d", w, counter, goroutines*iterations)
		}
		if w < 64 && uint64(byte(memory[0]>>56)) != uint64(goroutines*iterations)&0xff {
			t.Fatalf("%d-bit lock: neighbour %d", w, byte(memory[0]>>56))
		}
		runtime.KeepAlive(memory)
		cas.Close()
		store.Close()
		add.Close()
	}
}
