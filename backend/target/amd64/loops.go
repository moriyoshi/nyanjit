package amd64

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// fuseLoops replaces DEC RCX; JNZ with LOOP when the decrement's flags
// are dead along both outgoing paths. It runs after branch compaction and
// before range relaxation. The long form preserves reach until relaxation.
func fuseLoops(stream *machine.Stream, labels []int, bound []bool) ([]int, error) {
	if stream == nil || len(labels) != len(bound) {
		return nil, machine.ErrInvalidBranch
	}
	old := append([]machine.Instruction(nil), stream.Instructions()...)
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
			return nil, machine.ErrInvalidBranch
		}
		labelIndex[label] = index
		protected[index] = true
	}
	// live[i] holds the flags read on some path from instruction i before
	// they are written again, per flag; a writer of only some flags leaves
	// the others live. The graph may have backedges, so iterate to a fixed
	// point. Kills and reads follow amd64's own flag model, as in
	// ElideDeadFlags, not the stream's options.
	live := make([]machine.FlagMask, len(old)+1)
	successors := func(i int, ins machine.Instruction) ([]int, error) {
		if ins.Op == machine.OpReturn {
			return nil, nil
		}
		if isBranch(ins.Op) {
			if ins.Imm >= uint64(len(labels)) || !bound[int(ins.Imm)] {
				return nil, machine.ErrInvalidBranch
			}
			target := labelIndex[int(ins.Imm)]
			if ins.Op == machine.OpJump {
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
			var after machine.FlagMask
			for _, next := range succ {
				after |= live[next]
			}
			if needed := flagReads(old[i]) | after&^killedFlags(stream, old, i); needed != live[i] {
				live[i], changed = needed, true
			}
		}
	}
	remove := make([]bool, len(old))
	fused := false
	for i := 0; i+1 < len(old); i++ {
		dec, branch := old[i], old[i+1]
		if dec.Op != machine.OpDec || dec.A != 1 || dec.B != 1 ||
			branch.Op != machine.OpJumpIf || branch.Condition != op.BranchNotEqual || protected[i+1] ||
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
		// LOOP leaves flags alone, so every flag the decrement writes must
		// be dead after the branch.
		var after machine.FlagMask
		for _, next := range succ {
			after |= live[next]
		}
		if after&candidateFlags(stream, old, i) != 0 {
			continue
		}
		old[i] = machine.Instruction{Op: machine.OpLoop, A: 1, Imm: branch.Imm, Long: true}
		remove[i+1] = true
		fused = true
		i++
	}
	if !fused {
		// Nothing moved; drop the facts as the replacement would have.
		stream.ForgetFlags()
		return labels, nil
	}
	oldToNew := make([]int, len(old)+1)
	kept := make([]machine.Instruction, 0, len(old))
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

// FuseLoops implements machine.LoopFuser with the LOOP instruction.
func (Policy) FuseLoops(s *machine.Stream, labels []int, bound []bool) ([]int, []bool, error) {
	labels, err := fuseLoops(s, labels, bound)
	if err != nil {
		return nil, nil, err
	}
	return labels, bound, nil
}

func isBranch(op machine.Opcode) bool {
	switch op {
	case machine.OpJump, machine.OpJumpIf, machine.OpBranchZero, machine.OpBranchBit, machine.OpLoop:
		return true
	default:
		return false
	}
}
