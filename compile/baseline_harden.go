package compile

import (
	"crypto/rand"
	"encoding/binary"
	"slices"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	targetppc64 "github.com/moriyoshi/nyanjit/backend/target/ppc64"
	"github.com/moriyoshi/nyanjit/ir"
)

// Func.Harden in the baseline tier gives the guarantees of the optimized
// tier's hardening (docs/optional-hardening-design.md) with the engine's own
// code shapes:
//
//   - Constant blinding. After analysis, every integer constant becomes a
//     value computed once from a fresh crypto/rand key and its XOR-encoded
//     bits, then allocated, spilled and carried across blocks like any other
//     value: it is never rematerialized, folded into an instruction, or
//     loaded from the literal pool. Shift and rotation counts and
//     OpAddScaled scales become blinded register operands, and a comparison
//     with a nonzero immediate compares with a blinded register. Symbol
//     relocations stay host-supplied addresses.
//   - Control flow. Every label the engine binds, except the epilogue,
//     begins with the target's speculation barrier; targets without one
//     (riscv64, loong64) reject functions with labels with ErrUnsupported,
//     as the optimized tier does. A bounds check (an unsigned-less comparison whose
//     success edge passes the compared value as its second argument, the
//     shape of IndexedAddress and Data.Index) clamps that argument to zero
//     on its success edge with a flag-dependent select (CMOV, CSEL, ISEL),
//     on a path of its own. Hardened comparisons always set their own
//     flags: an FP compare is not fused into its branch.
//   - Entry placement. The Template randomizes the executable entry.
//
// The scratch registers the sequences use, and the clamped register, are
// reported as written.

// Records that hardening rewrites, after analysis, so that allocation lowers
// them here.
const (
	// bOpBlind is a non-symbol OpConst: imm is the constant.
	bOpBlind ir.Opcode = 246 + iota
	// bOpBlindScaled is an OpAddScaled: a + b×imm with imm blinded.
	bOpBlindScaled
	// bOpBlindShift is a shift or rotation by the constant imm; x holds the
	// original opcode.
	bOpBlindShift
)

// Physical kinds of hardened code. Their values leave room for the engine's
// own kinds.
const (
	// pkBarrier is the target's speculation barrier.
	pkBarrier uint8 = 200 + iota
	// pkClamp keeps a when the last comparison found its left operand below
	// its right one, and otherwise copies b, which holds zero, into a. It
	// leaves the flags as they are.
	pkClamp
)

func init() {
	baselineShapes[bOpBlind] = opShape{pure: true, value: true, ok: true}
	baselineShapes[bOpBlindScaled] = opShape{operands: 2, pure: true, value: true, ok: true}
	baselineShapes[bOpBlindShift] = opShape{operands: 1, pure: true, value: true, ok: true}
	for _, o := range []ir.Opcode{bOpBlind, bOpBlindScaled, bOpBlindShift} {
		baselineValue[o] = true
	}
}

// baselineShiftBy maps a shift or rotation by an immediate to its form with
// a count operand, which gives the same result for every count below the
// width.
var baselineShiftBy = [256]ir.Opcode{
	ir.OpShiftLeft:            ir.OpShiftLeftBy,
	ir.OpShiftRight:           ir.OpShiftRightBy,
	ir.OpShiftRightArithmetic: ir.OpShiftRightArithmeticBy,
	ir.OpRotateLeft:           ir.OpRotateLeftBy,
	ir.OpRotateRight:          ir.OpRotateRightBy,
}

// prepareHarden checks that the target can harden the function's control
// flow and rewrites the records that hardening lowers itself. It runs after
// analysis, so validation and its errors are those of an unhardened lowering.
func (e *baselineEngine) prepareHarden() error {
	if !e.options.Harden {
		return nil
	}
	if len(e.blocks) > 1 || slices.Contains(e.labelBlock, 0) || e.blocks[0].term == ir.TermTailCall {
		// The optimized tier's rule: a function with labels, or with the
		// control program a tail call needs, needs a speculation barrier.
		if _, err := hardeningBarrier(newMachineTarget(e.options)); err != nil {
			return err
		}
	}
	for i := range e.recs {
		r := &e.recs[i]
		switch r.op {
		case ir.OpConst:
			if r.fl&rfSymbol == 0 {
				r.op = bOpBlind
			}
		case ir.OpAddScaled:
			r.op = bOpBlindScaled
		case ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight:
			r.x, r.op = uint32(r.op), bOpBlindShift
		}
	}
	return nil
}

// blindTemp returns the scratch register that decodes a constant into dst.
func (e *baselineEngine) blindTemp(dst int8) int8 {
	if e.m.wide && regBank(dst) == bankGP {
		// 386: the engine's scratch registers are XMM registers; EAX is
		// never allocated.
		return x86EAX
	}
	return e.m.transit
}

// blind materializes the w-bit constant x in dst as a random key XOR its
// encoding, using temp, a register of dst's bank.
func (e *baselineEngine) blind(dst, temp int8, w uint8, x uint64) {
	a := &e.alloc
	if w == 0 || w > 64 {
		w = 64
	}
	var b [8]byte
	// crypto/rand.Read does not fail: a broken system source ends the
	// process rather than falling back to unhardened code.
	_, _ = rand.Read(b[:])
	mask := ^uint64(0) >> (64 - w)
	key := binary.LittleEndian.Uint64(b[:]) & mask
	e.put(bPhys{k: pkConst, w: w, a: dst, imm: key})
	e.put(bPhys{k: pkConst, w: w, a: temp, imm: x&mask ^ key})
	xor := bRec{op: ir.OpXor, bits: w, a: noValue, b: noValue, c: noValue}
	if regBank(dst) == bankFP {
		// 386 XMM registers: both halves are zero-extended, so a 64-bit
		// XOR decodes any width.
		xor.bits = 64
	}
	_ = e.arch.lower(e, &xor, dst, [3]int8{dst, temp, noReg})
	a.used |= regBit(dst) | regBit(temp)
	a.flags = true
}

// lowerBlinded lowers a record that prepareHarden rewrote.
func (e *baselineEngine) lowerBlinded(i uint32, r *bRec, k int) error {
	a := &e.alloc
	bank := bankGP
	if r.fl&rfDefFP != 0 {
		bank = bankFP
	}
	operandBank := func(v uint32) uint8 {
		if e.m.wide && e.valueBits(v) == 64 {
			return bankFP
		}
		return bankGP
	}
	var lock uint64
	switch r.op {
	case bOpBlind:
		dst := e.def(i, bank, 0)
		e.blind(dst, e.blindTemp(dst), r.bits, r.imm)
	case bOpBlindShift:
		src := e.use(r.a, operandBank(r.a), &lock)
		count := e.allocReg(bankGP, lock, -1, false)
		lock |= regBit(count)
		bits := uint8(64)
		if e.m.wide {
			bits = 32
		}
		e.blind(count, e.blindTemp(count), bits, r.imm)
		e.advance(r.a, a.nu[k])
		dst := e.def(i, bank, lock)
		shift := bRec{op: baselineShiftBy[r.x], bits: r.bits, a: r.a, b: noValue, c: noValue}
		if err := e.arch.lower(e, &shift, dst, [3]int8{src, count, noReg}); err != nil {
			return err
		}
	case bOpBlindScaled:
		base := e.use(r.a, operandBank(r.a), &lock)
		index := e.use(r.b, operandBank(r.b), &lock)
		scale := e.allocReg(bank, lock, -1, false)
		lock |= regBit(scale)
		e.blind(scale, e.blindTemp(scale), r.bits, r.imm)
		product := bRec{op: ir.OpMul, bits: r.bits, a: noValue, b: noValue, c: noValue}
		if err := e.arch.lower(e, &product, scale, [3]int8{scale, index, noReg}); err != nil {
			return err
		}
		e.advance(r.a, a.nu[k])
		if r.b != r.a {
			e.advance(r.b, a.nu[k+1])
		}
		// The sum may take an operand's register, as any operation's
		// result may; only the product must stay.
		dst := e.def(i, bank, regBit(scale))
		sum := bRec{op: ir.OpAdd, bits: r.bits, a: noValue, b: noValue, c: noValue}
		if err := e.arch.lower(e, &sum, dst, [3]int8{base, scale, noReg}); err != nil {
			return err
		}
	}
	a.flags = true
	a.used |= regBit(e.m.transit) | regBit(e.m.cycle)
	e.defined(i)
	return nil
}

// compareBlinded emits the comparison of predicate leaf n with its nonzero
// immediate: the immediate is decoded into the cycle register first, so the
// left operand may still use transit.
func (e *baselineEngine) compareBlinded(n *bPred, w uint8, cond ir.BranchCondition) {
	e.blind(e.m.cycle, e.m.transit, 64, uint64(n.imm))
	left := e.predicateOperand(n.value, e.m.transit)
	e.put(bPhys{k: pkCmp, sub: uint8(cond), w: w, b: left, c: e.m.cycle})
}

// clampsIndex reports whether block blk ends with a hardened bounds check:
// a comparison that its compared value is below the other operand, whose
// success edge (to) passes that value as its second argument.
func (e *baselineEngine) clampsIndex(blk *bBlock) bool {
	if !e.options.Harden || blk.term != ir.TermBranch || blk.nArgs < 2 {
		return false
	}
	n := &e.preds[blk.pred]
	return n.kind == ir.PredicateCompare && n.cond == ir.BranchBelow && e.args[blk.argStart+1] == n.value
}

// clampIndex clamps the index that block blk's success edge has just moved
// into its second parameter of block t: the index is kept when the bounds
// comparison's flags say it is below the bound, and zeroed otherwise. Edge
// moves leave the flags as the comparison set them on every target with a
// barrier.
func (e *baselineEngine) clampIndex(t int32, blk *bBlock) {
	a := &e.alloc
	tb := &e.blocks[t]
	loc := int16(-1)
	for _, en := range a.states[tb.stateStart : tb.stateStart+tb.stateN] {
		if en.kind == eParam && en.id == 1 {
			loc = en.loc
		}
	}
	if loc < 0 {
		return
	}
	w := e.valueBits(e.args[blk.argStart+1])
	slot := loc >= bSlotLoc
	if e.m.wide {
		e.clampWide(loc, w)
		return
	}
	work, zero := int8(loc), e.m.cycle
	if slot || regBank(work) == bankFP {
		work = e.m.transit
	}
	switch {
	case slot:
		e.put(bPhys{k: pkReload, w: w, a: work, imm: uint64(loc - bSlotLoc)})
	case work != int8(loc):
		e.put(bPhys{k: pkMove, w: w, a: work, b: int8(loc)})
	}
	e.put(bPhys{k: pkConst, w: 64, a: zero})
	e.put(bPhys{k: pkClamp, a: work, b: zero})
	switch {
	case slot:
		e.put(bPhys{k: pkSpill, w: w, a: work, imm: uint64(loc - bSlotLoc)})
	case work != int8(loc):
		e.put(bPhys{k: pkMove, w: w, a: int8(loc), b: work})
	}
	a.used |= regBit(work) | regBit(zero)
	if !slot {
		a.used |= regBit(int8(loc))
	}
}

// clampWide is clampIndex on 386, whose model keeps 64-bit values in XMM
// registers: CMOV works on EAX (and EDX for a 64-bit index), with zero in ECX.
// MOVD, PSHUFD and the moves between the pair and XMM keep the flags.
func (e *baselineEngine) clampWide(loc int16, w uint8) {
	a := &e.alloc
	clamp := func(r int8) {
		e.put(bPhys{k: pkConst, w: 32, a: x86ECX})
		e.put(bPhys{k: pkClamp, a: r, b: x86ECX})
	}
	a.used |= i386Temps
	switch {
	case loc < bSlotLoc && regBank(int8(loc)) == bankGP:
		clamp(int8(loc))
		a.used |= regBit(int8(loc))
	case w != 64 && loc >= bSlotLoc:
		slot := uint64(loc - bSlotLoc)
		e.put(bPhys{k: pkReload, w: w, a: x86EAX, imm: slot})
		clamp(x86EAX)
		e.put(bPhys{k: pkSpill, w: w, a: x86EAX, imm: slot})
	case w != 64:
		e.put(bPhys{k: pkMove, w: w, a: x86EAX, b: int8(loc)})
		clamp(x86EAX)
		e.put(bPhys{k: pkMove, w: w, a: int8(loc), b: x86EAX})
		a.used |= regBit(int8(loc))
	default:
		x := int8(loc)
		if loc >= bSlotLoc {
			x = e.m.transit
			e.put(bPhys{k: pkReload, w: 64, a: x, imm: uint64(loc - bSlotLoc)})
		}
		e.put(bPhys{k: pkOp, sub: i386XToPair, b: x})
		clamp(x86EAX)
		e.put(bPhys{k: pkClamp, a: x86EDX, b: x86ECX})
		e.put(bPhys{k: pkOp, sub: i386PairToX, a: x})
		if loc >= bSlotLoc {
			e.put(bPhys{k: pkSpill, w: 64, a: x, imm: uint64(loc - bSlotLoc)})
		}
		a.used |= regBit(x)
	}
}

// insertBarriers returns ps with a speculation barrier after every label
// but the epilogue's, which only returning blocks jump to.
func (e *baselineEngine) insertBarriers(ps []bPhys, epilogue int32) []bPhys {
	n := 0
	for i := range ps {
		if ps[i].k == pkLabel && ps[i].imm != uint64(epilogue) && !e.tailEpilogue(ps[i].imm) {
			n++
		}
	}
	if n == 0 {
		return ps
	}
	barrier := bPhys{k: pkBarrier}
	barrier.sz = uint8(e.arch.size(e, &barrier))
	out := make([]bPhys, 0, len(ps)+n)
	for i := range ps {
		out = append(out, ps[i])
		if ps[i].k == pkLabel && ps[i].imm != uint64(epilogue) && !e.tailEpilogue(ps[i].imm) {
			out = append(out, barrier)
		}
	}
	return out
}

// x86EncodeHarden encodes a barrier or clamp record through an amd64 or 386
// byte or text assembler; rex selects the 64-bit CMOVAE.
func x86EncodeHarden(a any, p *bPhys, rex bool) error {
	if p.k == pkBarrier {
		return a.(interface{ Barrier(machine.Barrier) error }).Barrier(machine.BarrierSpeculation)
	}
	// CMOVAE a, b: the optimized tier's clamp, which no assembler method
	// encodes.
	code := []byte{0x0f, 0x43, 0xc0 | byte(p.a&7)<<3 | byte(p.b&7)}
	if rex {
		code = append([]byte{0x48 | byte(p.a>>3)<<2 | byte(p.b>>3)}, code...)
	}
	return a.(interface {
		Literal(machine.Instruction) error
	}).Literal(machine.Instruction{Op: machine.OpLiteralData, Data: code})
}

// arm64EncodeHarden encodes a barrier (DSB SY; ISB) or clamp (CSEL ... LO).
func arm64EncodeHarden(a any, p *bPhys) error {
	if p.k == pkBarrier {
		return a.(interface{ Barrier(machine.Barrier) error }).Barrier(machine.BarrierSpeculation)
	}
	a.(interface {
		SelectFlags(dst, ifTrue, ifFalse backend.Register, condition op.BranchCondition)
	}).SelectFlags(breg(p.a), breg(p.a), breg(p.b), op.BranchBelow)
	return nil
}

// ppc64EncodeHarden encodes a barrier (ori 31,31,0) or clamp (ISEL on CR0's
// LT bit, which an unsigned comparison sets for below).
func ppc64EncodeHarden(o *ppcOut, p *bPhys) {
	if p.k == pkBarrier {
		o.emit(ppcI{Op: targetppc64.SpeculationBarrier})
		return
	}
	o.emit(ppcI{Op: targetppc64.Isel, A: gr(p.a), B: gr(p.a), C: gr(p.b), BI: 0})
}
