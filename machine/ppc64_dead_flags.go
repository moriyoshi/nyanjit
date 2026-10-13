package machine

import "github.com/moriyoshi/nyanjit/ir"

// ElidePPC64DeadFlags selects non-recording, non-carry arithmetic when neither
// CR0 nor XER is read before its next definition on any control-flow path.
// The alternate POWER forms have the same size, so label PCs remain valid.
func ElidePPC64DeadFlags(s *Stream, labels []int, bound []bool) error {
	if s == nil || len(labels) != len(bound) {
		return ErrInvalidBranch
	}
	old := append([]Instruction(nil), s.Instructions()...)
	at := map[int]int{}
	pc := 0
	for n, i := range old {
		at[pc] = n
		pc += s.Size(i)
	}
	at[pc] = len(old)
	successors := func(n int) ([]int, error) {
		i := old[n]
		if i.Op == OpReturn {
			return nil, nil
		}
		if isBranch(i.Op) {
			if i.Imm >= uint64(len(labels)) || !bound[i.Imm] {
				return nil, ErrInvalidBranch
			}
			next, ok := at[labels[i.Imm]]
			if !ok {
				return nil, ErrInvalidBranch
			}
			if i.Op == OpJump {
				return []int{next}, nil
			}
			return []int{next, n + 1}, nil
		}
		return []int{n + 1}, nil
	}
	live := make([]FlagMask, len(old)+1)
	for changed := true; changed; {
		changed = false
		for n := len(old) - 1; n >= 0; n-- {
			next, err := successors(n)
			if err != nil {
				return err
			}
			var after FlagMask
			for _, x := range next {
				after |= live[x]
			}
			needed := ppcFlagReads(old[n]) | after&^s.ModifiedFlags(old[n])
			if needed != live[n] {
				live[n] = needed
				changed = true
			}
		}
	}
	for n, i := range old {
		switch i.Op {
		case OpBinary, OpBinaryImmediate:
			if i.Arithmetic != ir.OpAdd && i.Arithmetic != ir.OpSub {
				continue
			}
		case OpInc, OpDec:
		default:
			continue
		}
		next, err := successors(n)
		if err != nil {
			return err
		}
		var after FlagMask
		for _, x := range next {
			after |= live[x]
		}
		if after == 0 {
			old[n].PreserveFlags = true
		}
	}
	s.Replace(old)
	return nil
}
