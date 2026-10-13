// Package execution owns executable mappings and the platform call boundary.
// It does not interpret IR or choose machine instructions.
package execution

import (
	"errors"
	"runtime"
	"strconv"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// ErrUnsupported indicates that this platform has no executable call boundary.
var ErrUnsupported = errors.New("nyanjit: native execution is unsupported on this platform")

// ErrInvalidCallback indicates that purego could not wrap the supplied value.
var ErrInvalidCallback = errors.New("execution: invalid callback")

// ErrEmptyCode indicates that Map was given no instructions.
var ErrEmptyCode = errors.New("execution: empty code")

// Map copies machine code into executable memory. It returns ErrEmptyCode
// for no bytes, ErrUnsupported where native execution is unsupported, and
// otherwise the error from mapping memory.
func Map(code []byte) ([]byte, error) {
	if len(code) == 0 {
		return nil, ErrEmptyCode
	}
	return mapCode(code, nil)
}

// MapWithRelocations links machine bytes at their final mapping address before
// sealing the mapping executable. Unmap owns the returned mapping as with Map.
// It does not resolve library-provided symbols; callers supply every binding.
// Besides Map's errors it returns those of machine.LinkRelocationsAt.
func MapWithRelocations(code []byte, relocations []machine.Relocation, bindings map[string]uint64) ([]byte, error) {
	return mapWithRelocations(code, len(code), relocations, bindings)
}

func mapWithRelocations(code []byte, codeBytes int, relocations []machine.Relocation, bindings map[string]uint64) ([]byte, error) {
	if len(code) == 0 {
		return nil, ErrEmptyCode
	}
	if len(relocations) == 0 && len(bindings) == 0 {
		return Map(code)
	}
	return mapCode(code, func(mem []byte) error {
		linked, err := machine.LinkRelocationsAt(mem[:codeBytes], relocations, bindings, uint64(Entry(mem)))
		if err != nil {
			return err
		}
		copy(mem, linked)
		return nil
	})
}

// Unmap releases an executable mapping.
func Unmap(mem []byte) error { return unmapCode(mem) }

// Entry returns the address of the first byte of a mapping.
func Entry(mem []byte) uintptr {
	if len(mem) == 0 {
		return 0
	}
	return entry(mem)
}

// Scratch allocates writable memory for a generated function's workspace. It
// returns ErrUnsupported where native execution is unsupported, and otherwise
// the error from mapping memory.
func Scratch(size int) ([]byte, error) { return scratch(size) }

// FreeScratch releases a workspace allocated by Scratch.
func FreeScratch(mem []byte) error { return freeScratch(mem) }

// MaxArgumentWords is the number of native ABI argument words a call can
// pass: eight parameters, each of which may need two words on a 32-bit host.
const MaxArgumentWords = maxArguments * 64 / strconv.IntSize

// ArgumentWords holds the native words passed to a generated entry.
type ArgumentWords = [MaxArgumentWords]uintptr

// Wide joins a two-word result from CallPair into a 64-bit value. On 64-bit
// hosts a single word already holds the value, so hi is ignored.
func Wide(lo, hi uintptr) uint64 {
	if strconv.IntSize == 32 {
		return uint64(uint32(lo)) | uint64(uint32(hi))<<32
	}
	return uint64(lo)
}

// Call invokes the entry with the selected ABI and returns its first result
// word. Use CallWithArity for a 386 Go ABI entry, whose return slot follows
// its arguments.
// This is a trusted raw entry-address path: it bypasses stack, managed-entry,
// aggregate-type and lifetime checks. Prefer Code.Call(Words(values...)...) to retain argument roots.
func Call(flavor abi.Flavor, address uintptr, args *ArgumentWords, workspace uintptr) uintptr {
	lo, _ := callWithABI(flavor, address, args, workspace)
	return lo
}

// CallWithArity supplies the number of argument words for stack-result ABIs
// such as 386 Go. Other platforms use the same call path as Call.
// This trusted raw path bypasses Code stack, managed-entry and type checks.
func CallWithArity(flavor abi.Flavor, address uintptr, args *ArgumentWords, workspace uintptr, words int) uintptr {
	lo, _ := CallPairWithArity(flavor, address, args, workspace, words)
	return lo
}

// CallPairWithArity is CallWithArity returning both result words, low first.
// On 64-bit hosts only scalar results are supported and hi is zero.
// This trusted raw path bypasses Code stack, managed-entry and type checks.
func CallPairWithArity(flavor abi.Flavor, address uintptr, args *ArgumentWords, workspace uintptr, words int) (lo, hi uintptr) {
	if words < 0 || words > MaxArgumentWords {
		panic(ErrArity)
	}
	return callWithABIArity(flavor, address, args, workspace, words)
}

// CallForeign invokes a C ABI function pointer through a runtime-aware
// Go-to-C transition. Each argument is one native ABI word; see Words.
// It panics with ErrUnsupported when the target has no foreign-call gateway.
func CallForeign(address uintptr, args []uintptr) uintptr {
	lo, _ := CallForeignPair(address, args)
	return lo
}

// CallForeignPair is CallForeign returning both integer result registers,
// low first (EAX:EDX on 386). A zero address panics with ErrInvalidCodeOptions.
func CallForeignPair(address uintptr, args []uintptr) (lo, hi uintptr) {
	if address == 0 {
		panic(ErrInvalidCodeOptions)
	}
	if !foreignSupported() {
		panic(ErrUnsupported)
	}
	lo, hi = callForeign(address, args)
	// A Words result owns the Go memory its words address.
	runtime.KeepAlive(args)
	return lo, hi
}

// RegisterForeignCallback creates a C ABI callback to a Go function.
// In a pure-Go binary, callbacks require
// an active runtime-originating C call via CallForeign or a compiled
// foreign/managed-C gateway. With real cgo linked, the runtime also attaches
// C-created threads. Callback mappings have process lifetime. It returns
// ErrUnsupported without a foreign-call gateway and ErrInvalidCallback when fn
// cannot be wrapped.
func RegisterForeignCallback(fn any) (uintptr, error) { return registerForeignCallback(fn) }
