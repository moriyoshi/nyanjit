// Command barriers shows what nyanjit's barriers guarantee and where they go.
//
// machine.OpBarrier names a guarantee rather than an instruction, and each
// target's Policy.Barrier reports what its instruction for that guarantee
// rests on: the instruction set, host configuration, or nothing.
//
// The speculation section lowers a bounds-checked lookup with
// compile.Func.Harden. A mistrained bounds branch could otherwise let the
// processor load out of bounds speculatively before the comparison resolves;
// every hardened label starts with a speculation barrier, and the index is
// clamped to zero on the failing path. Results are unchanged: the barrier
// only constrains speculative execution.
//
// The memory section runs a store-buffering litmus test. Two threads each
// store 1 to their own word and then load the other's. Without a full
// barrier, both loads may return 0, because each store can still sit in its
// core's store buffer when the other core loads. machine.BarrierMemory
// forbids that outcome.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

var contracts = map[machine.BarrierContract]string{
	machine.BarrierUnavailable:   "unavailable",
	machine.BarrierArchitectural: "architectural",
	machine.BarrierPlatform:      "platform (needs host configuration)",
}

func main() {
	rounds := flag.Int("rounds", 200000, "store-buffering litmus rounds per variant")
	flag.Parse()
	if err := run(os.Stdout, *rounds); err != nil {
		log.Fatal(err)
	}
}

func run(w io.Writer, rounds int) error {
	fmt.Fprintln(w, "Speculation barrier (BarrierSpeculation)")
	if err := describe(w, machine.BarrierSpeculation); err != nil {
		return err
	}
	if err := speculation(w); err != nil {
		return err
	}
	fmt.Fprintln(w, "\nMemory barrier (BarrierMemory)")
	if err := describe(w, machine.BarrierMemory); err != nil {
		return err
	}
	return memory(w, rounds)
}

// describe prints every target's contract and instruction for kind, as its
// own text assembler renders the same OpBarrier.
func describe(w io.Writer, kind machine.Barrier) error {
	for _, arch := range architectures {
		t, err := newTarget(arch)
		if err != nil {
			return err
		}
		contract := t.policy.Barrier(kind)
		text := "-"
		if contract != machine.BarrierUnavailable {
			_, listing, err := t.assemble([]machine.Instruction{{Op: machine.OpBarrier, Barrier: kind}}, true)
			if err != nil {
				return fmt.Errorf("%s: %w", arch, err)
			}
			text = instructions(listing)
		}
		fmt.Fprintf(w, "  %-8s %-36s %s\n", arch, contracts[contract], text)
	}
	return nil
}

// instructions joins a listing's instructions, dropping their PCs.
func instructions(listing string) string {
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(listing), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && strings.Trim(fields[0], "0123456789abcdef:") == "" {
			fields = fields[1:]
		}
		out = append(out, strings.Join(fields, " "))
	}
	return strings.Join(out, "; ")
}

// lookup returns view[index], or 0xdead when index is out of bounds.
func lookup() (*ir.SealedProgram, error) {
	b := nyanjit.NewBuilder()
	view := nyanjit.SliceParam[uint64](b)
	index := b.ParamNative()
	outOfBounds := b.NewLabel()
	b.Return(view.Index(index, outOfBounds).Load())
	b.Bind(outOfBounds)
	b.Return(b.Const(0xdead))
	return b.Finish()
}

var hardened = compile.Func{ABI: abi.C, Context: compile.ContextForeign, Harden: true,
	Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}}

func speculation(w io.Writer) error {
	program, err := lookup()
	if err != nil {
		return err
	}
	target, err := compile.NewTarget(compile.TargetSpec{})
	if err != nil {
		return err
	}
	unit, err := compile.Lower(target, program, hardened)
	if errors.Is(err, compile.ErrUnsupported) {
		fmt.Fprintf(w, "\n%s has no speculation barrier, so Harden rejects control flow: %v\n", runtime.GOARCH, err)
		return nil
	}
	if err != nil {
		return err
	}
	listing, err := unit.Listing()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "\nHardened bounds-checked lookup on %s. Each label starts with the barrier,\nand the in-bounds edge clamps the index with a flag-dependent select:\n%s", runtime.GOARCH, listing)
	template, err := unit.Template()
	if err != nil {
		return err
	}
	code, err := template.Bind(nil)
	if err != nil {
		return err
	}
	defer code.Close()
	data := []uint64{10, 20, 30}
	for _, index := range []uint64{1, 3, ^uint64(0)} {
		got, err := code.Call(execution.Words(data, uintptr(index))...)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "lookup(%#x) = %#x\n", index, got)
	}
	return nil
}

// storeLoad builds f(store, load *uintptr) uintptr { *store = 1; [barrier;] return *load }.
func storeLoad(t *target, barrier bool) []machine.Instruction {
	var code []machine.Instruction
	var params [2]backend.Register
	for i := range params {
		register, ok := t.policy.ParamRegister(i)
		if !ok { // 386: ECX and EDX are free and caller-saved
			register = backend.Register(1 + i)
			code = append(code, machine.Instruction{Op: machine.OpLoadIncoming, A: register, Imm: t.incoming(i)})
		}
		params[i] = register
	}
	result := t.policy.ResultRegister()
	value := result
	if value == params[0] || value == params[1] {
		value = t.policy.ScratchRegister()
	}
	code = append(code,
		machine.Instruction{Op: machine.OpConst, A: value, Imm: 1},
		machine.Instruction{Op: machine.OpStore64, A: params[0], B: value})
	if barrier {
		code = append(code, machine.Instruction{Op: machine.OpBarrier, Barrier: machine.BarrierMemory})
	}
	return append(code,
		machine.Instruction{Op: machine.OpLoad64, A: result, B: params[1]},
		machine.Instruction{Op: machine.OpReturn})
}

func memory(w io.Writer, rounds int) error {
	t, err := newTarget(runtime.GOARCH)
	if err != nil {
		return err
	}
	for _, barrier := range []bool{false, true} {
		code := storeLoad(t, barrier)
		_, listing, err := t.assemble(code, true)
		if err != nil {
			return err
		}
		both, err := storeBuffering(t, code, rounds)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "\nstore 1; %sload on %s:\n%s", map[bool]string{false: "", true: "barrier; "}[barrier], runtime.GOARCH, listing)
		fmt.Fprintf(w, "both loads saw 0 in %d of %d rounds\n", both, rounds)
	}
	if runtime.GOMAXPROCS(0) < 2 {
		fmt.Fprintln(w, "(GOMAXPROCS is 1, so the threads rarely overlap)")
	}
	return nil
}

// storeBuffering runs rounds of the litmus test and counts rounds in which
// both threads loaded 0. Each round uses fresh zeroed words, and a spin
// barrier releases both threads together so their stores and loads overlap.
func storeBuffering(t *target, instructions []machine.Instruction, rounds int) (int, error) {
	bytes, _, err := t.assemble(instructions, false)
	if err != nil {
		return 0, err
	}
	code, err := execution.NewCode(bytes, execution.CodeOptions{ABI: abi.C, Foreign: true, Arity: 2}, nil)
	if err != nil {
		return 0, err
	}
	defer code.Close()
	words := [2][]uintptr{make([]uintptr, rounds), make([]uintptr, rounds)}
	var loaded [2][]uintptr
	var errs [2]error
	var arrived atomic.Int64
	var wg sync.WaitGroup
	for thread := range 2 {
		loaded[thread] = make([]uintptr, rounds)
		wg.Add(1)
		go func() {
			defer wg.Done()
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			mine, theirs := words[thread], words[1-thread]
			for i := range rounds {
				arrived.Add(1)
				for arrived.Load() < int64(2*(i+1)) {
				}
				v, err := code.Call(uintptr(unsafe.Pointer(&mine[i])), uintptr(unsafe.Pointer(&theirs[i])))
				if err != nil {
					errs[thread] = err
					// Keep arriving so the other thread is not left waiting.
					for i++; i < rounds; i++ {
						arrived.Add(1)
					}
					return
				}
				loaded[thread][i] = v
			}
		}()
	}
	wg.Wait()
	runtime.KeepAlive(words)
	if err := errors.Join(errs[:]...); err != nil {
		return 0, err
	}
	both := 0
	for i := range rounds {
		if loaded[0][i] == 0 && loaded[1][i] == 0 {
			both++
		}
	}
	return both, nil
}
