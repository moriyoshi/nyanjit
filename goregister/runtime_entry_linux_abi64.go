//go:build linux && (arm64 || amd64 || 386 || riscv64 || ppc64 || ppc64le)

package goregister

import (
	"encoding/binary"
	"runtime"
	"unsafe"
)

// RuntimeEntry finds a named function in the executable's runtime metadata.
// The returned address uses Go ABIInternal. Calling it requires its exact Go
// signature and runtime protocol; this does not make runtime APIs stable.
// Like Register, lookup is restricted to the supported runtime layouts.
func RuntimeEntry(name string) (uintptr, error) {
	if !Supported() || jitRuntimeModule == nil {
		return 0, ErrUnsupported
	}
	prefix := (*jitModulePrefix)(jitRuntimeModule)
	if prefix.pcHeader == nil || prefix.pcHeader.magic != 0xfffffff1 || int(prefix.pcHeader.minLC) != pcQuantum() || prefix.pcHeader.ptrSize != uint8(unsafe.Sizeof(uintptr(0))) {
		return 0, ErrUnsupported
	}
	for _, entry := range prefix.ftab {
		offset := uint64(entry.funcoff)
		if offset+8 > uint64(len(prefix.pclntable)) {
			continue
		}
		fn := prefix.pclntable[offset:]
		nameOffset := uint64(binary.NativeEndian.Uint32(fn[4:]))
		if nameOffset+uint64(len(name)) >= uint64(len(prefix.funcnametab)) {
			continue
		}
		names := prefix.funcnametab[nameOffset:]
		if string(names[:len(name)]) != name || names[len(name)] != 0 {
			continue
		}
		address := prefix.text + uintptr(entry.entryoff)
		// Resolve runtime-visible entries rather than sentinel/section offsets.
		if function := runtime.FuncForPC(address); function != nil && function.Entry() == address && function.Name() == name {
			return address, nil
		}
	}
	return 0, ErrUnsupported
}
