package i386

import (
	"sort"
	"sync"

	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// ElideDeadFlags implements machine.FlagElider. When no path reads the
// flags an instruction writes before they are written again, it selects
// the instruction's shorter PreserveFlags form: an addition, increment or
// decrement whose destination differs from its first operand needs a MOV
// before the two-address form, and a single LEA computes it without writing
// flags; an 8- or 16-bit result's AND mask becomes MOVZX, and a narrowing,
// which is that AND, a MOVZX. Labels move with the shorter code.
func (Policy) ElideDeadFlags(s *machine.Stream, labels []int, bound []bool) ([]int, error) {
	if s == nil || len(labels) != len(bound) {
		return nil, machine.ErrInvalidBranch
	}
	w := elisions.Get().(*elision)
	defer w.release()
	code := s.Instructions()
	// Rewrites wait until every candidate is decided, so the analysis
	// sees only the original instructions. Each candidate keeps its form.
	// Nearly every instruction writes flags, so the straight line after a
	// candidate usually settles it; only a jump first needs the whole graph.
	for n := range code {
		if !mayShorten(&code[n], s.SizeAt(n)) {
			continue
		}
		form, ok := shorterWithoutFlags(&code[n], s.SizeAt(n))
		if !ok {
			continue
		}
		switch flagsAfter(s, code, n) {
		case flagsDead:
			w.selected, w.forms = append(w.selected, n), append(w.forms, form)
		case flagsUnknown:
			w.undecided, w.undecidedForms = append(w.undecided, n), append(w.undecidedForms, form)
		}
	}
	if len(w.selected) == 0 && len(w.undecided) == 0 {
		return labels, nil
	}
	var positions []int
	if len(labels) != 0 {
		positions = resized(&w.positions, len(code)+1)
		for n := range code {
			positions[n+1] = positions[n] + s.SizeAt(n)
		}
	}
	// A label names the first instruction at its PC; instructions of zero
	// size before it neither read nor write flags.
	labelIndex := resized(&w.labelIndex, len(labels))
	for l, pc := range labels {
		if !bound[l] {
			continue
		}
		n := sort.SearchInts(positions, pc)
		if n > len(code) || positions[n] != pc {
			return nil, machine.ErrInvalidBranch
		}
		labelIndex[l] = n
	}
	if len(w.undecided) != 0 {
		live, err := w.liveFlags(s, code, labels, bound, labelIndex)
		if err != nil {
			return nil, err
		}
		for k, n := range w.undecided {
			// A candidate is not a branch, so its only successor is n+1.
			if live[n+1]&candidateFlags(s, code, n) == 0 {
				w.selected, w.forms = append(w.selected, n), append(w.forms, w.undecidedForms[k])
			}
		}
	}
	if len(w.selected) == 0 {
		return labels, nil
	}
	// The facts described values the rewritten additions no longer produce
	// flags for. Forgetting them leaves what Replace would, except that the
	// modified mask may still name flags a final LEA keeps, which only makes
	// later users more careful.
	s.ForgetFlags()
	for k, n := range w.selected {
		s.ReplaceAt(n, w.forms[k])
	}
	if len(labels) == 0 {
		return labels, nil
	}
	for n := range code {
		positions[n+1] = positions[n] + s.SizeAt(n)
	}
	moved := make([]int, len(labels))
	for l, pc := range labels {
		moved[l] = pc
		if bound[l] {
			moved[l] = positions[labelIndex[l]]
		}
	}
	return moved, nil
}

// elision holds the buffers of one ElideDeadFlags call, which elisions
// keeps between calls: the analysis of a function with branches needs
// several arrays as long as its code.
type elision struct {
	selected, undecided   []int
	forms, undecidedForms []machine.Instruction // the PreserveFlags forms of selected and undecided
	positions, labelIndex []int
	first, branchesTo     []int
	fill, work            []int
	live                  []machine.FlagMask
	queued                []bool
}

var elisions = sync.Pool{New: func() any { return new(elision) }}

// release empties w, dropping the forms' references, and returns it to
// elisions.
func (w *elision) release() {
	clear(w.forms)
	clear(w.undecidedForms)
	w.selected, w.undecided = w.selected[:0], w.undecided[:0]
	w.forms, w.undecidedForms = w.forms[:0], w.undecidedForms[:0]
	elisions.Put(w)
}

// resized returns *buffer resized to n zero elements, reusing its array
// when it is large enough.
func resized[T any](buffer *[]T, n int) []T {
	if cap(*buffer) < n {
		*buffer = make([]T, n)
	} else {
		*buffer = (*buffer)[:n]
		clear(*buffer)
	}
	return *buffer
}

const (
	flagsLive = iota
	flagsDead
	flagsUnknown
)

// straightLineLimit bounds how far flagsAfter looks, so candidates whose
// flags outlive a long run of instructions that keep some of them, such as
// INCs after an ADD, cost no more than the graph.
const straightLineLimit = 64

// flagOverflow stands for OF beside the stream's sign, zero and carry, which
// the stream does not track: JO, JNO and the signed conditions read it.
// ModifiedFlagsAt never reports this bit.
const flagOverflow machine.FlagMask = 1 << 7

// flagsAfter follows the straight line after instruction n until the flags
// it writes are read, written again or leave the function, or until a branch
// may take them elsewhere. Kills are those of the original instructions:
// a candidate turned into LEA had dead flags, so it hid no read.
func flagsAfter(s *machine.Stream, code []machine.Instruction, n int) int {
	needed := candidateFlags(s, code, n)
	for k := range code[n+1:] {
		if k == straightLineLimit {
			return flagsUnknown
		}
		i := &code[n+1+k]
		if flagReads(i)&needed != 0 {
			return flagsLive
		}
		if i.Op == machine.OpReturn || i.Op == machine.OpTailCall {
			return flagsDead
		}
		if needed &^= killedFlags(s, code, n+1+k); needed == 0 {
			return flagsDead
		}
		if isBranch(i.Op) {
			return flagsUnknown
		}
	}
	return flagsDead
}

// liveFlags returns, for each instruction, the flags read on some path from
// it before they are written again. A worklist revisits an instruction only
// when the flags live after it grow, so the work is linear in the code and
// its branches even when liveness crosses many back edges.
func (w *elision) liveFlags(s *machine.Stream, code []machine.Instruction, labels []int, bound []bool, labelIndex []int) ([]machine.FlagMask, error) {
	// branchesTo[first[n]:first[n+1]] are the branches that target
	// instruction n.
	first := resized(&w.first, len(code)+2)
	for n := range code {
		i := &code[n]
		if !isBranch(i.Op) {
			continue
		}
		if i.Imm >= uint64(len(labels)) || !bound[i.Imm] {
			return nil, machine.ErrInvalidBranch
		}
		first[labelIndex[i.Imm]+1]++
	}
	for n := range len(code) + 1 {
		first[n+1] += first[n]
	}
	branchesTo := resized(&w.branchesTo, first[len(code)+1])
	fill := resized(&w.fill, len(first))
	copy(fill, first)
	for n := range code {
		if i := &code[n]; isBranch(i.Op) {
			target := labelIndex[i.Imm]
			branchesTo[fill[target]] = n
			fill[target]++
		}
	}
	live := resized(&w.live, len(code)+1)
	queued := resized(&w.queued, len(code))
	// The last instruction is visited first, as a backward pass would.
	work := resized(&w.work, len(code))
	for n := range code {
		work[n], queued[n] = n, true
	}
	for len(work) != 0 {
		n := work[len(work)-1]
		work, queued[n] = work[:len(work)-1], false
		i := &code[n]
		after := live[n+1]
		switch {
		case i.Op == machine.OpReturn || i.Op == machine.OpTailCall, i.Op == machine.OpJumpTable:
			// A jump table compares its index itself.
			after = 0
		case isBranch(i.Op):
			if i.Op == machine.OpJump {
				after = 0
			}
			after |= live[labelIndex[i.Imm]]
		}
		needed := flagReads(i) | after&^killedFlags(s, code, n)
		if needed == live[n] {
			continue
		}
		live[n] = needed
		if n > 0 && code[n-1].Op != machine.OpReturn && code[n-1].Op != machine.OpJump && code[n-1].Op != machine.OpTailCall && !queued[n-1] {
			work, queued[n-1] = append(work, n-1), true
		}
		for _, from := range branchesTo[first[n]:first[n+1]] {
			if !queued[from] {
				work, queued[from] = append(work, from), true
			}
		}
	}
	w.work = work[:0]
	return live, nil
}

// killedFlags returns the flags instruction n certainly overwrites: those
// both the stream and FlagFacts report, which need not trust the stream's
// options. Every form that writes both sign and zero (all but CMPXCHG8B,
// which writes ZF only, and ROL or ROR, which write CF and OF) writes OF
// too.
func killedFlags(s *machine.Stream, code []machine.Instruction, n int) machine.FlagMask {
	killed := s.ModifiedFlagsAt(n)
	if killed == 0 {
		return 0
	}
	killed &= FlagFacts(code[n]).Modified
	if killed&(machine.FlagSign|machine.FlagZero) == machine.FlagSign|machine.FlagZero {
		killed |= flagOverflow
	}
	return killed
}

// candidateFlags returns every flag candidate n may write: ADD, SUB, INC
// and DEC all write OF.
func candidateFlags(s *machine.Stream, code []machine.Instruction, n int) machine.FlagMask {
	return s.ModifiedFlagsAt(n) | FlagFacts(code[n]).Modified | flagOverflow
}

// mayShorten rejects, without copying i or computing a form, what
// shorterWithoutFlags certainly rejects: an instruction that has no
// PreserveFlags form, or a 32-bit register addition no longer than the
// shortest LEA [base + index] (three bytes).
func mayShorten(i *machine.Instruction, size int) bool {
	if i.PreserveFlags {
		return false
	}
	switch i.Op {
	case machine.OpBinary:
		if i.Bits == 8 || i.Bits == 16 {
			return extends(i.A, i.Bits)
		}
		return i.Arithmetic == op.OpAdd && size > 3
	case machine.OpBinaryImmediate:
		switch i.Arithmetic {
		case op.OpAdd, op.OpSub:
			return true
		case op.OpAnd:
			return uint32(i.Imm) == 0xff || uint32(i.Imm) == 0xffff
		}
	case machine.OpShift:
		return (i.Bits == 8 || i.Bits == 16) && i.Imm <= 255 && i.Arithmetic != op.OpShiftRightArithmetic && extends(i.A, i.Bits)
	case machine.OpInc, machine.OpDec:
		return i.A != i.B
	case machine.OpNarrow:
		return i.Imm == 8 || i.Imm == 16
	}
	return false
}

// shorterWithoutFlags returns the PreserveFlags form of *in, which takes size
// bytes, when it is shorter. A narrowing is AND with its mask, so it becomes
// that AND's form.
func shorterWithoutFlags(in *machine.Instruction, size int) (machine.Instruction, bool) {
	i := *in
	switch i.Op {
	case machine.OpBinary, machine.OpBinaryImmediate, machine.OpShift:
	case machine.OpInc, machine.OpDec:
		if i.A == i.B {
			return i, false // INC and DEC are shorter than LEA
		}
	case machine.OpNarrow:
		if i.Imm != 8 && i.Imm != 16 {
			return i, false
		}
		i = machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAnd, Bits: 32, A: i.A, B: i.B, Imm: 1<<i.Imm - 1}
	default:
		return i, false
	}
	if i.PreserveFlags {
		return i, false
	}
	i.PreserveFlags = true
	f, ok := preservedFor(i)
	return i, ok && f.size() < size
}

// flagReads reports the flags an instruction reads. Conditions, which
// include JO, JNO and the signed ones, read every flag; so may the opaque
// bytes of OpLiteralData. The high word of a pair's ADD or SUB (ADC or SBB)
// and a carry taken from the flags read CF.
func flagReads(i *machine.Instruction) machine.FlagMask {
	switch i.Op {
	case machine.OpJumpIf, machine.OpSelectFlags, machine.OpLiteralData:
		return machine.FlagSign | machine.FlagZero | machine.FlagCarry | flagOverflow
	case machine.OpBinaryCarry:
		return machine.FlagCarry
	case machine.OpWordCarry, machine.OpCarryBit:
		if i.CarryMode == machine.CarryFlags {
			return machine.FlagCarry
		}
	}
	return 0
}

func isBranch(op machine.Opcode) bool {
	switch op {
	case machine.OpJump, machine.OpJumpIf, machine.OpBranchZero, machine.OpBranchBit, machine.OpLoop:
		return true
	default:
		return false
	}
}
