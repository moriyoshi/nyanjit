package machine

import "github.com/moriyoshi/nyanjit/ir"

// FuseZeroLoops uses a direct register branch for ARM/RISC countdown loops.
// ARM may remove the decrement's flag writes only when flags are dead on both
// outgoing paths. RISC has no arithmetic flags; its zero compare is a pseudo-op.
func FuseZeroLoops(s *Stream, labels []int, bound []bool, architecture string) ([]int, error) {
	if s == nil || len(labels) != len(bound) {
		return nil, ErrInvalidBranch
	}
	old := append([]Instruction(nil), s.Instructions()...)
	positions := make([]int, len(old)+1)
	indexAt := map[int]int{}
	for i, ins := range old {
		if _, ok := indexAt[positions[i]]; !ok {
			indexAt[positions[i]] = i
		}
		positions[i+1] = positions[i] + s.Size(ins)
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
			needed &^= s.ModifiedFlags(old[i])
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
	remove := make([]bool, len(old))
	for i, dec := range old {
		if dec.Op != OpDec {
			continue
		}
		j := i + 1
		if architecture == "riscv64" {
			if j >= len(old) || old[j].Op != OpCompareImmediate || old[j].Imm != 0 || old[j].A != dec.A || protected[j] {
				continue
			}
			j++
		}
		if j >= len(old) || protected[j] {
			continue
		}
		branch := old[j]
		if branch.Op != OpJumpIf || branch.Condition != ir.BranchNotEqual || branch.Imm >= uint64(len(labels)) || labelIndex[branch.Imm] > i {
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
			old[i].PreserveFlags = true
		} else if architecture == "riscv64" {
			remove[i+1] = true
		} else {
			continue
		}
		branch.Op, branch.A, branch.Bits = OpBranchZero, dec.A, 64
		old[j] = branch
	}
	kept := make([]Instruction, 0, len(old))
	oldToNew := make([]int, len(old)+1)
	for i, ins := range old {
		oldToNew[i] = len(kept)
		if !remove[i] {
			kept = append(kept, ins)
		}
	}
	oldToNew[len(old)] = len(kept)
	s.Replace(kept)
	newPC := make([]int, len(kept)+1)
	for i, ins := range kept {
		newPC[i+1] = newPC[i] + s.Size(ins)
	}
	for l := range labels {
		if bound[l] {
			labels[l] = newPC[oldToNew[labelIndex[l]]]
		}
	}
	return labels, nil
}
