//go:build linux && ppc64

package execution

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/goregister"
	"runtime"
	"unsafe"
)

func foreignSupported() bool { return goregister.ForeignCallSupported() }

func callForeign(address uintptr, args []uintptr) (uintptr, uintptr) {
	convention, _ := abi.DefaultPPC64CABI("ppc64")
	return callForeignPPC64(address, args, convention), 0
}

type ppc64ForeignArgs struct {
	target     uintptr
	args       [8]uintptr
	convention uintptr
	result     uintptr
}

func foreignNativePointer() unsafe.Pointer

func callForeignPPC64(address uintptr, args []uintptr, convention abi.PPC64CABI) uintptr {
	if len(args) > 8 {
		panic(ErrArity)
	}
	if _, err := abi.NewPPC64Profile("ppc64", abi.C, convention); err != nil {
		panic(ErrInvalidCodeOptions)
	}
	words := new(ppc64ForeignArgs)
	words.target, words.convention = address, uintptr(convention)
	copy(words.args[:], args)
	trampoline := foreignNativePointer()
	// runtime.asmcgocall itself follows the executable's ELF ABI. The
	// trampoline descriptor is distinct from the callee's selected ABI.
	hostABI, _ := abi.DefaultPPC64CABI("ppc64")
	var descriptor *[3]uintptr
	if hostABI == abi.PPC64ELFv1 {
		descriptor = &[3]uintptr{uintptr(trampoline), 0, 0}
		trampoline = unsafe.Pointer(descriptor)
	}
	goregister.ForeignCall(trampoline, unsafe.Pointer(words))
	runtime.KeepAlive(descriptor)
	return words.result
}
