package riscv64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// Sequentially consistent atomics follow the RVWMO mapping of the
// unprivileged specification (Table A.6): loads are fence rw,rw; l; fence
// r,rw, stores fence rw,w; s, read-modify-writes amo<op>.aqrl, and
// compare-and-swap an lr.aqrl/sc.rl loop. A naturally aligned byte or
// halfword load or store is single-copy atomic, so those need no word access.
// Byte and halfword read-modify-writes work on the aligned word: And, Or and
// Xor in one word AMO with the other bytes masked to the identity, Add, Swap
// and CAS in a masked lr.w/sc.w loop. Every LR/SC loop is a constrained loop:
// the setup and the result extraction stay outside it, and inside are at most
// six base-I instructions and the retry branch.
//
// X30 and X31 are scratch. The masked loops also use fixed allocatable
// temporaries (atomicTemporaries), which the allocator keeps free.

// CanAtomic reports whether the target encodes atomic operation at width.
func (Policy) CanAtomic(operation op.Opcode, width uint8) bool {
	return operation >= op.OpAtomicLoad && operation <= op.OpAtomicCompareSwap && validWidth(width)
}

// atomicTemporaries lists the allocatable registers ins's expansion writes
// besides A, X30 and X31.
func atomicTemporaries(ins machine.Instruction) []backend.Register {
	if ins.Bits >= 32 || !validWidth(ins.Bits) {
		return nil
	}
	switch ins.Arithmetic {
	case op.OpAtomicAdd, op.OpAtomicSwap:
		return []backend.Register{5, 6, 7}
	case op.OpAtomicCompareSwap:
		return []backend.Register{5, 6, 7, 28}
	}
	return nil
}

// atomicWrites lists every register other than A that ins's expansion
// writes: the temporaries and X30/X31.
func atomicWrites(ins machine.Instruction) []backend.Register {
	if !validWidth(ins.Bits) {
		return nil
	}
	switch {
	case ins.Arithmetic == op.OpAtomicLoad || ins.Arithmetic == op.OpAtomicStore:
		return nil
	case ins.Arithmetic == op.OpAtomicCompareSwap || ins.Bits < 32:
		return append(atomicTemporaries(ins), 30, 31)
	}
	return nil
}

// clearUnusedAtomic zeroes the registers ins's operation does not read: a
// store has no A, a load no C, and only compare-and-swap reads D.
func clearUnusedAtomic(ins *machine.Instruction) {
	switch ins.Arithmetic {
	case op.OpAtomicStore:
		ins.A = 0
	case op.OpAtomicLoad:
		ins.C = 0
	}
	if ins.Arithmetic != op.OpAtomicCompareSwap {
		ins.D = 0
	}
}

// validAtomic checks an OpAtomic: a defined operation, width and order, GP
// operands, a destination distinct from the inputs, and no operand in a
// temporary of the expansion.
func validAtomic(ins machine.Instruction) bool {
	clearUnusedAtomic(&ins)
	if ins.Op != machine.OpAtomic || !validInstructionRegisters(&ins) || !validWidth(ins.Bits) ||
		ins.Arithmetic < op.OpAtomicLoad || ins.Arithmetic > op.OpAtomicCompareSwap ||
		ins.Imm > 0xff || !op.MemoryOrder(ins.Imm).Valid() || ins.ByteOrder != op.ByteOrderNative {
		return false
	}
	inputs := [3]backend.Register{ins.B, ins.C, ins.D}
	n := 2
	switch ins.Arithmetic {
	case op.OpAtomicLoad:
		n = 1
	case op.OpAtomicCompareSwap:
		n = 3
	}
	temporaries := append(atomicTemporaries(ins), 30, 31)
	for _, r := range inputs[:n] {
		for _, t := range temporaries {
			if r == t {
				return false
			}
		}
		if ins.Arithmetic != op.OpAtomicStore && r == ins.A {
			return false
		}
	}
	if ins.Arithmetic != op.OpAtomicStore {
		if ins.A == 0 {
			return false
		}
		for _, t := range temporaries {
			if ins.A == t {
				return false
			}
		}
	}
	return true
}

const (
	amoAdd  = 0x00
	amoSwap = 0x01
	amoLR   = 0x02
	amoSC   = 0x03
	amoXor  = 0x04
	amoOr   = 0x08
	amoAnd  = 0x0c
)

// amoWord encodes an A-extension instruction; funct3 2 is .w and 3 is .d,
// ordering 3 is .aqrl and 1 is .rl.
func amoWord(funct5, ordering, rs2, rs1, funct3, rd uint32) uint32 {
	return rWord(funct5<<2|ordering, rs2, rs1, funct3, rd, 0x2f)
}

// atomicWords expands a valid OpAtomic.
func atomicWords(ins machine.Instruction) []uint32 {
	d, a, c, r := uint32(ins.A), uint32(ins.B), uint32(ins.C), uint32(ins.D)
	w := ins.Bits
	var out []uint32
	emit := func(words ...uint32) { out = append(out, words...) }
	add := func(rd, rs1, rs2 uint32) uint32 { return rWord(0, rs2, rs1, 0, rd, 0x33) }
	xor := func(rd, rs1, rs2 uint32) uint32 { return rWord(0, rs2, rs1, 4, rd, 0x33) }
	or := func(rd, rs1, rs2 uint32) uint32 { return rWord(0, rs2, rs1, 6, rd, 0x33) }
	and := func(rd, rs1, rs2 uint32) uint32 { return rWord(0, rs2, rs1, 7, rd, 0x33) }
	sll := func(rd, rs1, rs2 uint32) uint32 { return rWord(0, rs2, rs1, 1, rd, 0x33) }
	srl := func(rd, rs1, rs2 uint32) uint32 { return rWord(0, rs2, rs1, 5, rd, 0x33) }
	andi := func(rd, rs1 uint32, imm int64) uint32 { return iWord(imm, rs1, 7, rd, 0x13) }
	slli := func(rd, rs1 uint32, n int64) uint32 { return iWord(n, rs1, 1, rd, 0x13) }
	// branch is patched once its target index is known.
	branch := func(funct3, rs1, rs2 uint32) int {
		out = append(out, funct3<<12|rs2<<20|rs1<<15)
		return len(out) - 1
	}
	land := func(at, target int) {
		word := out[at]
		out[at] = bWord((target-at)*4, word>>20&31, word>>15&31, word>>12&7)
	}
	fieldMask := func(rd uint32) {
		if w == 8 {
			emit(iWord(0xff, 0, 0, rd, 0x13)) // addi rd, x0, 0xff
			return
		}
		emit(0x10<<12|rd<<7|0x37, iWord(-1, rd, 0, rd, 0x1b)) // lui rd, 0x10; addiw rd, rd, -1
	}
	// shift places the byte offset of the address, times eight, in rd.
	shift := func(rd uint32) { emit(andi(rd, a, 3), slli(rd, rd, 3)) }
	// extract zero-extends the field at shift X6 of the word in X30 into A.
	extract := func() {
		emit(and(d, 30, 7))
		shift(6)
		emit(srl(d, d, 6))
	}
	size := uint32(2) // .w
	if w == 64 {
		size = 3
	}
	switch ins.Arithmetic {
	case op.OpAtomicLoad:
		load := map[uint8]uint32{8: 4, 16: 5, 32: 6, 64: 3}[w] // lbu, lhu, lwu, ld
		emit(0x0330000f, iWord(0, a, load, d, 0x03), 0x0230000f)
		return out
	case op.OpAtomicStore:
		store := map[uint8]uint32{8: 0, 16: 1, 32: 2, 64: 3}[w]
		emit(0x0310000f, sWord(0, c, a, store))
		return out
	case op.OpAtomicCompareSwap:
		switch w {
		case 64:
			loop := len(out)
			emit(amoWord(amoLR, 3, 0, a, 3, 30))
			done := branch(1, 30, c)
			emit(amoWord(amoSC, 1, r, a, 3, 31))
			retry := branch(1, 31, 0)
			land(retry, loop)
			land(done, len(out))
			emit(iWord(0, 30, 0, d, 0x13)) // mv A, X30
		case 32:
			emit(iWord(0, c, 0, 31, 0x1b)) // sext.w X31, C
			loop := len(out)
			emit(amoWord(amoLR, 3, 0, a, 2, 30))
			done := branch(1, 30, 31)
			emit(amoWord(amoSC, 1, r, a, 2, d))
			retry := branch(1, d, 0)
			land(retry, loop)
			land(done, len(out))
			emit(zeroExtendWords(d, 30, 32)...)
		default:
			// X5 = aligned word, X7 = field mask, X28 = expected and X31 =
			// expected^replacement at the field, X30 = old word, X6 = new word.
			shift(6)
			emit(andi(5, a, -4))
			fieldMask(7)
			emit(sll(7, 7, 6), sll(28, c, 6), xor(31, c, r), sll(31, 31, 6))
			loop := len(out)
			emit(amoWord(amoLR, 3, 0, 5, 2, 30), and(6, 30, 7))
			done := branch(1, 6, 28)
			emit(xor(6, 30, 31), amoWord(amoSC, 1, 6, 5, 2, d))
			retry := branch(1, d, 0)
			land(retry, loop)
			land(done, len(out))
			extract()
		}
		return out
	}
	funct5 := map[op.Opcode]uint32{op.OpAtomicAdd: amoAdd, op.OpAtomicAnd: amoAnd, op.OpAtomicOr: amoOr, op.OpAtomicXor: amoXor, op.OpAtomicSwap: amoSwap}[ins.Arithmetic]
	if w >= 32 {
		emit(amoWord(funct5, 3, c, a, size, d))
		if w == 32 {
			emit(zeroExtendWords(d, d, 32)...)
		}
		return out
	}
	switch ins.Arithmetic {
	case op.OpAtomicAnd, op.OpAtomicOr, op.OpAtomicXor:
		// One word AMO with the operand at the field; And keeps the other
		// bytes with ones. X31 = shift, then the operand; X30 = aligned word.
		// The shift is recomputed for the extraction, so the AMO's
		// destination differs from both of its sources.
		shift(31)
		if ins.Arithmetic == op.OpAtomicAnd {
			fieldMask(d)
			emit(sll(d, d, 31), iWord(-1, d, 4, d, 0x13)) // A = ^(mask << shift)
		}
		emit(sll(31, c, 31))
		if ins.Arithmetic == op.OpAtomicAnd {
			emit(or(31, 31, d))
		}
		emit(andi(30, a, -4), amoWord(funct5, 3, 31, 30, 2, d))
		shift(31)
		emit(srl(d, d, 31))
		if w == 8 {
			emit(andi(d, d, 0xff))
		} else {
			emit(slli(d, d, 48), iWord(48, d, 5, d, 0x13))
		}
	default:
		// Masked loop: X5 = aligned word, X7 = field mask, X31 = operand at
		// the field, X30 = old word, A = new word, X6 = SC status.
		shift(6)
		emit(andi(5, a, -4))
		fieldMask(7)
		emit(sll(7, 7, 6), sll(31, c, 6))
		loop := len(out)
		emit(amoWord(amoLR, 3, 0, 5, 2, 30))
		if ins.Arithmetic == op.OpAtomicAdd {
			emit(add(d, 30, 31), xor(d, d, 30))
		} else {
			emit(xor(d, 30, 31))
		}
		emit(and(d, d, 7), xor(d, d, 30), amoWord(amoSC, 1, d, 5, 2, 6))
		retry := branch(1, 6, 0)
		land(retry, loop)
		extract()
	}
	return out
}

// Atomic expands OpAtomic; see atomicWords.
func (e *ByteAssembler) Atomic(ins machine.Instruction) error {
	if !e.require(validAtomic(ins)) {
		return e.err
	}
	e.words = append(e.words, atomicWords(ins)...)
	return nil
}
