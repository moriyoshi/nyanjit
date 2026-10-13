package ppc64

import (
	"sort"

	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

const everyFlag = machine.FlagSign | machine.FlagZero | machine.FlagCarry

// elideDeadFlags selects non-recording, non-carry arithmetic when neither
// CR0 nor XER is read before its next definition on any control-flow path.
// The alternate POWER forms have the same size, so label PCs remain valid.
func elideDeadFlags(s *machine.Stream, labels []int, bound []bool) error {
	if s == nil || len(labels) != len(bound) {
		return machine.ErrInvalidBranch
	}
	code := s.Instructions()
	// Rewrites wait until every candidate is decided, so the analysis
	// sees only the original instructions.
	var selected []int
	// Most arithmetic records CR0 or XER, so the straight line after a
	// candidate usually settles it; only a branch first needs the whole graph.
	var undecided []int
	for n, i := range code {
		if !flagCandidate(i) {
			continue
		}
		switch flagsAfter(s, code, n) {
		case flagsDead:
			selected = append(selected, n)
		case flagsUnknown:
			undecided = append(undecided, n)
		}
	}
	if undecided != nil {
		live, err := liveFlags(s, code, labels, bound)
		if err != nil {
			return err
		}
		for _, n := range undecided {
			// A candidate is not a branch, so its only successor is n+1.
			if live[n+1] == 0 {
				selected = append(selected, n)
			}
		}
	}
	if selected == nil {
		return nil
	}
	// The selected forms write no flags, so facts naming them are gone;
	// the forms keep their sizes.
	s.ForgetFlags()
	for _, n := range selected {
		i := code[n]
		i.PreserveFlags = true
		s.ReplaceAt(n, i)
	}
	return nil
}

// flagCandidate reports whether i has a non-recording, non-carry form.
func flagCandidate(i machine.Instruction) bool {
	switch i.Op {
	case machine.OpBinary, machine.OpBinaryImmediate:
		return i.Arithmetic == op.OpAdd || i.Arithmetic == op.OpSub
	case machine.OpInc, machine.OpDec:
		return true
	}
	return false
}

const (
	flagsLive = iota
	flagsDead
	flagsUnknown
)

// flagsAfter follows the straight line after instruction n until every flag
// is written again or leaves the function, a flag is read, or a branch may
// take the flags elsewhere. Kills are those of the original instructions: a
// selected form had no flag live after it, so it hid no read.
func flagsAfter(s *machine.Stream, code []machine.Instruction, n int) int {
	needed := everyFlag
	for k, i := range code[n+1:] {
		if ppcFlagReads(i)&needed != 0 {
			return flagsLive
		}
		if i.Op == machine.OpReturn {
			return flagsDead
		}
		if needed &^= s.ModifiedFlagsAt(n + 1 + k); needed == 0 {
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
func liveFlags(s *machine.Stream, code []machine.Instruction, labels []int, bound []bool) ([]machine.FlagMask, error) {
	positions := make([]int, len(code)+1)
	for n := range code {
		positions[n+1] = positions[n] + s.SizeAt(n)
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
			needed := ppcFlagReads(i) | after&^s.ModifiedFlagsAt(n)
			if needed != live[n] {
				live[n] = needed
				changed = true
			}
		}
	}
	return live, nil
}

// ElideDeadFlags implements machine.FlagElider: it clears CR0 and XER
// updates that no path reads. The forms have the same size, so labels stay.
func (Policy) ElideDeadFlags(s *machine.Stream, labels []int, bound []bool) ([]int, error) {
	if err := elideDeadFlags(s, labels, bound); err != nil {
		return nil, err
	}
	return labels, nil
}
