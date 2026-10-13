package loong64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func (e *ByteAssembler) memoryBase(a backend.Register, d int64) (backend.Register, int64) {
	if d >= -2048 && d <= 2047 {
		return a, d
	}
	s := backend.Register(19)
	if a == s {
		s = 20
	}
	e.Constant(s, uint64(d))
	e.Binary(op.OpAdd, 64, s, a, s)
	return s, 0
}
func (e *ByteAssembler) memory(load bool, w uint8, d, a backend.Register, disp uint64) {
	a, v := e.memoryBase(a, int64(disp))
	code, name := uint32(0x28c00000), "ld.d"
	if load {
		switch w {
		case 8:
			code, name = 0x2a000000, "ld.bu"
		case 16:
			code, name = 0x2a400000, "ld.hu"
		case 32:
			code, name = 0x2a800000, "ld.wu"
		}
	} else {
		switch w {
		case 8:
			code, name = 0x29000000, "st.b"
		case 16:
			code, name = 0x29400000, "st.h"
		case 32:
			code, name = 0x29800000, "st.w"
		default:
			code, name = 0x29c00000, "st.d"
		}
	}
	e.emit(code|(uint32(v)&4095)<<10|uint32(a)<<5|uint32(d), "%s r%d, %d(r%d)", name, d, v, a)
}
func (e *ByteAssembler) Load64(d, a backend.Register) {
	if !e.require((validGeneralRegisters(d, a))) {
		return
	}
	e.memory(true, 64, d, a, 0)
}
func (e *ByteAssembler) Store64(a, v backend.Register) {
	if !e.require((validGeneralRegisters(a, v))) {
		return
	}
	e.memory(false, 64, v, a, 0)
}
func (e *ByteAssembler) Load8(d, a backend.Register) {
	if !e.require((validGeneralRegisters(d, a))) {
		return
	}
	e.memory(true, 8, d, a, 0)
}
func (e *ByteAssembler) Store8(a, v backend.Register) {
	if !e.require((validGeneralRegisters(a, v))) {
		return
	}
	e.memory(false, 8, v, a, 0)
}
func (e *ByteAssembler) LoadOffset(d, a backend.Register, v uint64) {
	if !e.require((validGeneralRegisters(d, a))) {
		return
	}
	e.memory(true, 64, d, a, v)
}
func (e *ByteAssembler) StoreOffset(a, v backend.Register, o uint64) {
	if !e.require((validGeneralRegisters(a, v))) {
		return
	}
	e.memory(false, 64, v, a, o)
}
func (e *ByteAssembler) indexed(load bool, d, a, b backend.Register, s, disp uint64) {
	if disp == 0 {
		if s != 1 {
			if s != 0 && s&(s-1) == 0 {
				e.shift(op.OpShiftLeft, 19, b, uint8(log2(s)))
			} else {
				e.Constant(19, s)
				e.Binary(op.OpMul, 64, 19, b, 19)
			}
			b = 19
		}
		code, name := uint32(0x380c0000), "ldx.d"
		if !load {
			code, name = 0x381c0000, "stx.d"
		}
		e.rrr(code, name, d, a, b)
		return
	}
	e.Scaled(19, a, b, s, 0)
	e.memory(load, 64, d, 19, disp)
}
func log2(s uint64) int {
	n := 0
	for s > 1 {
		s >>= 1
		n++
	}
	return n
}
func (e *ByteAssembler) LoadAddress(d, a, b backend.Register, s, v uint64) {
	if !e.require((validGeneralRegisters(d, a, b))) {
		return
	}

	e.indexed(true, d, a, b, s, v)
}
func (e *ByteAssembler) StoreAddress(a, b, v backend.Register, s, disp uint64) {
	if !e.require((validGeneralRegisters(a, b, v))) {
		return
	}

	e.indexed(false, v, a, b, s, disp)
}

// OrderedMemory keeps byte-order conversion in the architecture expansion.
func (e *ByteAssembler) OrderedMemory(i machine.Instruction) error {
	if !e.require((validInstructionRegisters(i))) {
		return e.err
	}

	if i.ByteOrder != op.ByteOrderBigEndian {
		i.ByteOrder = op.ByteOrderNative
		return machine.Visit(i, e)
	}
	switch i.Op {
	case machine.OpLoad64:
		e.Load64(i.A, i.B)
		e.rr(0x3c00, "revb.d", i.A, i.A)
	case machine.OpLoadOffset:
		e.LoadOffset(i.A, i.B, i.Disp)
		e.rr(0x3c00, "revb.d", i.A, i.A)
	case machine.OpLoadAddress:
		e.LoadAddress(i.A, i.B, i.C, i.Imm, i.Disp)
		e.rr(0x3c00, "revb.d", i.A, i.A)
	case machine.OpStore64, machine.OpStoreOffset, machine.OpStoreAddress:
		// R12 retains the swapped value while R19/R20 form arbitrary addresses.
		value := i.B
		if i.Op == machine.OpStoreAddress {
			value = i.C
		}
		e.rr(0x3c00, "revb.d", 12, value)
		switch i.Op {
		case machine.OpStore64:
			e.Store64(i.A, 12)
		case machine.OpStoreOffset:
			e.StoreOffset(i.A, 12, i.Disp)
		case machine.OpStoreAddress:
			e.StoreAddress(i.A, i.B, 12, i.Imm, i.Disp)
		}
	default:
		return machine.ErrUnsupportedOperation
	}
	return nil
}
func (e *ByteAssembler) spillAddress(slot int) (backend.Register, uint64) {
	if e.stack == abi.StackPlatform {
		return 3, uint64((slot + 1 + e.goHomes) * 8)
	}
	return 23, uint64(slot * 8)
}
func (e *ByteAssembler) Spill(r backend.Register, s int) {
	if !e.require(validSlot(s, e.goHomes)) {
		return
	}
	if !e.require((validGeneralRegisters(r))) {
		return
	}

	a, v := e.spillAddress(s)
	e.StoreOffset(a, r, v)
}
func (e *ByteAssembler) Reload(r backend.Register, s int) {
	if !e.require(validSlot(s, e.goHomes)) {
		return
	}
	if !e.require((validGeneralRegisters(r))) {
		return
	}

	a, v := e.spillAddress(s)
	e.LoadOffset(r, a, v)
}
func (e *ByteAssembler) SaveLink(s int) {
	if !e.require(validSlot(s, e.goHomes)) {
		return
	}
	if e.stack != abi.StackPlatform {
		e.StoreOffset(23, 1, uint64(s*8))
	}
}
func (e *ByteAssembler) RestoreLink(s int) {
	if !e.require(validSlot(s, e.goHomes)) {
		return
	}
	if e.stack != abi.StackPlatform {
		e.LoadOffset(1, 23, uint64(s*8))
	}
}
func (e *ByteAssembler) IncomingWord(r backend.Register, o int) {
	if !e.require((validGeneralRegisters(r))) {
		return
	}
	e.LoadOffset(r, 3, uint64(o))
}
func (e *ByteAssembler) adjustSP(n int, sub bool) {
	for n > 0 {
		step := min(n, 2032)
		v := step
		if sub {
			v = -v
		}
		e.ri(0x02c00000, "addi.d", 3, 3, int64(v))
		n -= step
	}
}
func (e *ByteAssembler) FrameEnter(n int) {
	if !e.require((n >= 0 && uint64(n) <= 1<<20)) {
		return
	}
	e.adjustSP(n, true)
	e.Store64(3, 1)
}
func (e *ByteAssembler) FrameLeave(n int) {
	if !e.require((n >= 0 && uint64(n) <= 1<<20)) {
		return
	}
	e.Load64(1, 3)
	e.adjustSP(n, false)
}

// SaveCFloat and RestoreCFloat preserve LP64D fs0-fs7 across Go calls.
func (e *ByteAssembler) SaveCFloat(r backend.Register, slot int) {
	if !e.require((validGeneralRegisters(r)) && (validSlot(slot, e.goHomes))) {
		return
	}

	a, v := e.memoryBase(3, int64((slot+1+e.goHomes)*8))
	e.emit(0x2bc00000|(uint32(v)&4095)<<10|uint32(a)<<5|uint32(r), "fst.d f%d, %d(r%d)", r, v, a)
}
func (e *ByteAssembler) RestoreCFloat(r backend.Register, slot int) {
	if !e.require((validGeneralRegisters(r)) && (validSlot(slot, e.goHomes))) {
		return
	}

	a, v := e.memoryBase(3, int64((slot+1+e.goHomes)*8))
	e.emit(0x2b800000|(uint32(v)&4095)<<10|uint32(a)<<5|uint32(r), "fld.d f%d, %d(r%d)", r, v, a)
}
