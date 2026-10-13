package ppc64

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// Atomics are sequentially consistent, with the mappings of the C++11 to
// POWER tables. A load is sync, the load, then a compare and never-taken
// branch on the loaded value followed by isync, which keeps later accesses
// after it. A store is sync, then the store. A read-modify-write is a
// load-reserve/store-conditional loop between sync and isync: the old value
// is in r11 and the new one in r12, and only the loop's own stcx. failure
// branches back. Compare-and-swap leaves the loop with bne- when the old
// value differs, still through the isync. lbarx and lharx are ISA 2.06, so
// POWER8 has every width. Each reservation load zero-extends; mr copies the
// old value to A last.

// CanAtomic reports whether the target encodes atomic operation at width.
func (Policy) CanAtomic(operation op.Opcode, width uint8) bool {
	return operation >= op.OpAtomicLoad && operation <= op.OpAtomicCompareSwap &&
		(width == 8 || width == 16 || width == 32 || width == 64)
}

// atomicLoad, atomicStore, atomicReserve and atomicConditional give the
// native forms of width w.
func atomicForms(w uint8) (load, store, reserve, conditional Opcode) {
	switch w {
	case 8:
		return Lbz, Stb, lbarx, stbcx
	case 16:
		return Lhz, Sth, lharx, sthcx
	case 32:
		return Lwz, Stw, lwarx, stwcx
	}
	return Ld, Std, ldarx, stdcx
}

// atomic expands OpAtomic. r0 cannot be the D-form address base, and r11 and
// r12 are the loop temporaries, so no operand may name them; A, when
// written, differs from every input.
func (p *expansion) atomic(ins machine.Instruction) error {
	w, o := ins.Bits, ins.Arithmetic
	if w != 8 && w != 16 && w != 32 && w != 64 || o < op.OpAtomicLoad || o > op.OpAtomicCompareSwap ||
		ins.Imm > 0xff || !op.MemoryOrder(ins.Imm).Valid() {
		return machine.ErrUnsupportedOperation
	}
	inputs := []backend.Register{ins.B}
	if o != op.OpAtomicLoad {
		inputs = append(inputs, ins.C)
	}
	if o == op.OpAtomicCompareSwap {
		inputs = append(inputs, ins.D)
	}
	registers := inputs
	if o != op.OpAtomicStore {
		registers = append(registers, ins.A)
	}
	for _, r := range registers {
		if r > 31 {
			return machine.ErrUnsupportedOperation
		}
		if r == 0 || r == 1 || r == 11 || r == 12 {
			return machine.ErrInvalidInstruction
		}
	}
	if o != op.OpAtomicStore {
		for _, r := range inputs {
			if r == ins.A {
				return machine.ErrInvalidInstruction
			}
		}
	}
	load, store, reserve, conditional := atomicForms(w)
	compareBits := uint8(32)
	if w == 64 {
		compareBits = 64
	}
	p.emit(Instruction{Op: Sync})
	switch o {
	case op.OpAtomicLoad:
		p.emit(Instruction{Op: load, A: ins.A, B: ins.B})
		p.emit(Instruction{Op: Cmp, B: ins.A, C: ins.A, Bits: compareBits})
		p.emit(Instruction{Op: Bc, BO: 4, BI: 2, Imm: 4})
		p.emit(Instruction{Op: Isync})
		return nil
	case op.OpAtomicStore:
		p.emit(Instruction{Op: store, A: ins.C, B: ins.B})
		return nil
	}
	loop := len(p.instructions)
	p.emit(Instruction{Op: reserve, A: 11, C: ins.B})
	value := ins.C
	switch o {
	case op.OpAtomicAdd, op.OpAtomicAnd, op.OpAtomicOr, op.OpAtomicXor:
		operation := map[op.Opcode]Opcode{op.OpAtomicAdd: Add, op.OpAtomicAnd: And, op.OpAtomicOr: Or, op.OpAtomicXor: Xor}[o]
		p.emit(Instruction{Op: operation, A: 12, B: 11, C: ins.C})
		value = 12
	case op.OpAtomicCompareSwap:
		p.emit(Instruction{Op: Cmp, B: 11, C: ins.C, Bits: compareBits})
		// bne- past the stcx. and its retry branch.
		p.emit(Instruction{Op: Bc, BO: 4, BI: 2, Imm: 12})
		value = ins.D
	}
	p.emit(Instruction{Op: conditional, A: value, C: ins.B})
	p.emit(Instruction{Op: Bc, BO: 4, BI: 2, Imm: int64(4 * (loop - len(p.instructions)))})
	p.emit(Instruction{Op: Isync})
	p.move(ins.A, 11)
	return nil
}

// atomicReservationText names a load-reserve or store-conditional form, or
// returns "" for another opcode.
func atomicReservationText(ins Instruction) string {
	name := map[Opcode]string{lbarx: "lbarx", lharx: "lharx", lwarx: "lwarx", ldarx: "ldarx",
		stbcx: "stbcx.", sthcx: "sthcx.", stwcx: "stwcx.", stdcx: "stdcx."}[ins.Op]
	if name == "" {
		return ""
	}
	return fmt.Sprintf("%s r%d, %d, r%d", name, ins.A, ins.B, ins.C)
}

// atomicReservationWord encodes a load-reserve (EH=0) or store-conditional
// (Rc=1) X-form word, with RA in B and RB in C.
func atomicReservationWord(ins Instruction) (uint32, bool) {
	xo := map[Opcode]uint32{lbarx: 52, lharx: 116, lwarx: 20, ldarx: 84, stbcx: 694, sthcx: 726, stwcx: 150, stdcx: 214}[ins.Op]
	if xo == 0 {
		return 0, false
	}
	word := 31<<26 | uint32(ins.A)<<21 | uint32(ins.B)<<16 | uint32(ins.C)<<11 | xo<<1
	if ins.storeConditional() {
		word |= 1
	}
	return word, true
}

// storeConditional reports a st{b,h,w,d}cx. form, which records CR0.
func (ins Instruction) storeConditional() bool {
	return ins.Op == stbcx || ins.Op == sthcx || ins.Op == stwcx || ins.Op == stdcx
}
