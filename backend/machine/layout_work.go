package machine

// Each internal stream owns its temporary layout buffers. Final compiler
// output is copied into owned located instructions before this work is reused.
type layoutBuffers struct {
	history                                                       struct{ located, old, kept, relax, positions, labelIndex, oldToNew, newPositions, sizes, pending, remove, referenced, reachable, threadNext, threadDest, threadPath, threadState, threadCyclic BufferHistory }
	located                                                       []LocatedInstruction
	old, kept, relax                                              []Instruction
	positions, labelIndex, oldToNew, newPositions, sizes, pending []int
	remove, referenced, reachable                                 []bool
	threadNext, threadDest, threadPath                            []int
	threadState                                                   []uint8
	threadCyclic                                                  []bool
}

func (s *Stream) workSizes() [21]int {
	w := &s.layout
	return [21]int{workBytes(s.instructions), workBytes(s.sizes), workBytes(s.marks), workBytes(w.located), workBytes(w.old), workBytes(w.kept), workBytes(w.relax), workBytes(w.positions), workBytes(w.labelIndex), workBytes(w.oldToNew), workBytes(w.newPositions), workBytes(w.sizes), workBytes(w.pending), workBytes(w.remove), workBytes(w.referenced), workBytes(w.reachable), workBytes(w.threadNext), workBytes(w.threadDest), workBytes(w.threadPath), workBytes(w.threadState), workBytes(w.threadCyclic)}
}
func (s *Stream) releaseWork(poison bool) {
	s.instructionHistory.Observe(len(s.instructions))
	s.sizeHistory.Observe(len(s.sizes))
	s.markHistory.Observe(len(s.marks))
	if poison {
		bad := Instruction{Op: 255, Imm: ^uint64(0), Symbol: "scratch poison"}
		for _, buf := range [][]Instruction{s.instructions, s.layout.old, s.layout.kept, s.layout.relax} {
			buf = buf[:cap(buf)]
			for i := range buf {
				buf[i] = bad
			}
		}
		for i := range s.layout.located[:cap(s.layout.located)] {
			s.layout.located[:cap(s.layout.located)][i] = LocatedInstruction{Instruction: bad, PC: -0x5a5a}
		}
		for _, buf := range [][]int{s.sizes, s.layout.positions, s.layout.labelIndex, s.layout.oldToNew, s.layout.newPositions, s.layout.sizes, s.layout.pending, s.layout.threadNext, s.layout.threadDest, s.layout.threadPath} {
			buf = buf[:cap(buf)]
			for i := range buf {
				buf[i] = -0x5a5a
			}
		}
		for _, buf := range [][]bool{s.layout.remove, s.layout.referenced, s.layout.reachable, s.layout.threadCyclic} {
			for i := range buf[:cap(buf)] {
				buf[:cap(buf)][i] = true
			}
		}
		for i := range s.layout.threadState[:cap(s.layout.threadState)] {
			s.layout.threadState[:cap(s.layout.threadState)][i] = 255
		}
		for i := range s.marks[:cap(s.marks)] {
			s.marks[:cap(s.marks)][i] = 255
		}
	} else {
		clear(s.instructions)
		clear(s.layout.old)
		clear(s.layout.kept)
		clear(s.layout.relax)
		clear(s.layout.located)
	}
	s.instructions = s.instructions[:0]
	s.layout.old = s.layout.old[:0]
	s.layout.kept = s.layout.kept[:0]
	s.layout.relax = s.layout.relax[:0]
	s.layout.located = s.layout.located[:0]
	// Callbacks and flag facts can retain caller storage; they are not scratch.
	s.size = nil
	s.flagEffect = nil
	s.flagFacts = nil
	s.flags = FlagState{}
	s.previousZero = FlagFact{}
	s.aliasArena = nil
	s.lastOp = 0
	s.lastImm = 0
	if poison {
		s.instructions = poisonReplacement(s.instructions)
		s.sizes = poisonReplacement(s.sizes)
		s.marks = poisonReplacement(s.marks)
		s.layout.located = poisonReplacement(s.layout.located)
		s.layout.old = poisonReplacement(s.layout.old)
		s.layout.kept = poisonReplacement(s.layout.kept)
		s.layout.relax = poisonReplacement(s.layout.relax)
		s.layout.positions = poisonReplacement(s.layout.positions)
		s.layout.labelIndex = poisonReplacement(s.layout.labelIndex)
		s.layout.oldToNew = poisonReplacement(s.layout.oldToNew)
		s.layout.newPositions = poisonReplacement(s.layout.newPositions)
		s.layout.sizes = poisonReplacement(s.layout.sizes)
		s.layout.pending = poisonReplacement(s.layout.pending)
		s.layout.remove = poisonReplacement(s.layout.remove)
		s.layout.referenced = poisonReplacement(s.layout.referenced)
		s.layout.reachable = poisonReplacement(s.layout.reachable)
		s.layout.threadNext = poisonReplacement(s.layout.threadNext)
		s.layout.threadDest = poisonReplacement(s.layout.threadDest)
		s.layout.threadPath = poisonReplacement(s.layout.threadPath)
		s.layout.threadState = poisonReplacement(s.layout.threadState)
		s.layout.threadCyclic = poisonReplacement(s.layout.threadCyclic)
	}
}
