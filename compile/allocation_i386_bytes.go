package compile

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/regalloc"
	"github.com/moriyoshi/nyanjit/ir"
)

// Only EAX, ECX, EDX and EBX have a low byte register (AL..BL). In one of
// them an 8-bit result's zero extension is MOVZX r32, r8 (3 bytes) where it
// is otherwise AND r, 0xff (6), an 8-bit store needs no borrowed register
// and SETcc needs no MOV, Jcc and INC. The 386 allocators prefer them for
// such values: a preference only, taken when a byte register is free, so it
// adds no spill.

// byteResult386 reports whether ins writes an 8-bit result whose code ends
// in a zero extension or a SETcc, which a byte destination shortens.
func byteResult386(ins *machineInstruction) bool {
	switch ins.Op {
	case mBinary:
		return ins.Bits == 8
	case mShift:
		return ins.Bits == 8 && ins.Arithmetic != ir.OpShiftRightArithmetic
	case mCompareSet:
		return true
	}
	return false
}

// threeAddress386 reports whether op writes a destination other than its
// operands at no cost, so that it may as well write a byte register.
func threeAddress386(op machine.SelectedOpcode) bool {
	switch op {
	case mLoad8, mLoad16, mLoad32, mLoad64, mLoadOffset, mLoadAddress, mScaled:
		return true
	}
	return false
}

// markByteValues386 sets wants[v] for the one-word values the 386 code
// shortens in a byte register: byteResult386 results, 8-bit store values
// and 8-bit narrowing sources. A single-use value that wants one passes it
// to the first operand of its definition when that can reuse the operand's
// register at its last use, so that the chain stays in one byte register;
// a value with more uses does not, since its copies compete for the byte
// registers with it. lastUse returns a value's last use position (or -1)
// and its number of uses.
func markByteValues386(program *machineProgram, lastUse func(int) (int, int), canReuse func(machineInstruction, int) bool, wants []bool) {
	word := func(id int) bool {
		v := &program.Values[id]
		return v.Words <= 1 && v.Class == backend.General
	}
	for pos := len(program.Instructions) - 1; pos >= 0; pos-- {
		ins := &program.Instructions[pos]
		switch {
		case ins.Op == mStore8 && ins.B >= 0:
			wants[ins.B] = wants[ins.B] || word(ins.B)
		case ins.Op == mNarrow && ins.Imm == 8 && ins.A >= 0:
			wants[ins.A] = wants[ins.A] || word(ins.A)
		}
		if ins.Dst < 0 || !word(ins.Dst) {
			continue
		}
		if byteResult386(ins) {
			wants[ins.Dst] = true
		}
		if !wants[ins.Dst] || ins.A < 0 || !word(ins.A) {
			continue
		}
		switch ins.Op {
		case mMove, mInc, mDec, mNarrow, mShift, mBinary, mBinaryImmediate:
			if _, uses := lastUse(ins.Dst); uses > 1 {
				continue
			}
			if last, _ := lastUse(ins.A); last == pos && canReuse(*ins, 0) {
				wants[ins.A] = true
			}
		}
	}
}

// byteRegister386 returns a free byte register for a value that wants one:
// the first of registers, in the allocator's order, that is a byte register
// free accepts, outside pinned and unowned, skipping a preserved register
// not yet written, whose first write would need a save.
func byteRegister386(alloc *regalloc.Residency, registers []physReg, pinned regalloc.Pins, preserved backend.RegisterSet, written *registerWrites, free func(physReg) bool) (physReg, bool) {
	for _, r := range registers {
		if r < 4 && !pinned.Contains(r) && alloc.Owner(r) == -1 && free(r) && (!preserved.Contains(r) || written[r]) {
			return r, true
		}
	}
	return 0, false
}

// onlineByteChains386 is the 386 byte preference on Online, whose Prefer
// names one register, beats a tie when that register is free, and is
// decided before the walk: a definition tied to the last use of a value
// that wants a byte register prefers EAX only when the first definition of
// its chain saw at least three other general values live, so that the chain
// likely started outside the byte registers, which the allocator gives out
// first. Elsewhere a jump to EAX would cost a MOV for no gain. It returns,
// per value of wants, how many such values lived across its chain's first
// definition. lastUse is describeOnline's, definedAt its definitions; the
// buffers are t's.
func onlineByteChains386(p *machineProgram, lastUse, definedAt []int, wants []bool, t *allocationTemps) []int32 {
	defines := func(id int) (int, bool) {
		pos := definedAt[id]
		return pos, pos >= 0 && pos < len(p.Instructions) && p.Instructions[pos].Dst == id
	}
	last := func(id int) int { return max(lastUse[id], -lastUse[id]) - 1 }
	across := resetBufferTracked(&t.byteLive, &t.history.byteLive, len(p.Instructions)+1)
	for id := range p.Values {
		v := &p.Values[id]
		if v.Constant || v.Class != backend.General {
			continue
		}
		if pos, ok := defines(id); ok && last(id) > pos+1 {
			across[pos+1]++
			across[last(id)]--
		}
	}
	for pos := 1; pos < len(across); pos++ {
		across[pos] += across[pos-1]
	}
	heads := resetBufferTracked(&t.byteHeads, &t.history.byteHeads, len(p.Values))
	for pos := range p.Instructions {
		ins := &p.Instructions[pos]
		if ins.Dst < 0 || ins.Dst >= len(wants) || !wants[ins.Dst] {
			continue
		}
		heads[ins.Dst] = across[pos]
		if a := ins.A; a >= 0 && wants[a] && last(a) == pos {
			if at, ok := defines(a); ok && at < pos {
				heads[ins.Dst] = heads[a]
			}
		}
	}
	return heads
}
