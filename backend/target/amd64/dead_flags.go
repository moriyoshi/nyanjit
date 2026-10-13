package amd64

import (
	"sort"
	"sync"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// ElideDeadFlags implements machine.FlagElider. An addition, increment or
// decrement whose destination differs from its first operand needs a MOV
// before the two-address form; when no path reads the flags it writes
// before they are written again, a single LEA computes it without writing
// flags. Labels move with the shorter code.
func (Policy) ElideDeadFlags(s *machine.Stream, labels []int, bound []bool) ([]int, error) {
	if s == nil || len(labels) != len(bound) {
		return nil, machine.ErrInvalidBranch
	}
	code := s.Instructions()
	// Rewrites wait until every candidate is decided, so the analysis
	// sees only the original instructions.
	var selected []int
	// Nearly every instruction writes flags, so the straight line after a
	// candidate usually settles it; only a jump first needs the whole graph.
	var undecided []int
	for n, i := range code {
		if !shorterWithoutFlags(s, i, s.SizeAt(n)) {
			continue
		}
		switch flagsAfter(s, code, n) {
		case flagsDead:
			selected = append(selected, n)
		case flagsUnknown:
			undecided = append(undecided, n)
		}
	}
	if selected == nil && undecided == nil {
		return labels, nil
	}
	var positions []int
	if len(labels) != 0 {
		positions = make([]int, len(code)+1)
		for n := range code {
			positions[n+1] = positions[n] + s.SizeAt(n)
		}
	}
	// A label names the first instruction at its PC; instructions of zero
	// size before it neither read nor write flags.
	labelIndex := make([]int, len(labels))
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
	if undecided != nil {
		live, err := liveFlags(s, code, labels, bound, labelIndex)
		if err != nil {
			return nil, err
		}
		for _, n := range undecided {
			// A candidate is not a branch, so its only successor is n+1.
			if live[n+1]&candidateFlags(s, code, n) == 0 {
				selected = append(selected, n)
			}
		}
	}
	if selected == nil {
		return labels, nil
	}
	// The facts described values the rewritten additions no longer produce
	// flags for. Forgetting them leaves what Replace would, except that the
	// modified mask may still name flags a final LEA keeps, which only makes
	// later users more careful.
	s.ForgetFlags()
	for _, n := range selected {
		i := code[n]
		i.PreserveFlags = true
		s.ReplaceAt(n, i)
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

const (
	flagsLive = iota
	flagsDead
	flagsUnknown
)

// flagsAfter follows the straight line after instruction n until the flags
// it writes are read, written again or leave the function, or until a branch
// may take them elsewhere. Kills are those of the original instructions:
// a candidate turned into LEA had dead flags, so it hid no read.
func flagsAfter(s *machine.Stream, code []machine.Instruction, n int) int {
	needed := candidateFlags(s, code, n)
	for k, i := range code[n+1:] {
		if flagReads(i)&needed != 0 {
			return flagsLive
		}
		if i.Op == machine.OpReturn {
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
// it before they are written again.
func liveFlags(s *machine.Stream, code []machine.Instruction, labels []int, bound []bool, labelIndex []int) ([]machine.FlagMask, error) {
	live := make([]machine.FlagMask, len(code)+1)
	for changed := true; changed; {
		changed = false
		for n := len(code) - 1; n >= 0; n-- {
			i := code[n]
			after := live[n+1]
			switch {
			case i.Op == machine.OpReturn:
				after = 0
			case isBranch(i.Op):
				if i.Imm >= uint64(len(labels)) || !bound[i.Imm] {
					return nil, machine.ErrInvalidBranch
				}
				if i.Op == machine.OpJump {
					after = 0
				}
				after |= live[labelIndex[i.Imm]]
			}
			needed := flagReads(i) | after&^killedFlags(s, code, n)
			if needed != live[n] {
				live[n] = needed
				changed = true
			}
		}
	}
	return live, nil
}

// The stream's recorded flags follow its options, which a caller may give
// without exact amd64 flag facts. Liveness needs the flags an instruction
// writes on every CPU as kills, and every flag a candidate may write.

// everyCPU enables the extensions whose instructions write fewer flags, so
// its facts are what every amd64 CPU writes.
var everyCPU = sync.OnceValue(func() abi.ResolvedCPU {
	p, err := abi.CPUProfile{Extensions: []string{"bmi2"}}.Resolved("amd64")
	if err != nil {
		panic(err)
	}
	return p
})

// killedFlags returns the flags instruction n certainly overwrites: those
// both the stream and amd64 report.
func killedFlags(s *machine.Stream, code []machine.Instruction, n int) machine.FlagMask {
	return s.ModifiedFlagsAt(n) & FlagFactsWithCPU(code[n], everyCPU()).Modified
}

// candidateFlags returns every flag candidate n may write.
func candidateFlags(s *machine.Stream, code []machine.Instruction, n int) machine.FlagMask {
	return s.ModifiedFlagsAt(n) | FlagFacts(code[n]).Modified
}

// shorterWithoutFlags reports whether i, which takes size bytes, has a
// flag-preserving form that is shorter.
func shorterWithoutFlags(s *machine.Stream, i machine.Instruction, size int) bool {
	switch i.Op {
	case machine.OpBinary, machine.OpBinaryImmediate, machine.OpInc, machine.OpDec:
	default:
		return false
	}
	if i.PreserveFlags || i.A == i.B {
		return false
	}
	if _, ok := noFlagsForm(i); !ok {
		return false
	}
	without := i
	without.PreserveFlags = true
	return s.Size(without) < size
}

// flagReads reports the flags an instruction reads. Conditions read every
// tracked flag; the opaque bytes of OpLiteralData may read any.
func flagReads(i machine.Instruction) machine.FlagMask {
	switch i.Op {
	case machine.OpJumpIf, machine.OpSelectFlags, machine.OpLiteralData:
		return machine.FlagSign | machine.FlagZero | machine.FlagCarry
	case machine.OpWordCarry, machine.OpCarryBit:
		if i.CarryMode == machine.CarryFlags {
			return machine.FlagCarry
		}
	}
	return 0
}
