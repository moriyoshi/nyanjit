package backend

import "iter"

// Register is a target-specific physical register ordinal. Ordinals 64 to 95
// name the FP/vector register file; see VectorRegister.
type Register uint8

// VectorRegister identifies SIMD storage without aliasing a GPR ordinal.
func VectorRegister(number uint8) Register { return Register(64 + number) }

// IsVectorRegister reports the vector register file.
func IsVectorRegister(r Register) bool { return r >= 64 && r < 96 }

// VectorNumber returns the architecture's SIMD ordinal.
func VectorNumber(r Register) uint8 { return uint8(r) - 64 }

// RegisterSet is an immutable, ordered register list. A set built by a
// target also carries the target's register names, and sets derived from it
// with Derive keep them. Copies own their complete storage. At and All return
// values; AppendTo copies into caller-owned storage. The zero value is empty
// and unnamed. Returning or iterating a set requires no allocation. Sets
// compare equal when they hold the same registers in the same order under
// the same name table.
type RegisterSet struct {
	registers [64]Register
	count     uint8
	names     *[256]string
}

// NewRegisterSet copies up to 64 registers, preserving their order, into an
// unnamed set. It panics for larger sets, which exceed the supported physical
// register-bank size.
func NewRegisterSet(registers ...Register) RegisterSet {
	if len(registers) > 64 {
		panic("backend: register set exceeds 64 registers")
	}
	var s RegisterSet
	s.count = uint8(copy(s.registers[:], registers))
	return s
}

// Len returns the number of registers.
func (s RegisterSet) Len() int { return int(s.count) }

// At returns the register at index, panicking when index is outside the set.
func (s RegisterSet) At(index int) Register {
	if index < 0 || index >= s.Len() {
		panic("backend: register index out of bounds")
	}
	return s.registers[index]
}

// All visits registers in their allocation order.
func (s RegisterSet) All() iter.Seq[Register] {
	return func(yield func(Register) bool) {
		for _, r := range s.registers[:s.count] {
			if !yield(r) {
				return
			}
		}
	}
}

// AppendTo appends the registers to dst without exposing the set's storage.
func (s RegisterSet) AppendTo(dst []Register) []Register {
	return append(dst, s.registers[:s.count]...)
}

// Index returns the first position of register, or -1 if absent.
func (s RegisterSet) Index(register Register) int {
	for i, r := range s.registers[:s.count] {
		if r == register {
			return i
		}
	}
	return -1
}

// Contains reports whether register is present.
func (s RegisterSet) Contains(register Register) bool { return s.Index(register) >= 0 }

// NewNamedRegisterSet is NewRegisterSet for a set named by names, a
// target's table of register names by Register, with "" for a Register the
// target has no name for. The set refers to the table rather than copying
// it, so the table must not change afterwards; a nil table leaves the set
// unnamed.
func NewNamedRegisterSet(names *[256]string, registers ...Register) RegisterSet {
	s := NewRegisterSet(registers...)
	s.names = names
	return s
}

// Derive returns a set of registers, in order, named as s names its
// registers. The registers need not be in s.
func (s RegisterSet) Derive(registers ...Register) RegisterSet {
	d := NewRegisterSet(registers...)
	d.names = s.names
	return d
}

// Named reports whether the set carries register names.
func (s RegisterSet) Named() bool { return s.names != nil }

// Name returns the name of the register at index, or "" for an unnamed set.
// It panics when index is outside the set.
func (s RegisterSet) Name(index int) string { return s.NameOf(s.At(index)) }

// NameOf returns the name the set gives r, which need not be in the set, or
// "" for an unnamed set or a register its table does not name.
func (s RegisterSet) NameOf(r Register) string {
	if s.names == nil {
		return ""
	}
	return s.names[r]
}
