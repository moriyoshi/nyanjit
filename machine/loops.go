package machine

import "github.com/moriyoshi/nyanjit/ir"

// FuseAMD64Loops replaces DEC RCX; JNZ with LOOP when the decrement's flags
// are dead along both outgoing paths. It runs after branch compaction and
// before range relaxation. The long form preserves reach until relaxation.
func FuseAMD64Loops(stream *Stream, labels []int, bound []bool) ([]int, error) {
	if stream == nil || len(labels) != len(bound) {
		return nil, ErrInvalidBranch
	}
	old := append([]Instruction(nil), stream.Instructions()...)
	positions := make([]int, len(old)+1)
	indexAt := make(map[int]int, len(old)+1)
	for i, ins := range old {
		positions[i+1] = positions[i] + stream.Size(ins)
		indexAt[positions[i]] = i
	}
	indexAt[positions[len(old)]] = len(old)
	labelIndex := make([]int, len(labels))
	protected := make(map[int]bool)
	for label, pc := range labels {
		if !bound[label] {
			continue
		}
		index, ok := indexAt[pc]
		if !ok {
			return nil, ErrInvalidBranch
		}
		labelIndex[label] = index
		protected[index] = true
	}
	// A flag read makes preceding flags live until a writer on every path.
	// The graph may have backedges, so iterate to a fixed point.
	live := make([]bool, len(old)+1)
	successors := func(i int, ins Instruction) ([]int, error) {
		if ins.Op == OpReturn {
			return nil, nil
		}
		if isBranch(ins.Op) {
			if ins.Imm >= uint64(len(labels)) || !bound[int(ins.Imm)] {
				return nil, ErrInvalidBranch
			}
			target := labelIndex[int(ins.Imm)]
			if ins.Op == OpJump {
				return []int{target}, nil
			}
			return []int{target, i + 1}, nil
		}
		return []int{i + 1}, nil
	}
	for changed := true; changed; {
		changed = false
		for i := len(old) - 1; i >= 0; i-- {
			succ, err := successors(i, old[i])
			if err != nil {
				return nil, err
			}
			needed := old[i].Op == OpJumpIf || old[i].Op == OpSelectFlags
			if !needed && !stream.ChangesFlags(old[i]) {
				for _, next := range succ {
					needed = needed || live[next]
				}
			}
			if needed && !live[i] {
				live[i], changed = true, true
			}
		}
	}
	remove := make([]bool, len(old))
	for i := 0; i+1 < len(old); i++ {
		dec, branch := old[i], old[i+1]
		if dec.Op != OpDec || dec.A != 1 || dec.B != 1 ||
			branch.Op != OpJumpIf || branch.Condition != ir.BranchNotEqual || protected[i+1] ||
			branch.Imm >= uint64(len(labels)) || labelIndex[int(branch.Imm)] >= i {
			continue
		}
		shortDistance := positions[labelIndex[int(branch.Imm)]] - (positions[i] + 2)
		if shortDistance < -128 || shortDistance > 127 {
			continue
		}
		succ, err := successors(i+1, branch)
		if err != nil {
			return nil, err
		}
		flagsNeeded := false
		for _, next := range succ {
			flagsNeeded = flagsNeeded || live[next]
		}
		if flagsNeeded {
			continue
		}
		old[i] = Instruction{Op: OpLoop, A: 1, Imm: branch.Imm, Long: true}
		remove[i+1] = true
		i++
	}
	oldToNew := make([]int, len(old)+1)
	kept := make([]Instruction, 0, len(old))
	for i, ins := range old {
		oldToNew[i] = len(kept)
		if !remove[i] {
			kept = append(kept, ins)
		}
	}
	oldToNew[len(old)] = len(kept)
	stream.Replace(kept)
	newPositions := make([]int, len(kept)+1)
	for i, ins := range kept {
		newPositions[i+1] = newPositions[i] + stream.Size(ins)
	}
	for label := range labels {
		if bound[label] {
			labels[label] = newPositions[oldToNew[labelIndex[label]]]
		}
	}
	return labels, nil
}
