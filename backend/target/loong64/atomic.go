package loong64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// Sequentially consistent atomics. Loads and stores sit between two dbar 0
// full barriers; naturally aligned byte and halfword accesses are
// single-copy atomic, so they need no word access. 32- and 64-bit
// read-modify-writes are one am<op>_db, which orders like a full barrier on
// both sides. Compare-and-swap is an ll/sc loop between dbar 0 barriers, so
// the failure path is ordered too. Byte and halfword read-modify-writes work
// on the aligned word: And, Or and Xor in one amand_db.w/amor_db.w/amxor_db.w
// with the other bytes at the identity, Add, Swap and compare-and-swap in a
// masked ll.w/sc.w loop. ll.w and am*.w sign-extend, so 32-bit results are
// zero-extended, and the 32-bit compare-and-swap compares with a
// sign-extended copy of the expected value.
//
// R12, R19 and R20 are scratch. The masked loops also use the fixed
// allocatable temporaries atomicTemporaries lists.

// CanAtomic reports whether the target encodes atomic operation at width.
func (Policy) CanAtomic(operation op.Opcode, width uint8) bool {
	return operation >= op.OpAtomicLoad && operation <= op.OpAtomicCompareSwap && validWidth(width)
}

// atomicTemporaries lists the allocatable registers ins's expansion writes
// besides A.
func atomicTemporaries(ins machine.Instruction) []backend.Register {
	if ins.Bits >= 32 || !validWidth(ins.Bits) {
		return nil
	}
	switch ins.Arithmetic {
	case op.OpAtomicAdd, op.OpAtomicSwap:
		return []backend.Register{13}
	case op.OpAtomicCompareSwap:
		return []backend.Register{13, 14}
	}
	return nil
}

// atomicWrites lists every register other than A that ins's expansion
// writes, from Arithmetic and Bits alone.
func atomicWrites(ins machine.Instruction) []backend.Register {
	if !validWidth(ins.Bits) {
		return nil
	}
	switch {
	case ins.Arithmetic == op.OpAtomicLoad || ins.Arithmetic == op.OpAtomicStore:
		return nil
	case ins.Arithmetic == op.OpAtomicCompareSwap && ins.Bits == 64:
		return []backend.Register{12, 19}
	case ins.Arithmetic == op.OpAtomicCompareSwap && ins.Bits == 32:
		return []backend.Register{12, 19, 20}
	case ins.Bits < 32:
		return append([]backend.Register{12, 19, 20}, atomicTemporaries(ins)...)
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
// operands, a destination distinct from the inputs (AM* requires it), and
// no operand in a temporary of the expansion.
func validAtomic(ins machine.Instruction) bool {
	clearUnusedAtomic(&ins)
	if ins.Op != machine.OpAtomic || !validInstructionRegisters(ins) || !validWidth(ins.Bits) ||
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
	temporaries := append(atomicTemporaries(ins), 12, 19, 20)
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

// Atomic expands OpAtomic.
func (e *ByteAssembler) Atomic(ins machine.Instruction) error {
	if !e.require(validAtomic(ins)) {
		return e.err
	}
	d, a, c, r := ins.A, ins.B, ins.C, ins.D
	w := ins.Bits
	start := len(e.plan)
	reg := func(x backend.Register) uint32 { return uint32(x) }
	rrr := func(code uint32, name string, rd, rj, rk backend.Register) {
		e.emit(code|reg(rk)<<10|reg(rj)<<5|reg(rd), "%s r%d, r%d, r%d", name, rd, rj, rk)
	}
	imm12 := func(code uint32, name string, rd, rj backend.Register, v int64) {
		e.emit(code|(uint32(v)&0xfff)<<10|reg(rj)<<5|reg(rd), "%s r%d, r%d, %d", name, rd, rj, v)
	}
	shiftImm := func(code uint32, name string, rd, rj backend.Register, n uint32) {
		e.emit(code|n<<10|reg(rj)<<5|reg(rd), "%s r%d, r%d, %d", name, rd, rj, n)
	}
	dbar := func() { e.emit(0x38720000, "dbar 0") }
	pcOf := func(index int) int { return e.pc + 4*(index-start) }
	// linked emits ll/sc (2RI14 with a zero offset).
	linked := func(code uint32, name string, rd, rj backend.Register) {
		e.emit(code|reg(rj)<<5|reg(rd), "%s r%d, r%d, 0", name, rd, rj)
	}
	// bne branches forward to a later index, which land patches.
	bne := func(rj, rd backend.Register) int {
		e.emit(0x5c000000|reg(rj)<<5|reg(rd), "bne r%d, r%d, 0x%x", rj, rd)
		return len(e.plan) - 1
	}
	land := func(index int) {
		v := uint32(len(e.plan) - index)
		e.plan[index].word |= (v & 0xffff) << 10
		if t := e.plan[index].text; t != nil {
			t.args = append(t.args, pcOf(len(e.plan)))
		}
	}
	// beqz branches back to loop.
	beqz := func(rj backend.Register, loop int) {
		v := uint32(loop - len(e.plan))
		e.emit(0x40000000|(v&0xffff)<<10|reg(rj)<<5|(v>>16&31), "beqz r%d, 0x%x", rj, pcOf(loop))
	}
	fieldMask := func(rd backend.Register) {
		if w == 8 {
			imm12(0x03800000, "ori", rd, 0, 0xff)
			return
		}
		e.emit(0x14000000|0x10<<5|reg(rd), "lu12i.w r%d, 0x10", rd)
		imm12(0x02800000, "addi.w", rd, rd, -1)
	}
	// shift places the byte offset of the address, times eight, in rd.
	shift := func(rd backend.Register) {
		imm12(0x03400000, "andi", rd, a, 3)
		shiftImm(0x00408000, "slli.w", rd, rd, 3)
	}
	aligned := func(rd backend.Register) {
		shiftImm(0x00450000, "srli.d", rd, a, 2)
		shiftImm(0x00410000, "slli.d", rd, rd, 2)
	}
	sllw := func(rd, rj, rk backend.Register) { rrr(0x00170000, "sll.w", rd, rj, rk) }
	srlw := func(rd, rj, rk backend.Register) { rrr(0x00178000, "srl.w", rd, rj, rk) }
	and := func(rd, rj, rk backend.Register) { rrr(0x00148000, "and", rd, rj, rk) }
	xor := func(rd, rj, rk backend.Register) { rrr(0x00158000, "xor", rd, rj, rk) }
	// extract zero-extends the field of the old word in A at shift tmp.
	extract := func(tmp backend.Register) {
		and(d, d, 20)
		shift(tmp)
		srlw(d, d, tmp)
	}
	switch ins.Arithmetic {
	case op.OpAtomicLoad:
		code := map[uint8]uint32{8: 0x2a000000, 16: 0x2a400000, 32: 0x2a800000, 64: 0x28c00000}[w]
		name := map[uint8]string{8: "ld.bu", 16: "ld.hu", 32: "ld.wu", 64: "ld.d"}[w]
		dbar()
		imm12(code, name, d, a, 0)
		dbar()
		return nil
	case op.OpAtomicStore:
		code := map[uint8]uint32{8: 0x29000000, 16: 0x29400000, 32: 0x29800000, 64: 0x29c00000}[w]
		name := map[uint8]string{8: "st.b", 16: "st.h", 32: "st.w", 64: "st.d"}[w]
		dbar()
		imm12(code, name, c, a, 0)
		dbar()
		return nil
	case op.OpAtomicCompareSwap:
		switch w {
		case 32, 64:
			ll, llName, sc, scName, expected := uint32(0x22000000), "ll.d", uint32(0x23000000), "sc.d", c
			if w == 32 {
				ll, llName, sc, scName, expected = 0x20000000, "ll.w", 0x21000000, "sc.w", 20
				imm12(0x02800000, "addi.w", 20, c, 0) // sign-extend C
			}
			dbar()
			loop := len(e.plan)
			linked(ll, llName, 12, a)
			done := bne(12, expected)
			rrr(0x00150000, "or", 19, r, 0)
			linked(sc, scName, 19, a)
			beqz(19, loop)
			land(done)
			dbar()
			if w == 32 {
				e.emit(0x00c00000|31<<16|12<<5|reg(d), "bstrpick.d r%d, r12, 31, 0", d)
			} else {
				rrr(0x00150000, "or", d, 12, 0)
			}
		default:
			// R19 = aligned word, R20 = field mask, R12 = expected and R13 =
			// expected^replacement at the field, A = old word, R14 = new word.
			shift(14)
			aligned(19)
			fieldMask(20)
			sllw(20, 20, 14)
			sllw(12, c, 14)
			xor(13, c, r)
			sllw(13, 13, 14)
			dbar()
			loop := len(e.plan)
			linked(0x20000000, "ll.w", d, 19)
			and(14, d, 20)
			done := bne(14, 12)
			xor(14, d, 13)
			linked(0x21000000, "sc.w", 14, 19)
			beqz(14, loop)
			land(done)
			dbar()
			extract(14)
		}
		return nil
	}
	if w >= 32 {
		code := map[op.Opcode]uint32{op.OpAtomicSwap: 0x38690000, op.OpAtomicAdd: 0x386a0000, op.OpAtomicAnd: 0x386b0000, op.OpAtomicOr: 0x386c0000, op.OpAtomicXor: 0x386d0000}[ins.Arithmetic]
		name := map[op.Opcode]string{op.OpAtomicSwap: "amswap_db", op.OpAtomicAdd: "amadd_db", op.OpAtomicAnd: "amand_db", op.OpAtomicOr: "amor_db", op.OpAtomicXor: "amxor_db"}[ins.Arithmetic]
		if w == 64 {
			code, name = code|0x8000, name+".d"
		} else {
			name += ".w"
		}
		e.emit(code|reg(c)<<10|reg(a)<<5|reg(d), "%s r%d, r%d, r%d", name, d, c, a)
		if w == 32 {
			e.emit(0x00c00000|31<<16|reg(d)<<5|reg(d), "bstrpick.d r%d, r%d, 31, 0", d, d)
		}
		return nil
	}
	switch ins.Arithmetic {
	case op.OpAtomicAnd, op.OpAtomicOr, op.OpAtomicXor:
		// One word AM* with the operand at the field; And keeps the other
		// bytes with ones. R20 = shift, R12 = operand, R19 = aligned word.
		shift(20)
		sllw(12, c, 20)
		if ins.Arithmetic == op.OpAtomicAnd {
			fieldMask(19)
			sllw(19, 19, 20)
			rrr(0x00140000, "nor", 19, 19, 0)
			rrr(0x00150000, "or", 12, 12, 19)
		}
		aligned(19)
		code := map[op.Opcode]uint32{op.OpAtomicAnd: 0x386b0000, op.OpAtomicOr: 0x386c0000, op.OpAtomicXor: 0x386d0000}[ins.Arithmetic]
		name := map[op.Opcode]string{op.OpAtomicAnd: "amand_db.w", op.OpAtomicOr: "amor_db.w", op.OpAtomicXor: "amxor_db.w"}[ins.Arithmetic]
		e.emit(code|12<<10|19<<5|reg(d), "%s r%d, r12, r19", name, d)
		srlw(d, d, 20)
		e.emit(0x00c00000|uint32(w-1)<<16|reg(d)<<5|reg(d), "bstrpick.d r%d, r%d, %d, 0", d, d, w-1)
	default:
		// Masked loop: R19 = aligned word, R20 = field mask, R12 = operand
		// at the field, A = old word, R13 = shift, then the new word.
		shift(13)
		aligned(19)
		fieldMask(20)
		sllw(20, 20, 13)
		sllw(12, c, 13)
		dbar()
		loop := len(e.plan)
		linked(0x20000000, "ll.w", d, 19)
		if ins.Arithmetic == op.OpAtomicAdd {
			rrr(0x00100000, "add.w", 13, d, 12)
			xor(13, 13, d)
		} else {
			xor(13, d, 12)
		}
		and(13, 13, 20)
		xor(13, 13, d)
		linked(0x21000000, "sc.w", 13, 19)
		beqz(13, loop)
		dbar()
		extract(13)
	}
	return nil
}
