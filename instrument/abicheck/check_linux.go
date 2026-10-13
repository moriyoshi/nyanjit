//go:build linux && (386 || amd64 || arm64 || ppc64le || riscv64 || loong64)

package abicheck

import (
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
)

// register is one entry of a target's register file. A seeded register
// occupies one slot: its sentinel goes in and its post-call value comes out.
// Registers that cannot hold a sentinel (SP, g and fixed pointers) are
// recorded instead and occupy two slots: the values before and after the call.
// Seeded entries precede recorded ones, in the assembly caller's order.
type register struct {
	name     string
	recorded bool
	// zero seeds the value the ABI fixes, zero, instead of a sentinel.
	zero bool
	// c and goABI say whether the C or Go ABI preserves the register.
	c, goABI bool
}

// registerFile is the memory the assembly caller reads and writes; each
// target's assembly file documents its slots.
type registerFile [registerSlots]uintptr

// callChecked seeds registers from regs, calls entry with args the way
// execution's trampolines do, stores the observed registers back into regs
// and returns the first result word. goABI selects the Go argument order
// where it differs from C, and on 386 words locates the Go result slot.
// args and regs escape so that they stay off the goroutine stack.
func callChecked(entry uintptr, args *execution.ArgumentWords, goABI, words uintptr, regs *registerFile) uintptr

// stackLimit is the stack execution grants a non-foreign entry below its
// NOSPLIT trampoline, 640 bytes on 64-bit hosts and 240 on 32-bit ones. The
// assembly callers' frames are no larger than execution's.
const stackLimit = 240 + (strconv.IntSize-32)/32*400

func checkPreserved(t *execution.Template, args []uintptr) (Result, error) {
	r := t.Report()
	flavor := r.ABI()
	if r.Architecture() != runtime.GOARCH || (flavor != abi.C && flavor != abi.Go) ||
		r.HasFunctionCalls() || r.HasSystemCalls() || r.StackBytes()+r.CallStackBytes() > stackLimit {
		return Result{}, ErrUnsupported
	}
	words, err := entryWords(r)
	if err != nil {
		return Result{}, err
	}
	if len(args) != words {
		return Result{}, fmt.Errorf("abicheck: %d argument words for an entry taking %d: %w", len(args), words, execution.ErrArity)
	}
	seed := seeds()
	if n := r.WorkspaceBytes(); n > 0 {
		slot := seededSlot(r.WorkspaceRegister())
		if slot < 0 {
			return Result{}, ErrUnsupported
		}
		workspace, err := execution.Scratch(n)
		if err != nil {
			return Result{}, err
		}
		defer execution.FreeScratch(workspace)
		seed[slot] = uintptr(unsafe.Pointer(&workspace[0]))
	}
	code, err := t.Bind(nil)
	// A Go-managed entry cannot bind without goregister.
	if errors.Is(err, execution.ErrExternalCallOnly) || errors.Is(err, execution.ErrUnsupported) {
		return Result{}, fmt.Errorf("%w: %w", ErrUnsupported, err)
	}
	if err != nil {
		return Result{}, err
	}
	defer code.Close()
	entry, err := code.EntryAddress()
	if err != nil {
		return Result{}, err
	}
	argv := new(execution.ArgumentWords)
	copy(argv[:], args)
	regs := new(registerFile)
	*regs = seed
	ret := callChecked(entry, argv, uintptr(flavor), uintptr(words), regs)
	return Result{Return: ret, Clobbered: clobbered(flavor, &seed, regs)}, nil
}

// entryWords checks that each argument word sits where callChecked passes
// it and returns their count.
func entryWords(r abi.Report) (int, error) {
	words := 0
	for _, value := range r.ArgumentLocations() {
		for _, word := range value.Words {
			if want, ok := argumentWord(r.ABI(), words); !ok || word != want {
				return 0, ErrUnsupported
			}
			words++
		}
	}
	if result := r.ResultLocation(); len(result.Words) != 0 && result.Words[0] != resultWord(r.ABI(), words) {
		return 0, ErrUnsupported
	}
	return words, nil
}

// registerWord returns the i-th register of order.
func registerWord(order []string, i int) (abi.WordLocation, bool) {
	if i >= len(order) {
		return abi.WordLocation{}, false
	}
	return abi.WordLocation{Storage: abi.Register, Register: order[i]}, true
}

// seeds returns a register file with a distinct sentinel in every seeded
// slot, zero in fixed-zero slots and zero in the recorded slots.
func seeds() registerFile {
	var file registerFile
	slot := 0
	for _, reg := range registers {
		if reg.recorded {
			break
		}
		if !reg.zero {
			s := uint64(slot+1) * 0x0101_0101_0101
			if strconv.IntSize == 32 {
				file[slot] = uintptr(0xa500_0000 | s&0xff_ffff)
			} else {
				file[slot] = uintptr(0xa5a5<<48 | s)
			}
		}
		slot++
	}
	return file
}

// seededSlot returns the slot of the seeded register name, or -1.
func seededSlot(name string) int {
	for slot, reg := range registers {
		if reg.recorded {
			break
		}
		if reg.name == name {
			return slot
		}
	}
	return -1
}

// clobbered lists the registers flavor preserves whose values changed.
func clobbered(flavor abi.Flavor, seed, got *registerFile) []string {
	var names []string
	slot := 0
	for _, reg := range registers {
		changed := got[slot] != seed[slot]
		slot++
		if reg.recorded {
			changed = got[slot-1] != got[slot]
			slot++
		}
		if changed && (flavor == abi.C && reg.c || flavor == abi.Go && reg.goABI) {
			names = append(names, reg.name)
		}
	}
	return names
}
