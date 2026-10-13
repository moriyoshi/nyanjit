package arm64

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// Atomics are sequentially consistent. Loads are LDAR and stores STLR. The
// ARMv8.0 read-modify-writes are LDAXR/STLXR loops in X16 (the old value) and
// X17 (the new value), with the store-exclusive status in A, which is written
// last with the old value. No memory access sits between the exclusive pair,
// and the loop branches only to its own start.
//
// With FEAT_LSE (the unexported lse switch), read-modify-writes are single
// LDADDAL, LDCLRAL, LDSETAL, LDEORAL and SWPAL instructions, and
// compare-and-swap is MOV A, C; CASAL A, D, [B]. The byte and halfword forms
// zero-extend the old value, as LDAXRB and LDAXRH do.

// CanAtomic reports whether the target encodes atomic operation at width.
func (Policy) CanAtomic(operation op.Opcode, width uint8) bool {
	return operation >= op.OpAtomicLoad && operation <= op.OpAtomicCompareSwap && validWidth(width)
}

// atomicReadsC and atomicReadsD report the operands an atomic operation reads.
func atomicReadsC(operation op.Opcode) bool { return operation != op.OpAtomicLoad }
func atomicReadsD(operation op.Opcode) bool { return operation == op.OpAtomicCompareSwap }

// validAtomic checks an OpAtomic: GP registers below 31, none of them X16 or
// X17, and a destination distinct from every input it outlives.
func validAtomic(ins *machine.Instruction) bool {
	clearUnused(ins)
	if ins.Op != machine.OpAtomic || !validWidth(ins.Bits) || ins.Imm > 0xff || !op.MemoryOrder(ins.Imm).Valid() ||
		ins.Arithmetic < op.OpAtomicLoad || ins.Arithmetic > op.OpAtomicCompareSwap || ins.ByteOrder != op.ByteOrderNative {
		return false
	}
	usable := func(r backend.Register) bool { return r < 31 && r != 16 && r != 17 }
	o := ins.Arithmetic
	if !usable(ins.B) || atomicReadsC(o) && !usable(ins.C) || atomicReadsD(o) && !usable(ins.D) {
		return false
	}
	if o == op.OpAtomicStore {
		return true
	}
	return usable(ins.A) && ins.A != ins.B && (!atomicReadsC(o) || ins.A != ins.C) && (!atomicReadsD(o) || ins.A != ins.D)
}

// atomicPlan expands a valid OpAtomic; text adds the listing.
func atomicPlan(ins machine.Instruction, lse, text bool) concretePlan {
	var out concretePlan
	if !validAtomic(&ins) {
		return out
	}
	emit := func(word uint32) { out.parts[out.n].word = word; out.n++ }
	listing := func(format string, args ...any) { out.parts[out.n-1].text = fmt.Sprintf(format, args...) }
	w := ins.Bits
	a, b, c, d := uint32(ins.A), uint32(ins.B), uint32(ins.C), uint32(ins.D)
	// size is the load/store size field; sf selects the X view of ALU words.
	size, sf, r, suffix := uint32(3), uint32(1)<<31, "X", ""
	switch w {
	case 8:
		size, sf, r, suffix = 0, 0, "W", "b"
	case 16:
		size, sf, r, suffix = 1, 0, "W", "h"
	case 32:
		size, sf, r = 2, 0, "W"
	}
	sz := size << 30
	o := ins.Arithmetic
	switch o {
	case op.OpAtomicLoad:
		emit(sz | 0x08DFFC00 | b<<5 | a)
		if text {
			listing("ldar%s %s%d, [X%d]", suffix, r, a, b)
		}
		return out
	case op.OpAtomicStore:
		emit(sz | 0x089FFC00 | b<<5 | c)
		if text {
			listing("stlr%s %s%d, [X%d]", suffix, r, c, b)
		}
		return out
	}
	if lse {
		mnemonic, opc, rs := "", uint32(0), c
		switch o {
		case op.OpAtomicAdd:
			mnemonic = "ldaddal"
		case op.OpAtomicAnd:
			// LDCLR clears the bits set in Rs: and with C is a clear of ~C.
			emit(sf | 0x2A2003E0 | c<<16 | 16)
			if text {
				listing("mvn %s16, %s%d", r, r, c)
			}
			mnemonic, opc, rs = "ldclral", 0x1000, 16
		case op.OpAtomicOr:
			mnemonic, opc = "ldsetal", 0x3000
		case op.OpAtomicXor:
			mnemonic, opc = "ldeoral", 0x2000
		case op.OpAtomicSwap:
			mnemonic, opc = "swpal", 0x8000
		case op.OpAtomicCompareSwap:
			emit(0xAA0003E0 | c<<16 | a)
			if text {
				listing("mov X%d, X%d", a, c)
			}
			emit(sz | 0x08E0FC00 | a<<16 | b<<5 | d)
			if text {
				listing("casal%s %s%d, %s%d, [X%d]", suffix, r, a, r, d, b)
			}
			return out
		}
		emit(sz | 0x38E00000 | opc | rs<<16 | b<<5 | a)
		if text {
			listing("%s%s %s%d, %s%d, [X%d]", mnemonic, suffix, r, rs, r, a, b)
		}
		return out
	}
	// L: LDAXR R16, [B]
	emit(sz | 0x085FFC00 | b<<5 | 16)
	if text {
		listing("ldaxr%s %s16, [X%d]", suffix, r, b)
	}
	value := c
	switch o {
	case op.OpAtomicAdd, op.OpAtomicAnd, op.OpAtomicOr, op.OpAtomicXor:
		word, mnemonic := uint32(0x0B000000), "add"
		switch o {
		case op.OpAtomicAnd:
			word, mnemonic = 0x0A000000, "and"
		case op.OpAtomicOr:
			word, mnemonic = 0x2A000000, "orr"
		case op.OpAtomicXor:
			word, mnemonic = 0x4A000000, "eor"
		}
		emit(sf | word | c<<16 | 16<<5 | 17)
		if text {
			listing("%s %s17, %s16, %s%d", mnemonic, r, r, r, c)
		}
		value = 17
	case op.OpAtomicCompareSwap:
		cmp := uint32(0x6B00001F)
		if w == 64 {
			cmp = 0xEB00001F
		}
		emit(cmp | c<<16 | 16<<5)
		if text {
			listing("cmp %s16, %s%d", r, r, c)
		}
		// B.NE to the final MOV, three words ahead.
		emit(0x54000000 | 3<<5 | 1)
		if text {
			listing("b.ne .+12")
		}
		value = d
	}
	emit(sz | 0x0800FC00 | a<<16 | b<<5 | value)
	if text {
		listing("stlxr%s W%d, %s%d, [X%d]", suffix, a, r, value, b)
	}
	// CBNZ WA back to the LDAXR.
	back := uint32(out.n) & 0x7ffff
	emit(0x35000000 | (-back&0x7ffff)<<5 | a)
	if text {
		listing("cbnz W%d, .-%d", a, 4*back)
	}
	emit(0xAA0003E0 | 16<<16 | a)
	if text {
		listing("mov X%d, X16", a)
	}
	return out
}

// atomicWritesFlags reports whether the ARMv8.0 expansion compares: only
// compare-and-swap does.
func atomicWritesFlags(ins machine.Instruction) bool {
	return ins.Op == machine.OpAtomic && ins.Arithmetic == op.OpAtomicCompareSwap
}

// atomicSize is the byte size of an OpAtomic, or 0 when it is rejected.
func atomicSize(ins machine.Instruction, lse bool) int { return 4 * atomicPlan(ins, lse, false).n }

// Atomic implements OpAtomic.
func (e *ByteAssembler) Atomic(ins machine.Instruction) error {
	p := atomicPlan(ins, e.lse, false)
	if !e.require(p.n != 0) {
		return e.err
	}
	for _, i := range p.parts[:p.n] {
		e.words = append(e.words, i.word)
	}
	return nil
}

// Atomic renders exactly the plan used by native emission.
func (e *TextAssembler) Atomic(ins machine.Instruction) error {
	if err := e.validErr(func(enc *ByteAssembler) error { return enc.Atomic(ins) }); err != nil {
		return err
	}
	p := atomicPlan(ins, e.lse, true)
	for _, i := range p.parts[:p.n] {
		e.line("%s", i.text)
	}
	return nil
}
