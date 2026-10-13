package machine

import "sort"

// Layout maps a stream's instruction indices to byte PCs. It is an immutable
// snapshot: later stream changes do not affect it, and it is safe for
// concurrent use. Profilers, debuggers and crash symbolization use it to find
// the instruction at a PC. The zero Layout describes an empty stream.
type Layout struct {
	pcs     []int // each instruction's start, then the end of the code
	invalid bool  // a negative instruction size broke PC order
}

// Layout returns the byte layout of the stream's current instructions. It
// costs O(instructions) and allocates once.
func (s *Stream) Layout() Layout {
	pcs := make([]int, len(s.sizes)+1)
	invalid := false
	for n, size := range s.sizes {
		invalid = invalid || size < 0
		pcs[n+1] = pcs[n] + size
	}
	return Layout{pcs: pcs, invalid: invalid}
}

// Len returns the number of instructions.
func (l Layout) Len() int { return max(len(l.pcs)-1, 0) }

// PC returns the byte position of the instruction at index. PC(Len()) is the
// end of the code. It panics when index is outside [0, Len()].
func (l Layout) PC(index int) int {
	if l.pcs == nil && index == 0 {
		return 0
	}
	return l.pcs[index]
}

// InstructionAt returns the first instruction that starts at pc. Zero-size
// instructions, such as labels, share their PC with the next instruction, so
// the first of them is returned; pc at the end of the code returns Len(). ok
// is false when pc is not an instruction boundary, or when the stream holds an
// instruction its target cannot size. It costs O(log instructions).
func (l Layout) InstructionAt(pc int) (index int, ok bool) {
	if l.invalid {
		return 0, false
	}
	if l.pcs == nil {
		return 0, pc == 0
	}
	return instructionAt(l.pcs, pc)
}

// instructionAt searches ascending instruction PCs that end with the code's
// end. Layout passes wrap their work buffers with it without copying.
func instructionAt(pcs []int, pc int) (int, bool) {
	if len(pcs) == 0 {
		return 0, false
	}
	if pc == pcs[len(pcs)-1] {
		return len(pcs) - 1, true
	}
	index := sort.SearchInts(pcs, pc)
	return index, index < len(pcs) && pcs[index] == pc
}
