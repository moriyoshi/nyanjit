package machine

import "github.com/moriyoshi/nyanjit/backend/op"

// FuseZeroLoops uses a direct register branch for ARM/RISC countdown loops.
// ARM may remove the decrement's flag writes only when flags are dead on both
// outgoing paths. RISC has no arithmetic flags; its zero compare is a pseudo-op.
func FuseZeroLoops(s *Stream, labels []int, bound []bool, architecture string) ([]int, error) {
	if s == nil || len(labels) != len(bound) {
		return nil, ErrInvalidBranch
	}
	old := s.Instructions()
	positions := make([]int, len(old)+1)
	indexAt := map[int]int{}
	for i := range old {
		if _, ok := indexAt[positions[i]]; !ok {
			indexAt[positions[i]] = i
		}
		positions[i+1] = positions[i] + s.SizeAt(i)
	}
	indexAt[positions[len(old)]] = len(old)
	labelIndex := make([]int, len(labels))
	protected := map[int]bool{}
	for l, pc := range labels {
		if !bound[l] {
			continue
		}
		i, ok := indexAt[pc]
		if !ok {
			return nil, ErrInvalidBranch
		}
		labelIndex[l] = i
		protected[i] = true
	}
	successors := func(i int) ([]int, error) {
		ins := old[i]
		if ins.Op == OpReturn {
			return nil, nil
		}
		if isBranch(ins.Op) {
			if ins.Imm >= uint64(len(labels)) || !bound[ins.Imm] {
				return nil, ErrInvalidBranch
			}
			to := labelIndex[ins.Imm]
			if ins.Op == OpJump {
				return []int{to}, nil
			}
			return []int{to, i + 1}, nil
		}
		return []int{i + 1}, nil
	}
	live := make([]FlagMask, len(old)+1)
	for changed := true; changed; {
		changed = false
		for i := len(old) - 1; i >= 0; i-- {
			next, err := successors(i)
			if err != nil {
				return nil, err
			}
			var needed FlagMask
			for _, n := range next {
				needed |= live[n]
			}
			needed &^= s.ModifiedFlagsAt(i)
			if old[i].Op == OpJumpIf || old[i].Op == OpSelectFlags {
				needed |= FlagSign | FlagZero | FlagCarry
			}
			if (old[i].Op == OpCarryBit || old[i].Op == OpWordCarry) && old[i].CarryMode == CarryFlags {
				needed |= FlagCarry
			}
			if needed != live[i] {
				live[i] = needed
				changed = true
			}
		}
	}
	// A fusion rewrites a decrement and the branch after it, and reads only
	// instructions after earlier fusions, so the original stream decides
	// every candidate.
	remove := make([]bool, len(old))
	var changed []int
	var with []Instruction
	for i, dec := range old {
		if dec.Op != OpDec {
			continue
		}
		j := i + 1
		if architecture == "riscv64" || architecture == "loong64" {
			if j >= len(old) || old[j].Op != OpCompareImmediate || old[j].Imm != 0 || old[j].A != dec.A || protected[j] {
				continue
			}
			j++
		}
		if j >= len(old) || protected[j] {
			continue
		}
		branch := old[j]
		if branch.Op != OpJumpIf || branch.Condition != op.BranchNotEqual || branch.Imm >= uint64(len(labels)) || labelIndex[branch.Imm] > i {
			continue
		}
		if architecture == "arm64" {
			next, err := successors(j)
			if err != nil {
				return nil, err
			}
			var needed FlagMask
			for _, n := range next {
				needed |= live[n]
			}
			if needed != 0 {
				continue
			}
			dec.PreserveFlags = true
			changed, with = append(changed, i), append(with, dec)
		} else if architecture == "riscv64" || architecture == "loong64" {
			remove[i+1] = true
		} else {
			continue
		}
		branch.Op, branch.A, branch.Bits = OpBranchZero, dec.A, 64
		changed, with = append(changed, j), append(with, branch)
	}
	// The fused decrements no longer establish the flags.
	s.ForgetFlags()
	if changed == nil {
		return labels, nil
	}
	oldToNew := make([]int, len(old)+1)
	if architecture == "arm64" {
		// Nothing is removed, so the rewrites stay in place.
		for k, i := range changed {
			s.ReplaceAt(i, with[k])
		}
		for i := range oldToNew {
			oldToNew[i] = i
		}
	} else {
		kept := make([]Instruction, 0, len(old))
		next := 0
		for i, ins := range old {
			oldToNew[i] = len(kept)
			if next < len(changed) && changed[next] == i {
				ins = with[next]
				next++
			}
			if !remove[i] {
				kept = append(kept, ins)
			}
		}
		oldToNew[len(old)] = len(kept)
		s.Replace(kept)
	}
	newPC := make([]int, len(s.Instructions())+1)
	for i := range newPC[1:] {
		newPC[i+1] = newPC[i] + s.SizeAt(i)
	}
	for l := range labels {
		if bound[l] {
			labels[l] = newPC[oldToNew[labelIndex[l]]]
		}
	}
	return labels, nil
}
