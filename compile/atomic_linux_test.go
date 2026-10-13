//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"testing"
	"unsafe"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// The atomic tests run the same programs at both tiers: each operation and
// width once against memory, then concurrent counters, a compare-and-swap
// spin lock, and the store-buffering (SB) and message-passing (MP) litmus
// tests. On real hardware the litmus tests check the sequentially consistent
// mapping; under QEMU they only exercise the code.

var atomicTiers = []compile.Tier{compile.TierOptimized, compile.TierBaseline}

// atomicSink keeps the test buffers on the heap: generated code holds their
// addresses as plain words, which a moving goroutine stack would invalidate.
var atomicSink []any

func atomicBuffer(words int) []uint64 {
	buffer := make([]uint64, words)
	atomicSink = append(atomicSink, buffer)
	return buffer
}

func atomicCompile(t *testing.T, tier compile.Tier, build func(b *n.Builder)) *execution.Code {
	t.Helper()
	target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
	if err != nil {
		t.Fatal(err)
	}
	b := n.NewBuilderFor(target, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
	build(b)
	u, err := b.Lower()
	if err != nil {
		t.Fatalf("tier %d: %v", tier, err)
	}
	tmpl, err := u.Template()
	if err != nil {
		t.Fatal(err)
	}
	code, err := tmpl.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func atomicNative(p unsafe.Pointer) uintptr { return uintptr(p) }

var atomicOperations = []ir.Opcode{ir.OpAtomicLoad, ir.OpAtomicStore, ir.OpAtomicAdd, ir.OpAtomicAnd, ir.OpAtomicOr, ir.OpAtomicXor, ir.OpAtomicSwap, ir.OpAtomicCompareSwap}

// atomicReference applies operation to old with operand and replacement at
// width w and returns the new value and whether memory changes.
func atomicReference(operation ir.Opcode, w uint8, old, operand, replacement uint64) (uint64, bool) {
	mask := uint64(1)<<w - 1
	if w == 64 {
		mask = ^uint64(0)
	}
	switch operation {
	case ir.OpAtomicStore, ir.OpAtomicSwap:
		return operand & mask, true
	case ir.OpAtomicAdd:
		return (old + operand) & mask, true
	case ir.OpAtomicAnd:
		return old & operand, true
	case ir.OpAtomicOr:
		return old | operand, true
	case ir.OpAtomicXor:
		return old ^ operand, true
	case ir.OpAtomicCompareSwap:
		return replacement, old == operand
	}
	return old, false
}

func atomicEmit(b *n.Builder, operation ir.Opcode, address, operand, replacement n.Value, w uint8) n.Value {
	switch operation {
	case ir.OpAtomicLoad:
		return b.AtomicLoad(address, w)
	case ir.OpAtomicStore:
		b.AtomicStore(address, operand)
		return b.Const(0)
	case ir.OpAtomicAdd:
		return b.AtomicAdd(address, operand)
	case ir.OpAtomicAnd:
		return b.AtomicAnd(address, operand)
	case ir.OpAtomicOr:
		return b.AtomicOr(address, operand)
	case ir.OpAtomicXor:
		return b.AtomicXor(address, operand)
	case ir.OpAtomicSwap:
		return b.AtomicSwap(address, operand)
	}
	return b.AtomicCompareSwap(address, operand, replacement)
}

func atomicArgument(w uint8, x uint64) any {
	switch w {
	case 8:
		return uint8(x)
	case 16:
		return uint16(x)
	case 32:
		return uint32(x)
	}
	return x
}

// TestAtomicOperations checks every operation and width at every aligned
// offset of a word: the old value, zero-extended, and the bytes in memory,
// with compare-and-swap succeeding and failing.
func TestAtomicOperations(t *testing.T) {
	edges := []uint64{0, 1, 0x7f, 0x80, 0xff, 0x8000, 0xffff, 0x7fffffff, 0x80000000, 0xffffffff, 0x8000000000000000, ^uint64(0), 0x0123456789abcdef}
	for _, tier := range atomicTiers {
		for _, w := range []uint8{8, 16, 32, 64} {
			for _, operation := range atomicOperations {
				t.Run(fmt.Sprintf("tier%d/%v/%d", tier, operation, w), func(t *testing.T) {
					code := atomicCompile(t, tier, func(b *n.Builder) {
						address, operand, replacement := b.ParamBits(builder.NativeBits), b.ParamBits(w), b.ParamBits(w)
						b.Return(b.CastUnsigned(atomicEmit(b, operation, address, operand, replacement, w), 64))
					})
					defer code.Close()
					buffer := atomicBuffer(3)
					bytes := unsafe.Slice((*byte)(unsafe.Pointer(&buffer[0])), 24)
					size := int(w / 8)
					mask := uint64(1)<<w - 1
					if w == 64 {
						mask = ^uint64(0)
					}
					for offset := 8; offset < 16; offset += size {
						for k, initial := range edges {
							operand := edges[(k*7+3)%len(edges)] & mask
							if operation == ir.OpAtomicCompareSwap && k%2 == 0 {
								operand = initial & mask // succeeds
							}
							replacement := edges[(k*5+1)%len(edges)] & mask
							for i := range bytes {
								bytes[i] = byte(0xa5 ^ i)
							}
							want := append([]byte(nil), bytes...)
							for i := 0; i < size; i++ {
								bytes[offset+i] = byte(initial >> (8 * i))
								want[offset+i] = bytes[offset+i]
							}
							old := initial & mask
							next, stores := atomicReference(operation, w, old, operand, replacement)
							if stores {
								for i := 0; i < size; i++ {
									want[offset+i] = byte(next >> (8 * i))
								}
							}
							lo, hi, err := code.CallPair(execution.Words(atomicNative(unsafe.Pointer(&bytes[offset])), atomicArgument(w, operand), atomicArgument(w, replacement))...)
							got := execution.Wide(lo, hi)
							if operation == ir.OpAtomicStore {
								old = 0
							}
							if err != nil || got != old || string(bytes) != string(want) {
								t.Fatalf("offset %d initial %#x operand %#x replacement %#x: got %#x, %v; memory % x, want old %#x memory % x", offset, initial, operand, replacement, got, err, bytes, old, want)
							}
						}
					}
				})
			}
		}
	}
}

// TestAtomicFenceOrdersPlainMemory compiles Fence between plain accesses at
// both tiers and checks that the accesses keep their order.
func TestAtomicFenceOrdersPlainMemory(t *testing.T) {
	for _, tier := range atomicTiers {
		code := atomicCompile(t, tier, func(b *n.Builder) {
			address := b.ParamBits(builder.NativeBits)
			b.Store64(address, b.Const(7))
			b.Fence()
			b.Return(b.Load64(address))
		})
		buffer := atomicBuffer(1)
		got, err := code.Call(execution.Words(atomicNative(unsafe.Pointer(&buffer[0])))...)
		code.Close()
		if err != nil || got != 7 || buffer[0] != 7 {
			t.Fatalf("tier %d: got %d, %v; memory %d", tier, got, err, buffer[0])
		}
	}
}

// litmusEmulated reports a run under QEMU user mode, which the RULES recipes
// mark with QEMU_CPU or QEMU_LD_PREFIX in the guest environment. QEMU does not
// emulate every ordering guarantee of the guest (for one, arm64 LDAR after
// STLR may pass it), so there the litmus tests report forbidden outcomes
// instead of failing. NYANJIT_LITMUS_STRICT=1 makes them fail anyway.
func litmusEmulated() bool {
	return os.Getenv("NYANJIT_LITMUS_STRICT") != "1" && (os.Getenv("QEMU_CPU") != "" || os.Getenv("QEMU_LD_PREFIX") != "")
}

// litmusForbidden fails t, or only logs under emulation.
func litmusForbidden(t *testing.T, format string, args ...any) {
	t.Helper()
	if litmusEmulated() {
		t.Logf("emulated, not checked: "+format, args...)
		return
	}
	t.Fatalf(format, args...)
}

func atomicIterations(t *testing.T, native, emulated int) int {
	if s := os.Getenv("NYANJIT_LITMUS_ITERATIONS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			return v
		}
	}
	switch {
	case testing.Short():
		return emulated / 10
	case litmusEmulated():
		return emulated
	}
	return native
}

// atomicLoop builds a function of (count, args...) that runs body count
// times; body receives the iteration index at native width.
func atomicLoop(b *n.Builder, count n.Value, body func(i n.Value)) {
	i := b.Var(b.ConstBits(builder.NativeBits, 0))
	loop, done := b.NewLabel(), b.NewLabel()
	b.Bind(loop)
	b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, count), done)
	x := b.Get(i)
	body(x)
	b.Set(i, b.Add(x, b.ConstBits(builder.NativeBits, 1)))
	b.Jump(loop)
	b.Bind(done)
	b.Return(b.Const(0))
}

// atomicConcurrently runs f in goroutines at once, with the collector off so
// that no goroutine waits in generated code for one stopped by the GC.
func atomicConcurrently(t *testing.T, goroutines int, f func(g int)) {
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("needs GOMAXPROCS >= 2")
	}
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	// SetGCPercent can return before a finishing cycle flushes every P's
	// caches, which waits for a goroutine spinning in generated code.
	// ReadMemStats stops the world, so it returns once that cycle is over.
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			f(g)
		}()
	}
	wg.Wait()
}

// TestAtomicCounterStress adds from several goroutines to counters of each
// width; narrower counters share one word, so the masked sequences of the
// targets without byte atomics contend.
func TestAtomicCounterStress(t *testing.T) {
	iterations := atomicIterations(t, 200000, 20000)
	goroutines := min(8, max(2, runtime.GOMAXPROCS(0)))
	for _, tier := range atomicTiers {
		for _, w := range []uint8{8, 16, 32, 64} {
			code := atomicCompile(t, tier, func(b *n.Builder) {
				count, address := b.ParamBits(builder.NativeBits), b.ParamBits(builder.NativeBits)
				atomicLoop(b, count, func(n.Value) { b.AtomicAdd(address, b.ConstBits(w, 1)) })
			})
			buffer := atomicBuffer(1)
			lanes := 8 / int(w/8)
			base := uintptr(unsafe.Pointer(&buffer[0]))
			atomicConcurrently(t, goroutines, func(g int) {
				lane := base + uintptr(g%lanes)*uintptr(w/8)
				if _, err := code.Call(execution.Words(uintptr(iterations), lane)...); err != nil {
					t.Error(err)
				}
			})
			code.Close()
			bytes := unsafe.Slice((*byte)(unsafe.Pointer(&buffer[0])), 8)
			for lane := range lanes {
				var got uint64
				for i := 0; i < int(w/8); i++ {
					got |= uint64(bytes[lane*int(w/8)+i]) << (8 * i)
				}
				adders := 0
				for g := range goroutines {
					if g%lanes == lane {
						adders++
					}
				}
				want := uint64(adders * iterations)
				if w < 64 {
					want &= uint64(1)<<w - 1
				}
				if got != want {
					t.Fatalf("tier %d width %d lane %d: counter %d, want %d", tier, w, lane, got, want)
				}
			}
		}
	}
}

// TestAtomicSpinLock guards a plain counter with a compare-and-swap spin
// lock released by an atomic store.
func TestAtomicSpinLock(t *testing.T) {
	iterations := atomicIterations(t, 100000, 10000)
	goroutines := min(8, max(2, runtime.GOMAXPROCS(0)))
	for _, tier := range atomicTiers {
		for _, w := range []uint8{8, 32, 64} {
			code := atomicCompile(t, tier, func(b *n.Builder) {
				count, lock, counter := b.ParamBits(builder.NativeBits), b.ParamBits(builder.NativeBits), b.ParamBits(builder.NativeBits)
				atomicLoop(b, count, func(n.Value) {
					spin := b.NewLabel()
					b.Bind(spin)
					old := b.AtomicCompareSwap(lock, b.ConstBits(w, 0), b.ConstBits(w, 1))
					b.Branch(b.CompareImmediate(b.CastUnsigned(old, 64), ir.BranchNotEqual, 0), spin)
					b.Store64(counter, b.Add(b.Load64(counter), b.Const(1)))
					b.AtomicStore(lock, b.ConstBits(w, 0))
				})
			})
			buffer := atomicBuffer(2)
			lock, counter := uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&buffer[1]))
			atomicConcurrently(t, goroutines, func(int) {
				if _, err := code.Call(execution.Words(uintptr(iterations), lock, counter)...); err != nil {
					t.Error(err)
				}
			})
			code.Close()
			if want := uint64(goroutines * iterations); buffer[1] != want || buffer[0] != 0 {
				t.Fatalf("tier %d width %d: counter %d lock %d, want %d and 0", tier, w, buffer[1], buffer[0], want)
			}
		}
	}
}

// litmusThread builds one thread of a two-thread litmus test. Each iteration
// meets the other thread at a barrier, then runs body on the iteration's own
// words: mine and other are arrays of 64-bit words and results receives
// what the thread observed.
func litmusThread(t *testing.T, tier compile.Tier, body func(b *n.Builder, mine, other, result n.Value)) *execution.Code {
	return atomicCompile(t, tier, func(b *n.Builder) {
		count, barrier := b.ParamBits(builder.NativeBits), b.ParamBits(builder.NativeBits)
		mine, other, results := b.ParamBits(builder.NativeBits), b.ParamBits(builder.NativeBits), b.ParamBits(builder.NativeBits)
		atomicLoop(b, count, func(i n.Value) {
			b.AtomicAdd(barrier, b.ConstBits(builder.NativeBits, 1))
			target := b.ShiftLeft(b.Add(i, b.ConstBits(builder.NativeBits, 1)), 1)
			spin := b.NewLabel()
			b.Bind(spin)
			b.Branch(b.Compare(b.AtomicLoad(barrier, builder.NativeBits), ir.BranchBelow, target), spin)
			offset := b.ShiftLeft(i, 3)
			body(b, b.Add(mine, offset), b.Add(other, offset), b.Add(results, offset))
		})
	})
}

// runLitmus runs two litmus threads over iterations and returns what each
// observed.
func runLitmus(t *testing.T, iterations int, first, second *execution.Code) (r0, r1 []uint64) {
	x, y := atomicBuffer(iterations), atomicBuffer(iterations)
	r0, r1 = atomicBuffer(iterations), atomicBuffer(iterations)
	barrier := atomicBuffer(1)
	words := [2][]uintptr{
		execution.Words(uintptr(iterations), atomicNative(unsafe.Pointer(&barrier[0])), atomicNative(unsafe.Pointer(&x[0])), atomicNative(unsafe.Pointer(&y[0])), atomicNative(unsafe.Pointer(&r0[0]))),
		execution.Words(uintptr(iterations), atomicNative(unsafe.Pointer(&barrier[0])), atomicNative(unsafe.Pointer(&y[0])), atomicNative(unsafe.Pointer(&x[0])), atomicNative(unsafe.Pointer(&r1[0]))),
	}
	codes := [2]*execution.Code{first, second}
	atomicConcurrently(t, 2, func(g int) {
		if _, err := codes[g].Call(words[g]...); err != nil {
			t.Error(err)
		}
	})
	return r0, r1
}

// TestAtomicLitmusStoreBuffering is Dekker's test: each thread stores 1 to
// its word and then loads the other's. Under sequential consistency at least
// one thread sees the other's store. The plain-memory variant may show both
// loads reading 0 on hardware that buffers stores; it is reported, not
// checked, to show that the harness can observe reordering.
func TestAtomicLitmusStoreBuffering(t *testing.T) {
	iterations := atomicIterations(t, 1000000, 20000)
	for _, tier := range atomicTiers {
		for _, plain := range []bool{false, true} {
			thread := litmusThread(t, tier, func(b *n.Builder, mine, other, result n.Value) {
				if plain {
					b.Store64(mine, b.Const(1))
					b.Store64(result, b.Load64(other))
					return
				}
				b.AtomicStore(mine, b.Const(1))
				b.Store64(result, b.AtomicLoad(other, 64))
			})
			r0, r1 := runLitmus(t, iterations, thread, thread)
			thread.Close()
			weak := 0
			for i := range iterations {
				if r0[i] == 0 && r1[i] == 0 {
					weak++
				}
			}
			if plain {
				t.Logf("tier %d plain SB: %d of %d iterations saw both stores missing", tier, weak, iterations)
			} else if weak != 0 {
				litmusForbidden(t, "tier %d: SB forbidden outcome r0=r1=0 in %d of %d iterations", tier, weak, iterations)
			}
		}
	}
}

// TestAtomicLitmusMessagePassing writes data plainly and then a flag
// atomically; a reader that sees the flag must see the data.
func TestAtomicLitmusMessagePassing(t *testing.T) {
	iterations := atomicIterations(t, 1000000, 20000)
	for _, tier := range atomicTiers {
		// The writer's words are data (mine) and flags (other) at the
		// iteration's index; the reader receives them swapped.
		writer := litmusThread(t, tier, func(b *n.Builder, data, flag, result n.Value) {
			b.Store64(data, b.Const(1))
			b.AtomicStore(flag, b.Const(1))
		})
		reader := litmusThread(t, tier, func(b *n.Builder, flag, data, result n.Value) {
			seen := b.AtomicLoad(flag, 64)
			b.Store64(result, b.Or(b.ShiftLeft(seen, 1), b.Load64(data)))
		})
		_, r1 := runLitmus(t, iterations, writer, reader)
		writer.Close()
		reader.Close()
		bad, seen := 0, 0
		for i := range iterations {
			if r1[i]&2 != 0 {
				seen++
				if r1[i]&1 == 0 {
					bad++
				}
			}
		}
		t.Logf("tier %d MP: flag seen in %d of %d iterations", tier, seen, iterations)
		if bad != 0 {
			litmusForbidden(t, "tier %d: MP forbidden outcome (flag without data) in %d of %d iterations", tier, bad, iterations)
		}
	}
}

// TestAtomicRepeatsStayDistinct checks that identical atomics are neither
// merged nor dropped: two adds of one, and loads before and after them.
func TestAtomicRepeatsStayDistinct(t *testing.T) {
	for _, tier := range atomicTiers {
		code := atomicCompile(t, tier, func(b *n.Builder) {
			address := b.ParamBits(builder.NativeBits)
			before := b.AtomicLoad(address, 64)
			first := b.AtomicAdd(address, b.Const(1))
			second := b.AtomicAdd(address, b.Const(1))
			b.AtomicAdd(address, b.Const(1)) // unused
			after := b.AtomicLoad(address, 64)
			b.Return(b.Add(b.Add(b.ShiftLeft(before, 48), b.ShiftLeft(first, 32)), b.Add(b.ShiftLeft(second, 16), after)))
		})
		buffer := atomicBuffer(1)
		buffer[0] = 5
		lo, hi, err := code.CallPair(execution.Words(atomicNative(unsafe.Pointer(&buffer[0])))...)
		code.Close()
		if got, want := execution.Wide(lo, hi), uint64(5)<<48|5<<32|6<<16|8; err != nil || got != want || buffer[0] != 8 {
			t.Fatalf("tier %d: got %#x, %v, memory %d; want %#x and 8", tier, got, err, buffer[0], want)
		}
	}
}
