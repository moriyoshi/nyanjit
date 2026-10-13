package machine

import "errors"

// ErrInvalidBranch means a branch refers to an unbound label, a position that
// is not an instruction boundary, or an invalid path through the stream.
var ErrInvalidBranch = errors.New("nyanjit: invalid machine branch")

// ErrBranchOutOfRange means a target-specific branch cannot reach its label.
var ErrBranchOutOfRange = errors.New("nyanjit: machine branch out of range")

// LocatedInstruction is a physical instruction with its assigned byte PC.
type LocatedInstruction struct {
	Instruction Instruction
	PC          int
}

func isBranch(op Opcode) bool {
	switch op {
	case OpJump, OpJumpIf, OpBranchZero, OpBranchBit, OpLoop:
		return true
	default:
		return false
	}
}

func isLimitedBranch(op Opcode) bool {
	return op == OpJump || op == OpJumpIf || op == OpBranchZero || op == OpBranchBit || op == OpLoop
}

// ResolveBranches replaces label operands with byte PCs after compaction.
// The target supplies its displacement rule through fits.
func ResolveBranches(stream *Stream, labels []int, bound []bool,
	fits func(Instruction, int, int, int) bool) ([]LocatedInstruction, error) {
	if stream == nil || len(labels) != len(bound) || fits == nil {
		return nil, ErrInvalidBranch
	}
	located := make([]LocatedInstruction, 0, len(stream.Instructions()))
	boundaries := make(map[int]bool, len(stream.Instructions())+1)
	position := 0
	for _, ins := range stream.Instructions() {
		boundaries[position] = true
		size := stream.Size(ins)
		if size < 0 {
			return nil, ErrInvalidBranch
		}
		position += size
	}
	boundaries[position] = true
	if position != stream.PC() {
		return nil, ErrInvalidBranch
	}
	pc := 0
	for _, ins := range stream.Instructions() {
		size := stream.Size(ins)
		if isBranch(ins.Op) {
			if ins.Imm >= uint64(len(bound)) || !bound[int(ins.Imm)] {
				return nil, ErrInvalidBranch
			}
			targetPC := labels[int(ins.Imm)]
			if !boundaries[targetPC] {
				return nil, ErrInvalidBranch
			}
			if !fits(ins, pc, targetPC, size) {
				return nil, ErrBranchOutOfRange
			}
			ins.Imm = uint64(targetPC)
		}
		located = append(located, LocatedInstruction{Instruction: ins, PC: pc})
		pc += size
	}
	return located, nil
}

// CompactBranches resolves jump-only labels, removes fallthrough jumps and
// unreachable instructions, and updates label byte positions. Jump operands
// are label indices until final assembly. The supplied labels slice is updated
// in place and also returned for convenience.
func CompactBranches(stream *Stream, labels []int, bound []bool) ([]int, error) {
	if stream == nil || len(labels) != len(bound) {
		return nil, ErrInvalidBranch
	}
	for {
		before := len(stream.Instructions())
		updated, err := compactBranchesOnce(stream, labels, bound)
		if err != nil {
			return nil, err
		}
		labels = updated
		if len(stream.Instructions()) == before {
			return labels, nil
		}
	}
}

func compactBranchesOnce(stream *Stream, labels []int, bound []bool) ([]int, error) {
	// Work on a copy so failed validation cannot partially rewrite the stream.
	old := append([]Instruction(nil), stream.Instructions()...)
	positions := make(map[int]int, len(old)+1)
	pc := 0
	for i, ins := range old {
		// A zero-byte pseudo-op can establish the operands for the branch at
		// this PC. Keep the first instruction at a boundary so compaction
		// does not discard that pseudo-op when a label points here.
		if _, exists := positions[pc]; !exists {
			positions[pc] = i
		}
		size := stream.Size(ins)
		if size < 0 {
			return nil, ErrInvalidBranch
		}
		pc += size
	}
	positions[pc] = len(old)
	labelIndex := make([]int, len(labels))
	for label := range labels {
		if !bound[label] {
			labelIndex[label] = -1
			continue
		}
		index, ok := positions[labels[label]]
		if !ok {
			return nil, ErrInvalidBranch
		}
		labelIndex[label] = index
	}
	for i := range old {
		if !isBranch(old[i].Op) {
			continue
		}
		if old[i].Imm >= uint64(len(labels)) || !bound[int(old[i].Imm)] {
			return nil, ErrInvalidBranch
		}
		start := int(old[i].Imm)
		destination := start
		seen := make(map[int]bool)
		for {
			if seen[destination] {
				destination = start
				break
			}
			seen[destination] = true
			index := labelIndex[destination]
			if index >= len(old) || old[index].Op != OpJump {
				break
			}
			next := old[index].Imm
			if next >= uint64(len(labels)) || !bound[int(next)] {
				return nil, ErrInvalidBranch
			}
			if int(next) == destination {
				break
			}
			destination = int(next)
		}
		old[i].Imm = uint64(destination)
	}
	remove := make([]bool, len(old))
	referenced := make(map[int]bool)
	for _, ins := range old {
		if isBranch(ins.Op) {
			referenced[labelIndex[int(ins.Imm)]] = true
		}
	}
	// Jump-only edge labels have already been redirected. Fold
	// "if condition: fallthrough; jump target" into the inverse conditional
	// when the skipped jump is not an independently reachable instruction.
	for i := 0; i+1 < len(old); i++ {
		if old[i].Op == OpJumpIf && old[i+1].Op == OpJump &&
			labelIndex[int(old[i].Imm)] == i+2 && !referenced[i+1] {
			old[i].Condition ^= 1
			old[i].Imm = old[i+1].Imm
			remove[i+1] = true
		}
	}
	for i, ins := range old {
		if ins.Op == OpJump && labelIndex[int(ins.Imm)] == i+1 {
			remove[i] = true
		}
	}
	reachable := make([]bool, len(old))
	pending := []int{0}
	for len(pending) != 0 {
		index := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if index < 0 || index >= len(old) {
			return nil, ErrInvalidBranch
		}
		if reachable[index] {
			continue
		}
		reachable[index] = true
		ins := old[index]
		if remove[index] {
			pending = append(pending, index+1)
			continue
		}
		switch ins.Op {
		case OpJump:
			pending = append(pending, labelIndex[int(ins.Imm)])
		case OpJumpIf, OpBranchZero, OpBranchBit, OpLoop:
			pending = append(pending, labelIndex[int(ins.Imm)], index+1)
		case OpReturn:
		default:
			pending = append(pending, index+1)
		}
	}
	oldToNew := make([]int, len(old)+1)
	kept := make([]Instruction, 0, len(old))
	for i, ins := range old {
		oldToNew[i] = len(kept)
		if reachable[i] && !remove[i] {
			kept = append(kept, ins)
		}
	}
	oldToNew[len(old)] = len(kept)
	newPositions := make([]int, len(kept)+1)
	for i, ins := range kept {
		newPositions[i+1] = newPositions[i] + stream.Size(ins)
	}
	// Commit the stream before updating caller-owned labels.
	stream.Replace(kept)
	for label, index := range labelIndex {
		if bound[label] {
			labels[label] = newPositions[oldToNew[index]]
		}
	}
	return labels, nil
}

// RelaxBranches shrinks limited-range branches after compaction. Candidates
// begin in their long form, so each successful change only shortens the
// distance between other branches and their targets. Labels remain indices
// during this pass and are translated to the new byte layout at the end.
func RelaxBranches(stream *Stream, labels []int, bound []bool,
	fits func(Instruction, int, int, int) bool) ([]int, error) {
	if stream == nil || len(labels) != len(bound) || fits == nil {
		return nil, ErrInvalidBranch
	}
	insns := append([]Instruction(nil), stream.Instructions()...)
	positions := make([]int, len(insns)+1)
	indices := make(map[int]int, len(insns)+1)
	for i, ins := range insns {
		positions[i+1] = positions[i] + stream.Size(ins)
		if _, exists := indices[positions[i]]; !exists {
			indices[positions[i]] = i
		}
	}
	indices[positions[len(insns)]] = len(insns)
	labelIndices := make([]int, len(labels))
	for label, pc := range labels {
		if !bound[label] {
			continue
		}
		index, ok := indices[pc]
		if !ok {
			return nil, ErrInvalidBranch
		}
		labelIndices[label] = index
	}
	for {
		for i, ins := range insns {
			positions[i+1] = positions[i] + stream.Size(ins)
		}
		changed := false
		for i, ins := range insns {
			if !isLimitedBranch(ins.Op) || !ins.Long {
				continue
			}
			if ins.Imm >= uint64(len(labels)) || !bound[int(ins.Imm)] {
				return nil, ErrInvalidBranch
			}
			short := ins
			if ins.Far {
				short.Far = false
			} else {
				short.Long = false
			}
			oldSize, shortSize := stream.Size(ins), stream.Size(short)
			if shortSize <= 0 || shortSize >= oldSize {
				continue
			}
			targetIndex := labelIndices[int(ins.Imm)]
			targetPC := positions[targetIndex]
			if targetIndex > i {
				targetPC -= oldSize - shortSize
			}
			if fits(short, positions[i], targetPC, shortSize) {
				insns[i] = short
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	stream.Replace(insns)
	for i, ins := range insns {
		positions[i+1] = positions[i] + stream.Size(ins)
	}
	for label := range labels {
		if bound[label] {
			labels[label] = positions[labelIndices[label]]
		}
	}
	return labels, nil
}
