// Package execution owns executable mappings and the platform call boundary.
// It does not interpret IR or choose machine instructions.
package execution

import (
	"errors"
	"strconv"

	"github.com/moriyoshi/nyanjit/abi"
)

// ErrUnsupported indicates that this platform has no executable call boundary.
var ErrUnsupported = errors.New("nyanjit: native execution is unsupported on this platform")

// ErrInvalidCallback indicates that purego could not wrap the supplied value.
var ErrInvalidCallback = errors.New("execution: invalid callback")

// ErrEmptyCode indicates that Map was given no instructions.
var ErrEmptyCode = errors.New("execution: empty code")

// Map copies machine code into executable memory.
func Map(code []byte) ([]byte, error) {
	if len(code) == 0 {
		return nil, ErrEmptyCode
	}
	return mapCode(code)
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

// Scratch allocates writable memory for a generated function's workspace.
func Scratch(size int) ([]byte, error) { return scratch(size) }

// FreeScratch releases a workspace allocated by Scratch.
func FreeScratch(mem []byte) error { return freeScratch(mem) }

// MaxArgumentWords is the number of native ABI argument words a call can
// pass: eight parameters, each of which may need two words on a 32-bit host.
const MaxArgumentWords = 8 * 64 / strconv.IntSize

// ArgumentWords holds the native words passed to a generated entry.
type ArgumentWords = [MaxArgumentWords]uintptr

// Words converts logical arguments into native ABI words. Each format letter
// describes one value: 'l' is a native word (pointer, uintptr or a value of at
// most 32 bits on a 32-bit host) and 'L' is a 64-bit value. On 64-bit hosts
// every value occupies one word; on 32-bit hosts 'L' becomes the low and then
// the high word, matching the cdecl stack order. Words panics with ErrArity
// when format and values differ in length, on an unknown letter, or when an
// 'l' value does not fit in a native word.
func Words(format string, values ...uint64) []uintptr {
	if len(format) != len(values) {
		panic(ErrArity)
	}
	words := make([]uintptr, 0, len(values)*(64/strconv.IntSize))
	for i, v := range values {
		switch format[i] {
		case 'l':
			if uint64(uintptr(v)) != v {
				panic(ErrArity)
			}
			words = append(words, uintptr(v))
		case 'L':
			if strconv.IntSize == 32 {
				words = append(words, uintptr(uint32(v)), uintptr(uint32(v>>32)))
			} else {
				words = append(words, uintptr(v))
			}
		default:
			panic(ErrArity)
		}
	}
	return words
}

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
func Call(flavor abi.Flavor, address uintptr, args *ArgumentWords, workspace uintptr) uintptr {
	lo, _ := callWithABI(flavor, address, args, workspace)
	return lo
}

// CallWithArity supplies the number of argument words for stack-result ABIs
// such as 386 Go. Other platforms use the same call path as Call.
func CallWithArity(flavor abi.Flavor, address uintptr, args *ArgumentWords, workspace uintptr, words int) uintptr {
	lo, _ := CallPairWithArity(flavor, address, args, workspace, words)
	return lo
}

// CallPairWithArity is CallWithArity returning both result words, low first.
// On 64-bit hosts only scalar results are supported and hi is zero.
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
// low first (EAX:EDX on 386).
func CallForeignPair(address uintptr, args []uintptr) (lo, hi uintptr) {
	if !foreignSupported() {
		panic(ErrUnsupported)
	}
	return callForeign(address, args)
}

// RegisterForeignCallback creates a C ABI callback to a Go function.
// On big-endian PPC64 it accepts up to eight scalar integer/bool arguments
// and at most one integer/bool result. In a pure-Go binary, callbacks require
// an active runtime-originating C call via CallForeign or a compiled
// foreign/managed-C gateway. With real cgo linked, the runtime also attaches
// C-created threads. Callback mappings have process lifetime.
func RegisterForeignCallback(fn any) (uintptr, error) { return registerForeignCallback(fn) }

// CallbackOptions selects a C callback's function-pointer convention.
type CallbackOptions struct {
	// PPC64CABI selects ELFv1 descriptors or ELFv2 code entries on PPC64.
	// Unspecified follows the running Go binary's platform convention.
	PPC64CABI abi.PPC64CABI
}

// RegisterForeignCallbackWithOptions registers a callback with an explicit
// PPC64 C convention. On other targets the convention must be unspecified.
func RegisterForeignCallbackWithOptions(fn any, options CallbackOptions) (uintptr, error) {
	if options.PPC64CABI == abi.PPC64CABIUnspecified {
		return registerForeignCallback(fn)
	}
	return registerPPC64ForeignCallback(fn, options.PPC64CABI)
}
