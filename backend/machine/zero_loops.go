package machine

import (
	"slices"

	"github.com/moriyoshi/nyanjit/backend/op"
)

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
	live, err := liveFlags(s, old, labels, bound, labelIndex)
	if err != nil {
		return nil, err
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
			// Without flags, register 31 names SP rather than XZR.
			if dec.A == 31 || live[j+1]|live[labelIndex[branch.Imm]] != 0 {
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

// liveFlags returns, for each instruction of code, the flags read on some
// path from it before the stream says they are written again. A worklist
// revisits an instruction only when the flags live after it grow, so the
// work is linear in the code and its branches.
func liveFlags(s *Stream, code []Instruction, labels []int, bound []bool, labelIndex []int) ([]FlagMask, error) {
	// branchesTo[first[i]:first[i+1]] are the branches that target
	// instruction i.
	first := make([]int, len(code)+2)
	for _, ins := range code {
		if !isBranch(ins.Op) {
			continue
		}
		if ins.Imm >= uint64(len(labels)) || !bound[ins.Imm] {
			return nil, ErrInvalidBranch
		}
		first[labelIndex[ins.Imm]+1]++
	}
	for i := range len(code) + 1 {
		first[i+1] += first[i]
	}
	branchesTo := make([]int, first[len(code)+1])
	fill := slices.Clone(first)
	for i, ins := range code {
		if isBranch(ins.Op) {
			target := labelIndex[ins.Imm]
			branchesTo[fill[target]] = i
			fill[target]++
		}
	}
	live := make([]FlagMask, len(code)+1)
	queued := make([]bool, len(code))
	// The last instruction is visited first, as a backward pass would.
	work := make([]int, len(code))
	for i := range code {
		work[i], queued[i] = i, true
	}
	push := func(i int) {
		if !queued[i] {
			work, queued[i] = append(work, i), true
		}
	}
	for len(work) != 0 {
		i := work[len(work)-1]
		work, queued[i] = work[:len(work)-1], false
		ins := code[i]
		var needed FlagMask
		switch {
		case ins.Op == OpReturn || ins.Op == OpTailCall, ins.Op == OpJumpTable:
			// A jump table compares its index itself, so no flags reach
			// its targets from before it.
		case ins.Op == OpJump:
			needed = live[labelIndex[ins.Imm]]
		case isBranch(ins.Op):
			needed = live[labelIndex[ins.Imm]] | live[i+1]
		default:
			needed = live[i+1]
		}
		needed &^= s.ModifiedFlagsAt(i)
		if ins.Op == OpJumpIf || ins.Op == OpSelectFlags {
			needed |= FlagSign | FlagZero | FlagCarry
		}
		if (ins.Op == OpCarryBit || ins.Op == OpWordCarry) && ins.CarryMode == CarryFlags || ins.Op == OpBinaryCarry {
			needed |= FlagCarry
		}
		if needed == live[i] {
			continue
		}
		live[i] = needed
		if i > 0 && code[i-1].Op != OpReturn && code[i-1].Op != OpJump && code[i-1].Op != OpTailCall {
			push(i - 1)
		}
		for _, from := range branchesTo[first[i]:first[i+1]] {
			push(from)
		}
	}
	return live, nil
}
