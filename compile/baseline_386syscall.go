package compile

import "github.com/moriyoshi/nyanjit/ir"

// i386SyscallRegs are the Linux INT 0x80 registers of the number and the
// six arguments.
var i386SyscallRegs = [7]int8{x86EAX, x86EBX, x86ECX, x86EDX, x86ESI, x86EDI, x86EBP}

// i386Syscall lowers system call record r, value i. The number and the
// arguments pass their low words: a move to a general register reads the
// low word of a 64-bit value in an XMM register or a slot. The values that
// live past the call leave the argument registers, except EBP: a sixth
// argument overwrites it after its old value is saved to a slot, and INT
// 0x80 is followed at once by its restore. The result is EAX sign-extended
// to EDX:EAX, so a -errno result reads as on the 64-bit targets.
func i386Syscall(e *baselineEngine, i uint32, r *bRec) error {
	a := &e.alloc
	if _, err := e.callEffect(r); err != nil {
		return err
	}
	args := e.args[r.x : r.x+uint32(r.n)]
	if len(args) == 0 || len(args) > len(i386SyscallRegs) {
		return ErrBackendLimit
	}
	dsts := i386SyscallRegs[:len(args)]
	ebp := int32(-1)
	over := regBit(x86EAX) | regBit(x86EDX)
	for _, reg := range dsts {
		if reg != x86EBP {
			over |= regBit(reg)
		}
	}
	a.callers |= regBit(x86EAX)
	liveAfter := func(v uint32) bool {
		for j, x := range args {
			if x == v {
				return a.argNu[int(r.x)+j] != noValue
			}
		}
		return a.vs[v].next != noValue
	}
	for m := ^a.free & e.m.allocatable & over; m != 0; m &= m - 1 {
		reg := int8(trailingZeros(m))
		v := a.regVal[reg]
		if v == noValue || !liveAfter(v) {
			continue
		}
		if t := a.free & e.m.allocatable &^ over &^ a.pinned & bankMask(regBank(reg)); t != 0 {
			to := int8(trailingZeros(t))
			e.put(bPhys{k: pkMove, a: to, b: reg})
			a.regVal[reg] = noValue
			a.free |= regBit(reg)
			e.bind(v, to)
			continue
		}
		e.evict(reg)
	}
	if len(args) == len(i386SyscallRegs) {
		ebp = e.poolSlot()
		e.put(bPhys{k: pkSpill, w: 32, a: x86EBP, imm: uint64(ebp)})
	}
	for k, v := range args {
		e.addMove(int16(dsts[k]), v)
		a.used |= regBit(dsts[k])
	}
	e.resolveMoves()
	e.put(bPhys{k: pkSyscall})
	if ebp >= 0 {
		e.put(bPhys{k: pkReload, w: 32, a: x86EBP, imm: uint64(ebp)})
		e.freePoolSlot(ebp)
	}
	a.hasCall, a.flags = true, true
	a.used |= over | i386Temps
	for m := over & e.m.allocatable; m != 0; m &= m - 1 {
		if reg := int8(trailingZeros(m)); a.regVal[reg] != noValue {
			e.unbind(reg)
		}
	}
	for j, v := range args {
		dup := false
		for q := 0; q < j; q++ {
			dup = dup || args[q] == v
		}
		if !dup {
			e.advance(v, a.argNu[int(r.x)+j])
		}
	}
	if a.vs[i].next == noValue {
		return nil
	}
	if e.recs[i].bits == 64 {
		dst := e.def(i, bankFP, 0)
		e.put(bPhys{k: pkOp, sub: uint8(ir.OpSignExtend), w: 64, a: dst, b: x86EAX, imm: 32})
	} else {
		dst := e.def(i, bankGP, regBit(x86EAX)|regBit(x86EDX))
		e.put(bPhys{k: pkMove, a: dst, b: x86EAX})
	}
	e.defined(i)
	return nil
}
