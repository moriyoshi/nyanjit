package compile

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// atomic checks and records an atomic operation or fence: the shape
// Validate requires, as the optimized tier's selection does. Atomics are
// ordered memory operations; a read-modify-write both reads and writes.
func (e *baselineEngine) atomic(id int, ins *ir.Instruction) error {
	r := &e.recs[id]
	if ins.Imm > 0xff || !ir.MemoryOrder(ins.Imm).Valid() {
		return baselineInvalid(id, ins.Op, "memory order %d is not defined", ins.Imm)
	}
	if ins.Op == ir.OpFence {
		if ins.Bits != 0 {
			return baselineInvalid(id, ins.Op, "Fence has no width")
		}
		return nil
	}
	if a := e.recs[r.a].bits; a != 32 && a != 64 {
		return baselineInvalid(id, ins.Op, "atomic operation needs a 32- or 64-bit address")
	}
	for _, x := range [2]uint32{r.b, r.c} {
		if x != noValue && e.recs[x].bits != r.bits {
			return baselineInvalid(id, ins.Op, "atomic operation needs %d-bit operands", r.bits)
		}
	}
	if ins.Op != ir.OpAtomicStore {
		e.memRead, e.loads = true, true
	}
	if ins.Op != ir.OpAtomicLoad {
		e.memWrite, e.stores = true, true
	}
	return nil
}

// atomicOperands is the number of register operands of atomic record op:
// the address, then the operand and the replacement.
func atomicOperands(operation ir.Opcode) int {
	switch operation {
	case ir.OpFence:
		return 0
	case ir.OpAtomicLoad:
		return 1
	case ir.OpAtomicCompareSwap:
		return 3
	}
	return 2
}

// atomicFixedMask is atomicFixedRegisters as an engine register mask.
func atomicFixedMask(architecture string, operation ir.Opcode, bits uint8) uint64 {
	mask := uint64(0)
	for _, r := range atomicFixedRegisters(architecture, operation, bits) {
		mask |= regBit(int8(r))
	}
	return mask
}

// lowerAtomic lowers atomic or fence record r to one pkOp record whose
// registers meet machine.OpAtomic's contract: live values leave the fixed
// registers of the expansion (fixed), operands left in them move to free
// registers, and a result that would share a fixed register, or an operand
// unless alias says the expansion writes its result last, is computed in a
// free register and moved.
func (e *baselineEngine) lowerAtomic(r *bRec, dst int8, src [3]int8, fixed uint64, alias bool) {
	a := &e.alloc
	n := atomicOperands(r.op)
	if fixed != 0 {
		amd64Vacate(e, fixed, dst, src)
	}
	lock := fixed
	if dst >= 0 {
		lock |= regBit(dst)
	}
	for j := 0; j < n; j++ {
		lock |= regBit(src[j])
	}
	for j := 0; j < n; j++ {
		if fixed&regBit(src[j]) == 0 {
			continue
		}
		old, t := src[j], e.allocReg(bankGP, lock, -1, false)
		e.put(bPhys{k: pkMove, a: t, b: old})
		a.used |= regBit(t)
		lock |= regBit(t)
		for k := j; k < n; k++ {
			if src[k] == old {
				src[k] = t
			}
		}
	}
	result := dst
	if dst >= 0 {
		overlap := fixed&regBit(dst) != 0
		for j := 0; j < n && !alias; j++ {
			overlap = overlap || src[j] == dst
		}
		if overlap {
			result = e.allocReg(bankGP, lock, -1, false)
			a.used |= regBit(result)
		}
	}
	a.used |= fixed
	e.put(bPhys{k: pkOp, sub: uint8(r.op), w: r.bits, a: result, b: src[0], c: src[1], d: src[2], imm: r.imm})
	if result != dst {
		e.put(bPhys{k: pkMove, a: dst, b: result})
	}
}

// atomicInstruction is the machine.OpAtomic of atomic record p, with breg
// converting engine registers.
func atomicInstruction(p *bPhys, breg func(int8) backend.Register) machine.Instruction {
	ins := machine.Instruction{Op: machine.OpAtomic, Arithmetic: ir.Opcode(p.sub), Bits: p.w, B: breg(p.b), Imm: p.imm}
	if p.a >= 0 {
		ins.A = breg(p.a)
	}
	if n := atomicOperands(ir.Opcode(p.sub)); n > 1 {
		ins.C = breg(p.c)
		if n > 2 {
			ins.D = breg(p.d)
		}
	}
	return ins
}

// i386PoolRun returns the first of n adjacent free pool slots.
func i386PoolRun(e *baselineEngine, n int32) int32 {
	a := &e.alloc
	free := func(s int32) int {
		for i, t := range a.poolFree {
			if t == s {
				return i
			}
		}
		return -1
	}
	for _, s := range a.poolFree {
		k := int32(1)
		for k < n && free(s+k) >= 0 {
			k++
		}
		if k == n {
			for k = range n {
				i := free(s + k)
				a.poolFree = append(a.poolFree[:i], a.poolFree[i+1:]...)
			}
			return s
		}
	}
	s := e.newSlot()
	a.pool = append(a.pool, s)
	for range n - 1 {
		a.pool = append(a.pool, e.newSlot())
	}
	return s
}

// i386LowerAtomic lowers a 386 atomic record. Narrow ones use EAX and EDX,
// which the engine never allocates, and write their result last. A 64-bit
// one stores its address and XMM operands to three adjacent pool slots (the
// pair expansion's five words), saves EBX and ESI, which the expansion
// writes, to a fourth, and moves the EDX:EAX result to its XMM register.
func i386LowerAtomic(e *baselineEngine, r *bRec, dst int8, src [3]int8) {
	if r.op == ir.OpFence || r.bits < 64 {
		e.lowerAtomic(r, dst, src, regBit(x86EAX)|regBit(x86EDX), true)
		return
	}
	a := &e.alloc
	a.used |= regBit(x86EBX) | regBit(x86ESI)
	base := i386PoolRun(e, 3)
	save := e.poolSlot()
	e.put(bPhys{k: pkOp, sub: uint8(r.op), w: 64, a: dst, b: src[0], c: src[1], d: src[2], imm: r.imm | uint64(base)<<8 | uint64(save)<<24})
	e.freePoolSlot(save)
	for k := range int32(3) {
		e.freePoolSlot(base + k)
	}
}

// atomic encodes 386 atomic record p.
func (x *i386Enc) atomic(p *bPhys) error {
	a := x.a
	if ir.Opcode(p.sub) == ir.OpFence {
		return a.Barrier(machine.BarrierMemory)
	}
	ins := atomicInstruction(p, rx)
	if p.w < 64 {
		ins.B = x.gp(p.b, gECX)
		return a.Atomic(ins)
	}
	base, save := wslot(p.imm>>8&0xffff), wslot(p.imm>>24&0xffff)
	if isXMM(p.b) {
		if err := x.spillX(rx(p.b), p.imm>>8&0xffff); err != nil {
			return err
		}
	} else {
		a.Spill(rx(p.b), base)
	}
	operands := [2]int8{p.c, p.d}
	for k, r := range operands[:atomicOperands(ir.Opcode(p.sub))-1] {
		if err := a.FloatMemory(machine.Instruction{Op: machine.OpFloatSpill, Bits: 64, A: rx(r), Slot: base + 1 + 2*k}); err != nil {
			return err
		}
	}
	a.Spill(gEBX, save)
	a.Spill(gESI, save+1)
	err := a.Atomic(machine.Instruction{Op: machine.OpAtomic, Arithmetic: ins.Arithmetic, Bits: 64, A: gEAX, HighA: gEDX, Imm: p.imm & 0xff, Slot: base})
	a.Reload(gEBX, save)
	a.Reload(gESI, save+1)
	if p.a >= 0 {
		x.fromPair(rx(p.a))
	}
	return err
}
