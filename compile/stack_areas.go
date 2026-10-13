package compile

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/ir"
)

// Stack areas (ir.StackArea) are reserved spill slots at the bottom of the
// slot space: the frame on a platform stack, the workspace otherwise. Both
// tiers place area i at the slots after those of areas 0 to i-1, zero them
// all when the function is entered, and address them with
// machine.OpSlotAddress. Slots are word-aligned; a 16-byte area takes one
// more word and rounds its address up at run time, because a Go-ABI stack
// is only 8-byte aligned on most targets.

// stackSlotBytes is the size of one spill slot on architecture.
func stackSlotBytes(architecture string) int {
	if architecture == "386" {
		return 4
	}
	return 8
}

// stackAreaSlots returns the slots area takes with slotBytes-wide slots,
// and whether its address is rounded up to 16 bytes.
func stackAreaSlots(area ir.StackArea, slotBytes int) (int, bool) {
	words := (area.Bytes + slotBytes - 1) / slotBytes
	if area.Align > slotBytes {
		return words + 1, true
	}
	return words, false
}

// stackAreaWords returns the slots all of areas take.
func stackAreaWords(areas []ir.StackArea, slotBytes int) int {
	total := 0
	for _, area := range areas {
		words, _ := stackAreaSlots(area, slotBytes)
		total += words
	}
	return total
}

// checkStackAreas applies the target rules for stack areas that Lower
// checks in both tiers: alignment, address width, the slot budget and the
// Go-managed restriction on calls.
func checkStackAreas(areas []ir.StackArea, code []ir.Instruction, options compileOptions) error {
	if len(areas) == 0 {
		return nil
	}
	arch := targetArchitecture(options)
	slotBytes := stackSlotBytes(arch)
	maxAlign := 16
	if arch == "386" {
		// ABI0 and cdecl align the 386 stack to 4 bytes only.
		maxAlign = 4
	}
	for i, area := range areas {
		if area.Align > maxAlign {
			return fmt.Errorf("stack area %d: alignment %d exceeds %d on %s: %w", i, area.Align, maxAlign, arch, ErrBackendLimit)
		}
	}
	if words := stackAreaWords(areas, slotBytes); words > maxSpillSlots {
		return &LimitError{Kind: LimitSpillWords, Needed: words, Limit: maxSpillSlots, Arch: arch, Detail: "stack areas"}
	}
	for id := range code {
		ins := &code[id]
		switch ins.Op {
		case ir.OpStackAddress:
			if int(instructionBits(*ins)) != 8*slotBytes {
				return fmt.Errorf("stack address at value %d has %d bits; %s addresses have %d: %w", id, instructionBits(*ins), arch, 8*slotBytes, ErrBackendLimit)
			}
		case ir.OpCallC, ir.OpCallGo, ir.OpSyscall:
			if options.ManagedCalls {
				// A Go callee, or the C gateway, may move the goroutine
				// stack; the frame has no pointer map to fix area addresses.
				return fmt.Errorf("Go-managed function with stack areas calls at value %d: %w", id, ErrUnsupported)
			}
		}
	}
	return nil
}

// stackLegalization is the slot layout the legalizer gives a program's
// stack areas: area i starts at slots[i], and align[i] says its address is
// rounded up to 16 bytes.
type stackLegalization struct {
	slots []int
	align []bool
	words int
	// zeroAt is the first instruction after the entry parameters, where
	// the zeroing code goes.
	zeroAt int
}

// prepareStack lays out the source's stack areas. The legalized program has
// one word-sized area per slot, so OpStackAddress names its first slot.
func (l *vectorLegalizer) prepareStack() {
	areas := l.source.StackAreas
	slotBytes := int(l.bits / 8)
	s := &stackLegalization{slots: make([]int, len(areas)), align: make([]bool, len(areas))}
	for i, area := range areas {
		words, align := stackAreaSlots(area, slotBytes)
		s.slots[i], s.align[i] = s.words, align
		s.words += words
	}
	for s.zeroAt < len(l.source.Instructions) && l.source.Instructions[s.zeroAt].Op == ir.OpParam {
		s.zeroAt++
	}
	l.out.StackAreas = make([]ir.StackArea, s.words)
	for i := range l.out.StackAreas {
		l.out.StackAreas[i] = ir.StackArea{Bytes: slotBytes, Align: slotBytes}
	}
	l.stack = s
}

// zeroStack stores zero to every stack slot. Each base address covers 256
// slots, so every store's displacement folds on every target. The last
// slot gets an address of its own: the optimized tier sizes the frame from
// the highest slot an instruction names.
func (l *vectorLegalizer) zeroStack() {
	s := l.stack
	slotBytes := int(l.bits / 8)
	zero := l.constant(l.bits, 0)
	for first := 0; first < s.words; first += 256 {
		base := l.emit(ir.Instruction{Op: ir.OpStackAddress, Bits: l.bits, Imm: uint64(first)})
		for k := first; k < min(first+256, s.words); k++ {
			address := base
			if k == s.words-1 && k != first {
				address = l.emit(ir.Instruction{Op: ir.OpStackAddress, Bits: l.bits, Imm: uint64(k)})
			} else if k != first {
				address = l.binary(ir.OpAdd, l.bits, base, l.constant(l.bits, uint64((k-first)*slotBytes)))
			}
			if l.bits == 64 {
				l.emit(ir.Instruction{Op: ir.OpStore64, A: address, B: zero})
			} else {
				l.emit(ir.Instruction{Op: ir.OpStore32, Bits: 32, A: address, B: zero, Aligned: true})
			}
		}
	}
}

// stackAddress legalizes OpStackAddress id of area ins.Imm.
func (l *vectorLegalizer) stackAddress(id int, ins ir.Instruction) {
	s := l.stack
	area := int(ins.Imm)
	result := l.emit(ir.Instruction{Op: ir.OpStackAddress, Bits: ins.Bits, Imm: uint64(s.slots[area])})
	if s.align[area] {
		// Slots are 8-byte aligned: adding the address's bit 3 rounds
		// it up to 16 within the area's extra word.
		bits := selectedInstructionBits(ins)
		low := l.binary(ir.OpAnd, bits, result, l.constant(bits, 8))
		result = l.binary(ir.OpAdd, bits, result, low)
	}
	l.values[id] = legalizedValue{lanes: l.lane(result)}
}
