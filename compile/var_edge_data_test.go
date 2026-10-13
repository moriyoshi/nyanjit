package compile_test

var zzGlobal [64]uintptr

func zzMemory() ([]uintptr, []uintptr) {
	mem := zzGlobal[:]
	values := make([]uintptr, len(mem))
	for i := range mem {
		values[i] = uintptr(i*7919 + 13)
		mem[i] = values[i]
	}
	return mem, values
}
