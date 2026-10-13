package amd64

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// CanAtomic reports whether the target encodes atomic operation at width.
func (Policy) CanAtomic(operation op.Opcode, width uint8) bool {
	return operation >= op.OpAtomicLoad && operation <= op.OpAtomicCompareSwap && validWidth(width)
}

// Every x86 locked instruction, XCHG with memory included, is a full
// barrier, and plain loads are ordered after earlier locked operations, so
// all of these forms are sequentially consistent. R11 and RAX are the fixed
// temporaries (atomicTemporaries). Each form writes A only in its last
// instruction, after reading every input.
const atomicTemporary = 11

// atomicTemporaries lists the fixed registers an OpAtomic expansion writes
// besides A. The compiler keeps every operand and A out of them.
func atomicTemporaries(operation op.Opcode) []uint8 {
	switch operation {
	case op.OpAtomicStore, op.OpAtomicAdd, op.OpAtomicSwap:
		return []uint8{atomicTemporary}
	case op.OpAtomicAnd, op.OpAtomicOr, op.OpAtomicXor:
		return []uint8{0, atomicTemporary}
	case op.OpAtomicCompareSwap:
		return []uint8{0}
	}
	return nil
}

// atomicOperands returns the registers ins reads after A: B, then C unless it
// is a load, then D for compare-and-swap.
func atomicOperands(ins machine.Instruction) []uint8 {
	switch ins.Arithmetic {
	case op.OpAtomicLoad:
		return []uint8{uint8(ins.B)}
	case op.OpAtomicCompareSwap:
		return []uint8{uint8(ins.B), uint8(ins.C), uint8(ins.D)}
	}
	return []uint8{uint8(ins.B), uint8(ins.C)}
}

// validAtomic checks an OpAtomic: a known operation and width, a defined
// memory order, GP operands and nothing in a fixed temporary. Every form
// writes A only in its last instruction, so A may equal an input.
func validAtomic(ins machine.Instruction) bool {
	if ins.Op != machine.OpAtomic || ins.Arithmetic < op.OpAtomicLoad || ins.Arithmetic > op.OpAtomicCompareSwap ||
		!validWidth(ins.Bits) || ins.Imm > 0xff || !op.MemoryOrder(ins.Imm).Valid() || !ins.ByteOrder.Valid() || ins.ByteOrder != op.ByteOrderNative {
		return false
	}
	operands := atomicOperands(ins)
	used := operands
	if ins.Arithmetic != op.OpAtomicStore {
		used = append([]uint8{uint8(ins.A)}, operands...)
	}
	for _, r := range used {
		if r >= 16 {
			return false
		}
		for _, t := range atomicTemporaries(ins.Arithmetic) {
			if r == t {
				return false
			}
		}
	}
	return true
}

// atomicMemory encodes `prefixes [REX] opcode` with a ModRM naming [base] and
// register reg. byteReg forces REX for SPL..DIL in the reg field.
func atomicMemory(prefixes []byte, wide bool, reg, base uint8, byteReg bool, opcode ...byte) []byte {
	code := append([]byte(nil), prefixes...)
	rex := byte(0x40) | reg>>3<<2 | base>>3
	if wide {
		rex |= 8
	}
	if rex != 0x40 || byteReg && reg >= 4 && reg < 8 {
		code = append(code, rex)
	}
	code = append(code, opcode...)
	mod := byte(0)
	if base&7 == 5 {
		mod = 0x40
	}
	code = append(code, mod|(reg&7)<<3|base&7)
	if base&7 == 4 {
		code = append(code, 0x24)
	}
	if base&7 == 5 {
		code = append(code, 0)
	}
	return code
}

// atomicRegisterName names register r at width w.
func atomicRegisterName(r uint8, w uint8) string {
	switch w {
	case 8:
		return amd64RegisterNames8[r]
	case 16:
		return amd64RegisterNames16[r]
	case 32:
		return amd64RegisterNames32[r]
	}
	return amd64RegisterNames[r]
}

var atomicPointers = map[uint8]string{8: "byte", 16: "word", 32: "dword", 64: "qword"}

// atomicPlan expands a valid OpAtomic (see FORMS.md).
func atomicPlan(ins machine.Instruction) []expansionStep {
	var x expansion
	w, a, b, c, d := ins.Bits, uint8(ins.A), uint8(ins.B), uint8(ins.C), uint8(ins.D)
	wide := w == 64
	var size []byte // operand-size prefix of 16-bit forms
	if w == 16 {
		size = []byte{0x66}
	}
	locked := append([]byte{0xf0}, size...)
	// rmOpcode is the r/m, reg opcode of XCHG, XADD and CMPXCHG, whose 8-bit
	// form is one less.
	rmOpcode := func(opcode ...byte) []byte {
		opcode = append([]byte(nil), opcode...)
		if w == 8 {
			opcode[len(opcode)-1]--
		}
		return opcode
	}
	mem := "[" + amd64Name(ins.B) + "]"
	ptr := atomicPointers[w]
	// load writes the zero-extended value at [B] to r.
	load := func(r uint8) {
		switch w {
		case 8:
			x.emit(atomicMemory(nil, false, r, b, false, 0x0f, 0xb6), "movzx %s, byte ptr %s", amd64RegisterNames32[r], mem)
		case 16:
			x.emit(atomicMemory(nil, false, r, b, false, 0x0f, 0xb7), "movzx %s, word ptr %s", amd64RegisterNames32[r], mem)
		default:
			x.emit(atomicMemory(nil, wide, r, b, false, 0x8b), "mov %s, %s ptr %s", atomicRegisterName(r, w), ptr, mem)
		}
	}
	// extend zero-extends the low w bits of from into a.
	extend := func(from uint8) {
		switch w {
		case 8:
			x.emit(gpRR(false, a, from, true, 0x0f, 0xb6), "movzx %s, %s", amd64RegisterNames32[a], amd64RegisterNames8[from])
		case 16:
			x.emit(gpRR(false, a, from, false, 0x0f, 0xb7), "movzx %s, %s", amd64RegisterNames32[a], amd64RegisterNames16[from])
		default:
			if from != a {
				x.emit(gpRR(wide, from, a, false, 0x89), "mov %s, %s", atomicRegisterName(a, w), atomicRegisterName(from, w))
			}
		}
	}
	move := func(dst, src uint8) {
		x.emit(gpRR(true, src, dst, false, 0x89), "mov %s, %s", amd64RegisterNames[dst], amd64RegisterNames[src])
	}
	switch ins.Arithmetic {
	case op.OpAtomicLoad:
		load(a)
	case op.OpAtomicStore:
		move(atomicTemporary, c)
		x.emit(atomicMemory(size, wide, atomicTemporary, b, true, rmOpcode(0x87)...), "xchg %s ptr %s, %s", ptr, mem, atomicRegisterName(atomicTemporary, w))
	case op.OpAtomicAdd, op.OpAtomicSwap:
		move(atomicTemporary, c)
		if ins.Arithmetic == op.OpAtomicAdd {
			x.emit(atomicMemory(locked, wide, atomicTemporary, b, true, rmOpcode(0x0f, 0xc1)...), "lock xadd %s ptr %s, %s", ptr, mem, atomicRegisterName(atomicTemporary, w))
		} else {
			x.emit(atomicMemory(size, wide, atomicTemporary, b, true, rmOpcode(0x87)...), "xchg %s ptr %s, %s", ptr, mem, atomicRegisterName(atomicTemporary, w))
		}
		extend(atomicTemporary)
	case op.OpAtomicAnd, op.OpAtomicOr, op.OpAtomicXor:
		opcode := map[op.Opcode]byte{op.OpAtomicAnd: 0x21, op.OpAtomicOr: 0x09, op.OpAtomicXor: 0x31}[ins.Arithmetic]
		mnemonic := map[op.Opcode]string{op.OpAtomicAnd: "and", op.OpAtomicOr: "or", op.OpAtomicXor: "xor"}[ins.Arithmetic]
		load(0)
		retry := x.label()
		x.bind(retry)
		move(atomicTemporary, 0)
		x.emit(gpRR(wide, c, atomicTemporary, false, opcode), "%s %s, %s", mnemonic, atomicRegisterName(atomicTemporary, max(w, 32)), atomicRegisterName(c, max(w, 32)))
		x.emit(atomicMemory(locked, wide, atomicTemporary, b, true, rmOpcode(0x0f, 0xb1)...), "lock cmpxchg %s ptr %s, %s", ptr, mem, atomicRegisterName(atomicTemporary, w))
		x.branch(0x75, "jnz", retry)
		extend(0)
	case op.OpAtomicCompareSwap:
		move(0, c)
		x.emit(atomicMemory(locked, wide, d, b, true, rmOpcode(0x0f, 0xb1)...), "lock cmpxchg %s ptr %s, %s", ptr, mem, atomicRegisterName(d, w))
		extend(0)
	}
	return x.resolve()
}

// atomicWritesFlags reports whether an OpAtomic expansion writes the
// arithmetic flags: XADD, CMPXCHG and the loops' logic do; MOV and XCHG do not.
func atomicWritesFlags(ins machine.Instruction) bool {
	switch ins.Arithmetic {
	case op.OpAtomicLoad, op.OpAtomicStore, op.OpAtomicSwap:
		return false
	}
	return true
}

// Atomic implements OpAtomic.
func (e *ByteAssembler) Atomic(ins machine.Instruction) error {
	if !e.require(validAtomic(ins)) {
		return e.err
	}
	e.expansion(atomicPlan(ins))
	return nil
}

func (e *TextAssembler) Atomic(ins machine.Instruction) error {
	if err := e.validErr(func(enc *ByteAssembler) error { return enc.Atomic(ins) }); err != nil {
		return err
	}
	e.expansion(atomicPlan(ins))
	return nil
}
