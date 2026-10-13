package compile

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/ir"
)

// baselineStack lays out a function's stack areas in the baseline engine.
// Engine slots are 8 bytes on every target, so area i takes the engine
// slots from base[i], at the bottom of the frame or workspace; a 16-byte
// aligned area takes one slot more and rounds its address up at run time.
// The prologue zeroes every area slot once the frame is final.
type baselineStack struct {
	areas []ir.StackArea
	// block is one more than the block whose segment declared areas last,
	// so a segment the engine records again does not declare them twice.
	block int
	base  []int32
	align []bool
	slots int32
}

func (s *baselineStack) reset() {
	s.areas, s.base, s.align = s.areas[:0], s.base[:0], s.align[:0]
	s.block, s.slots = 0, 0
}

// declare adds the areas of the segment that starts block block.
func (s *baselineStack) declare(block int, areas []ir.StackArea) {
	if s.block == block+1 {
		return
	}
	s.block = block + 1
	for _, area := range areas {
		words, align := stackAreaSlots(area, 8)
		s.areas = append(s.areas, area)
		s.base = append(s.base, s.slots)
		s.align = append(s.align, align)
		s.slots += int32(words)
	}
}

// address checks OpStackAddress id, which ins is.
func (s *baselineStack) address(e *baselineEngine, id int, ins *ir.Instruction) error {
	if ins.Imm >= uint64(len(s.areas)) {
		return baselineInvalid(id, ins.Op, "stack area %d is not declared", ins.Imm)
	}
	if bits := int(instructionBits(*ins)); bits != 8*stackSlotBytes(e.target.architecture) {
		return fmt.Errorf("stack address at value %d has %d bits on %s: %w", id, bits, e.target.architecture, ErrBackendLimit)
	}
	return nil
}

// check applies Lower's stack area rules once the function is recorded.
func (s *baselineStack) check(e *baselineEngine) error {
	if len(s.areas) == 0 {
		return nil
	}
	if err := checkStackAreas(s.areas, nil, e.options); err != nil {
		return err
	}
	if e.options.ManagedCalls && (e.hasCall || e.hasSyscall) {
		return fmt.Errorf("Go-managed function with stack areas makes calls: %w", ErrUnsupported)
	}
	return nil
}

// zeroRecords is the number of prologue records that zero the areas.
func (s *baselineStack) zeroRecords() int {
	if s.slots == 0 {
		return 0
	}
	return 1 + int(s.slots)
}

// zero appends the records that zero every area slot through the transit
// register, which holds no entry argument on any target.
func (s *baselineStack) zero(e *baselineEngine, add func(bPhys)) {
	if s.slots == 0 {
		return
	}
	add(bPhys{k: pkConst, a: e.m.transit, w: 64})
	for slot := range s.slots {
		add(bPhys{k: pkSpill, a: e.m.transit, w: 64, imm: uint64(slot)})
	}
}

// lowerStackAddress lowers OpStackAddress record i: the address of its
// area's first slot, rounded up to 16 bytes through the transit register
// for a 16-byte area (slots are 8-byte aligned, so adding the address's
// bit 3 does it).
func (e *baselineEngine) lowerStackAddress(i uint32, r *bRec) {
	a := &e.alloc
	dst := e.def(i, bankGP, 0)
	e.put(bPhys{k: pkSlotAddress, a: dst, w: r.bits, imm: uint64(e.stack.base[r.imm])})
	if e.stack.align[r.imm] {
		t := e.m.transit
		e.put(bPhys{k: pkConst, a: t, imm: 8})
		e.put(bPhys{k: pkOp, sub: uint8(ir.OpAnd), w: 64, a: t, b: t, c: dst})
		e.put(bPhys{k: pkOp, sub: uint8(ir.OpAdd), w: 64, a: dst, b: dst, c: t})
		a.flags = true
		a.used |= regBit(t)
	}
	e.defined(i)
}
