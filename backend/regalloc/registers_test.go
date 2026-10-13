package regalloc

import "github.com/moriyoshi/nyanjit/backend"

// testRegisters returns n allocatable registers: index i is general register
// i, or vector register i when vector has bit i set.
func testRegisters(n int, vector uint64) backend.RegisterSet {
	var list []backend.Register
	for i := range n {
		if vector&(1<<uint(i)) != 0 {
			list = append(list, backend.VectorRegister(uint8(i)))
		} else {
			list = append(list, backend.Register(i))
		}
	}
	return backend.NewRegisterSet(list...)
}

// gprs returns n general registers.
func gprs(n int) backend.RegisterSet { return testRegisters(n, 0) }

// testSet returns the registers of registers at the indices mask selects.
func testSet(registers backend.RegisterSet, mask uint64) backend.RegisterSet {
	var list []backend.Register
	for i := range registers.Len() {
		if mask&(1<<uint(i)) != 0 {
			list = append(list, registers.At(i))
		}
	}
	return backend.NewRegisterSet(list...)
}

// named returns the registers of set, in order, named names.
func named(set backend.RegisterSet, names []string) backend.RegisterSet {
	byRegister := new([256]string)
	list := set.AppendTo(nil)
	for i, r := range list {
		byRegister[r] = names[i]
	}
	return backend.NewNamedRegisterSet(byRegister, list...)
}

// nameIndex returns the index of the register set names name, or -1.
func nameIndex(set backend.RegisterSet, name string) int {
	for i := range set.Len() {
		if set.Name(i) == name {
			return i
		}
	}
	return -1
}

// registerNamed returns the register set names name; the name must exist.
func registerNamed(set backend.RegisterSet, name string) backend.Register {
	return set.At(nameIndex(set, name))
}
