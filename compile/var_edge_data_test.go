package compile_test

import (
	"math/bits"
	"runtime"
)

var zzGlobal [64]uintptr

func zzMemory() ([]uintptr, []uintptr) {
	mem := zzGlobal[:]
	values := make([]uintptr, len(mem))
	for i := range mem {
		values[i] = uintptr(i*7919 + 13)
		mem[i] = values[i]
		if runtime.GOARCH == "ppc64" {
			mem[i] = uintptr(bits.ReverseBytes64(uint64(mem[i])))
		}
	}
	return mem, values
}
