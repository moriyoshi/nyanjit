package machine

import (
	"errors"
	"slices"
)

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
// The target supplies its displacement rule through fits. The read-only result
// is borrowed until the next ResolveBranches, CompactBranches or RelaxBranches
// call on stream, or its Workspace Reset, Rewind, Trim or Release. Copy it to retain it.
func ResolveBranches(stream *Stream, labels []int, bound []bool,
	fits func(Instruction, int, int, int) bool) ([]LocatedInstruction, error) {
	if stream != nil {
		stream.poisonLayoutResults()
	}
	var localWork layoutBuffers
	work := &localWork
	if stream != nil && (stream.pooled || workspacePoison) {
		work = &stream.layout
	}

	if stream == nil || len(labels) != len(bound) || fits == nil {
		return nil, ErrInvalidBranch
	}
	located := workAppendTracked(&work.located, &work.history.located, len(stream.Instructions()))
	defer func() { work.located = located }()
	positions := workResetTracked(&work.positions, &work.history.positions, len(stream.Instructions())+1)
	position := 0
	for index := range stream.Instructions() {
		positions[index] = position
		size := stream.SizeAt(index)
		if size < 0 {
			return nil, ErrInvalidBranch
		}
		position += size
	}
	positions[len(positions)-1] = position
	if position != stream.PC() {
		return nil, ErrInvalidBranch
	}
	pc := 0
	for index, ins := range stream.Instructions() {
		size := stream.SizeAt(index)
		if isBranch(ins.Op) {
			if ins.Imm >= uint64(len(bound)) || !bound[int(ins.Imm)] {
				return nil, ErrInvalidBranch
			}
			targetPC := labels[int(ins.Imm)]
			if _, ok := instructionAt(positions, targetPC); !ok {
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
// in place and also returned for convenience. Prior Instructions and resolved
// results expire at this layout pass, regardless of how the stream was created.
func CompactBranches(stream *Stream, labels []int, bound []bool) ([]int, error) {
	if stream != nil {
		stream.poisonLayoutResults()
	}
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
	var localWork layoutBuffers
	work := &localWork
	if stream != nil && (stream.pooled || workspacePoison) {
		work = &stream.layout
	}

	// Work on a copy so failed validation cannot partially rewrite the stream.
	old := append(workAppendTracked(&work.old, &work.history.old, len(stream.Instructions())), stream.Instructions()...)
	work.old = old
	positions := workResetTracked(&work.positions, &work.history.positions, len(old)+1)
	for i := range old {
		size := stream.SizeAt(i)
		if size < 0 {
			return nil, ErrInvalidBranch
		}
		positions[i+1] = positions[i] + size
	}
	labelIndex := workResetTracked(&work.labelIndex, &work.history.labelIndex, len(labels))
	for label := range labels {
		if !bound[label] {
			labelIndex[label] = -1
			continue
		}
		index, ok := instructionAt(positions, labels[label])
		if !ok {
			return nil, ErrInvalidBranch
		}
		labelIndex[label] = index
	}
	destinations, err := threadedLabels(old, labelIndex, bound, work)
	if err != nil {
		return nil, err
	}
	for i := range old {
		if isBranch(old[i].Op) {
			old[i].Imm = uint64(destinations[int(old[i].Imm)])
		}
	}
	remove := workResetTracked(&work.remove, &work.history.remove, len(old))
	referenced := workResetTracked(&work.referenced, &work.history.referenced, len(old)+1)
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
	reachable := workResetTracked(&work.reachable, &work.history.reachable, len(old))
	pending := append(workAppendTracked(&work.pending, &work.history.pending, len(old)), 0)
	defer func() { work.pending = pending }()
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
	oldToNew := workResetTracked(&work.oldToNew, &work.history.oldToNew, len(old)+1)
	kept := workAppendTracked(&work.kept, &work.history.kept, len(old))
	work.kept = nil
	keptCommitted := false
	defer func() {
		if !keptCommitted {
			work.kept = kept[:0]
		}
	}()
	for i, ins := range old {
		oldToNew[i] = len(kept)
		if reachable[i] && !remove[i] {
			kept = append(kept, ins)
		}
	}
	oldToNew[len(old)] = len(kept)
	newPositions := workResetTracked(&work.newPositions, &work.history.newPositions, len(kept)+1)
	for i, ins := range old {
		if !reachable[i] || remove[i] {
			continue
		}
		size := stream.SizeAt(i)
		original := stream.instructions[i]
		// Threading changes label operands and inversion changes conditions.
		// A public Size callback may depend on either, so remeasure all changed
		// branches while retaining the validated sizes of unchanged operations.
		if ins.Imm != original.Imm || ins.Condition != original.Condition ||
			ins.Long != original.Long || ins.Far != original.Far {
			size = stream.Size(ins)
		}
		index := oldToNew[i]
		newPositions[index+1] = newPositions[index] + size
	}
	// Commit the stream before updating caller-owned labels.
	previous := stream.instructions
	stream.replaceKeepingFacts(kept, newPositions)
	work.kept = previous[:0]
	keptCommitted = true
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
// Prior Instructions and resolved results expire at this layout pass for all streams.
func RelaxBranches(stream *Stream, labels []int, bound []bool,
	fits func(Instruction, int, int, int) bool) ([]int, error) {
	if stream != nil {
		stream.poisonLayoutResults()
	}
	var localWork layoutBuffers
	work := &localWork
	if stream != nil && (stream.pooled || workspacePoison) {
		work = &stream.layout
	}

	if stream == nil || len(labels) != len(bound) || fits == nil {
		return nil, ErrInvalidBranch
	}
	insns := append(workAppendTracked(&work.relax, &work.history.relax, len(stream.Instructions())), stream.Instructions()...)
	work.relax = nil
	relaxCommitted := false
	defer func() {
		if !relaxCommitted {
			work.relax = insns[:0]
		}
	}()
	positions := workResetTracked(&work.positions, &work.history.positions, len(insns)+1)
	sizes := workResetTracked(&work.sizes, &work.history.sizes, len(insns))
	for i := range sizes {
		sizes[i] = stream.SizeAt(i)
	}
	for i := range insns {
		positions[i+1] = positions[i] + sizes[i]
	}
	labelIndices := workResetTracked(&work.labelIndex, &work.history.labelIndex, len(labels))
	for label, pc := range labels {
		if !bound[label] {
			continue
		}
		index, ok := instructionAt(positions, pc)
		if !ok {
			return nil, ErrInvalidBranch
		}
		labelIndices[label] = index
	}
	for {
		for i := range insns {
			positions[i+1] = positions[i] + sizes[i]
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
			oldSize, shortSize := sizes[i], stream.Size(short)
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
				sizes[i] = shortSize
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	previous := stream.instructions
	stream.replaceKeepingFacts(insns, positions)
	work.relax = previous[:0]
	relaxCommitted = true
	for i := range insns {
		positions[i+1] = positions[i] + sizes[i]
	}
	for label := range labels {
		if bound[label] {
			labels[label] = positions[labelIndices[label]]
		}
	}
	return labels, nil
}

// Public layout slices must remain poisoned after the next pass returns,
// rather than immediately becoming plausible instructions through buffer reuse.
// Only debug builds detach these exposed buffers; private work buffers still
// reuse poisoned storage so incomplete reinitialization remains detectable.
func (s *Stream) poisonLayoutResults() {
	if !workspacePoison {
		return
	}
	bad := Instruction{Op: 255, Imm: ^uint64(0), Symbol: "expired layout"}
	borrowed := s.borrow.take()
	previous := s.instructions
	if borrowed || cap(s.layout.kept) != 0 || cap(s.layout.relax) != 0 {
		// The stream may alias an expired result or an owned Replace input.
		s.instructions = slices.Clone(s.instructions)
	}
	if borrowed {
		for i := range previous[:cap(previous)] {
			previous[:cap(previous)][i] = bad
		}
	}
	for _, buf := range [][]Instruction{s.layout.kept, s.layout.relax} {
		for i := range buf[:cap(buf)] {
			buf[:cap(buf)][i] = bad
		}
	}
	s.layout.kept = poisonReplacement(s.layout.kept)
	s.layout.relax = poisonReplacement(s.layout.relax)

	for i := range s.layout.located[:cap(s.layout.located)] {
		s.layout.located[:cap(s.layout.located)][i] = LocatedInstruction{Instruction: bad, PC: -0x5a5a}
	}
	s.layout.located = poisonReplacement(s.layout.located)
}
