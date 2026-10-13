package arm64

import (
	"sort"

	"github.com/moriyoshi/nyanjit/backend/machine"
)

const everyFlag = machine.FlagSign | machine.FlagZero | machine.FlagCarry

// ElideDeadFlags implements machine.FlagElider. ADDS, SUBS and ANDS (and
// the decrement's SUBS) become ADD, SUB and AND where no control-flow path
// reads NZCV before its next definition. Each instruction that writes NZCV
// writes all four flags, so the tracked mask stands for V as well. The forms
// have the same size, so labels stay.
func (Policy) ElideDeadFlags(s *machine.Stream, labels []int, bound []bool) ([]int, error) {
	if s == nil || len(labels) != len(bound) {
		return nil, machine.ErrInvalidBranch
	}
	code := s.Instructions()
	// Rewrites wait until every candidate is decided, so the analysis sees
	// only the original instructions.
	var selected []int
	// Nearly every arithmetic instruction writes NZCV, so the straight line
	// after a candidate usually settles it; only a branch first needs the
	// whole graph.
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
			return nil, err
		}
		for _, n := range undecided {
			// A candidate is not a branch, so its only successor is n+1.
			if live[n+1] == 0 {
				selected = append(selected, n)
			}
		}
	}
	if selected == nil {
		return labels, nil
	}
	// The selected forms write no flags, so facts naming them are gone.
	s.ForgetFlags()
	for _, n := range selected {
		i := code[n]
		i.PreserveFlags = true
		s.ReplaceAt(n, i)
	}
	return labels, nil
}

// flagCandidate reports whether i writes NZCV only as a side effect that a
// flag-free form of the same size avoids. Without the S bit, register 31
// names SP as an immediate form's destination, so XZR keeps the S form.
func flagCandidate(i machine.Instruction) bool {
	if i.PreserveFlags {
		return false
	}
	switch i.Op {
	case machine.OpBinary:
		return binaryFlagBits(i.Arithmetic) != 0
	case machine.OpBinaryImmediate:
		return binaryFlagBits(i.Arithmetic) != 0 && i.A != 31
	case machine.OpDec:
		return i.A != 31
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
	kills := make([]machine.FlagMask, len(code))
	for n := range code {
		kills[n] = killedFlags(s, code, n)
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
			needed := flagReads(i) | after&^kills[n]
			if needed != live[n] {
				live[n] = needed
				changed = true
			}
		}
	}
	return live, nil
}

// killedFlags returns the flags instruction n certainly overwrites: those
// both the stream and FlagFacts report. A stream without flag metadata
// kills nothing, which keeps every flag live longer.
func killedFlags(s *machine.Stream, code []machine.Instruction, n int) machine.FlagMask {
	m := s.ModifiedFlagsAt(n)
	if m == 0 {
		return 0
	}
	return m & FlagFacts(code[n]).Modified
}

// flagReads reports the flags an instruction reads. Conditions read every
// tracked flag (and V with them); the opaque bytes of OpLiteralData may read
// any.
func flagReads(i machine.Instruction) machine.FlagMask {
	switch i.Op {
	case machine.OpJumpIf, machine.OpSelectFlags, machine.OpLoop, machine.OpLiteralData, machine.OpSaveCondition:
		return everyFlag
	case machine.OpBinaryCarry:
		return machine.FlagCarry
	case machine.OpWordCarry, machine.OpCarryBit:
		if i.CarryMode == machine.CarryFlags {
			return machine.FlagCarry
		}
	}
	return 0
}

func isBranch(o machine.Opcode) bool {
	switch o {
	case machine.OpJump, machine.OpJumpIf, machine.OpBranchZero, machine.OpBranchBit, machine.OpLoop:
		return true
	}
	return false
}
