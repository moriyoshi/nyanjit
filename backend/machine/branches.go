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
	code := stream.Instructions()
	for index := range code {
		ins := &code[index]
		size := stream.SizeAt(index)
		imm := ins.Imm
		if isBranch(ins.Op) {
			if ins.Imm >= uint64(len(bound)) || !bound[int(ins.Imm)] {
				return nil, ErrInvalidBranch
			}
			targetPC := labels[int(ins.Imm)]
			if _, ok := instructionAt(positions, targetPC); !ok {
				return nil, ErrInvalidBranch
			}
			if !fits(*ins, pc, targetPC, size) {
				return nil, ErrBranchOutOfRange
			}
			imm = uint64(targetPC)
		}
		var table []int
		if ins.Op == OpJumpTable {
			// Table entries are labels until here; the stream keeps them.
			table = make([]int, len(ins.Table))
			for i, label := range ins.Table {
				if label < 0 || label >= len(bound) || !bound[label] {
					return nil, ErrInvalidBranch
				}
				if _, ok := instructionAt(positions, labels[label]); !ok {
					return nil, ErrInvalidBranch
				}
				table[i] = labels[label]
			}
		}
		located = append(located, LocatedInstruction{Instruction: *ins, PC: pc})
		located[len(located)-1].Instruction.Imm = imm
		if ins.Op == OpJumpTable {
			located[len(located)-1].Instruction.Table = table
		}
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

	// The pass decides and validates the whole rewrite from the current
	// stream before it rewrites the stream in place, so failed validation
	// leaves the stream unchanged.
	code := stream.instructions
	positions := workResetTracked(&work.positions, &work.history.positions, len(code)+1)
	for i := range code {
		size := stream.sizes[i]
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
	destinations, err := threadedLabels(code, labelIndex, bound, work)
	if err != nil {
		return nil, err
	}
	// target holds each branch's label once threaded (and folded below);
	// inverted marks the conditional branches a fold inverts. changed
	// records whether the pass rewrites the stream at all. Jump tables
	// always receive fresh, stream-owned tables.
	target := workResetTracked(&work.branchLabels, &work.history.branchLabels, len(code))
	inverted := workResetTracked(&work.inverted, &work.history.inverted, len(code))
	changed := false
	for i := range code {
		ins := &code[i]
		if isBranch(ins.Op) {
			target[i] = destinations[int(ins.Imm)]
			changed = changed || uint64(target[i]) != ins.Imm
		}
		if ins.Op == OpJumpTable {
			changed = true
			for _, label := range ins.Table {
				if label < 0 || label >= len(bound) || !bound[label] {
					return nil, ErrInvalidBranch
				}
			}
		}
	}
	// tableLabel is a table entry's label once threaded.
	tableLabel := func(ins *Instruction, label int) int {
		if ins.Op == OpJumpTable {
			return destinations[label]
		}
		return label
	}
	remove := workResetTracked(&work.remove, &work.history.remove, len(code))
	referenced := workResetTracked(&work.referenced, &work.history.referenced, len(code)+1)
	for i := range code {
		ins := &code[i]
		if isBranch(ins.Op) {
			referenced[labelIndex[target[i]]] = true
		}
		for _, label := range ins.Table {
			referenced[labelIndex[tableLabel(ins, label)]] = true
		}
	}
	// Jump-only edge labels have already been redirected. Fold
	// "if condition: fallthrough; jump target" into the inverse conditional
	// when the skipped jump is not an independently reachable instruction.
	for i := 0; i+1 < len(code); i++ {
		if code[i].Op == OpJumpIf && code[i+1].Op == OpJump &&
			labelIndex[target[i]] == i+2 && !referenced[i+1] {
			inverted[i] = true
			target[i] = target[i+1]
			remove[i+1] = true
			changed = true
		}
	}
	for i := range code {
		if code[i].Op == OpJump && labelIndex[target[i]] == i+1 {
			remove[i] = true
			changed = true
		}
	}
	reachable := workResetTracked(&work.reachable, &work.history.reachable, len(code))
	pending := append(workAppendTracked(&work.pending, &work.history.pending, len(code)), 0)
	defer func() { work.pending = pending }()
	for len(pending) != 0 {
		index := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if index < 0 || index >= len(code) {
			return nil, ErrInvalidBranch
		}
		if reachable[index] {
			continue
		}
		reachable[index] = true
		ins := &code[index]
		if remove[index] {
			pending = append(pending, index+1)
			continue
		}
		switch ins.Op {
		case OpJump:
			pending = append(pending, labelIndex[target[index]])
		case OpJumpIf, OpBranchZero, OpBranchBit, OpLoop:
			pending = append(pending, labelIndex[target[index]], index+1)
		case OpJumpTable:
			for _, label := range ins.Table {
				pending = append(pending, labelIndex[destinations[label]])
			}
		case OpReturn, OpTailCall:
		default:
			pending = append(pending, index+1)
		}
	}
	if !changed {
		for i := range code {
			if !reachable[i] {
				changed = true
				break
			}
		}
	}
	if !changed {
		// Every instruction and label keeps its position; the stream
		// already holds this layout.
		return labels, nil
	}
	oldToNew := workResetTracked(&work.oldToNew, &work.history.oldToNew, len(code)+1)
	kept := 0
	for i := range code {
		oldToNew[i] = kept
		if reachable[i] && !remove[i] {
			kept++
		}
	}
	oldToNew[len(code)] = kept
	newPositions := workResetTracked(&work.newPositions, &work.history.newPositions, kept+1)
	// Nothing below fails. Each kept instruction moves, with its size and
	// marks, to an index no greater than its own, after it has been read.
	for i := range code {
		if !reachable[i] || remove[i] {
			continue
		}
		ins := &code[i]
		size, mark := stream.sizes[i], stream.marks[i]
		// Threading changes label operands and inversion changes conditions.
		// A public Size or flag callback may depend on either, so remeasure all
		// changed branches, and the flags of jump tables, whose entries change,
		// while retaining the validated results of unchanged operations.
		if isBranch(ins.Op) {
			condition := ins.Condition
			if inverted[i] {
				condition ^= 1
			}
			if uint64(target[i]) != ins.Imm || condition != ins.Condition {
				ins.Imm, ins.Condition = uint64(target[i]), condition
				size, mark = stream.Size(*ins), stream.mark(*ins)
			}
		}
		if ins.Op == OpJumpTable {
			table := make([]int, len(ins.Table))
			for k, label := range ins.Table {
				table[k] = destinations[label]
			}
			ins.Table = table
			mark = stream.mark(*ins)
		}
		index := oldToNew[i]
		newPositions[index+1] = newPositions[index] + size
		if index != i {
			code[index] = *ins
		}
		stream.sizes[index], stream.marks[index] = size, mark
	}
	// Commit the stream before updating caller-owned labels.
	stream.truncateLayout(kept, newPositions[kept])
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
	// Relaxation only shortens branches, so it rewrites the stream in place
	// once every candidate's label is known to be valid.
	insns := stream.instructions
	for i := range insns {
		if ins := &insns[i]; isLimitedBranch(ins.Op) && ins.Long && (ins.Imm >= uint64(len(labels)) || !bound[int(ins.Imm)]) {
			return nil, ErrInvalidBranch
		}
	}
	positions := workResetTracked(&work.positions, &work.history.positions, len(insns)+1)
	sizes := workResetTracked(&work.sizes, &work.history.sizes, len(insns))
	copy(sizes, stream.sizes)
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
	shortened := false
	for {
		for i := range insns {
			positions[i+1] = positions[i] + sizes[i]
		}
		changed := false
		for i := range insns {
			ins := &insns[i]
			if !isLimitedBranch(ins.Op) || !ins.Long {
				continue
			}
			short := *ins
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
		shortened = true
	}
	if shortened {
		// Every change shrinks a size, so a changed size marks a rewritten
		// branch; only those are measured for their flag effects again.
		stream.instructionHistory.Observe(len(insns))
		stream.sizeHistory.Observe(len(sizes))
		stream.markHistory.Observe(len(insns))
		for i, size := range sizes {
			if size == stream.sizes[i] {
				continue
			}
			if stream.marks[i]&flagWriter != 0 {
				stream.flagWriters--
			}
			mark := stream.mark(insns[i])
			if mark&flagWriter != 0 {
				stream.flagWriters++
			}
			stream.marks[i] = mark
			stream.sizes[i] = size
		}
		stream.pc = positions[len(insns)]
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
	// Layout passes rewrite the stream in place, and it may alias an expired
	// result or an owned Replace input.
	s.instructions = slices.Clone(s.instructions)
	if borrowed {
		for i := range previous[:cap(previous)] {
			previous[:cap(previous)][i] = bad
		}
	}

	for i := range s.layout.located[:cap(s.layout.located)] {
		s.layout.located[:cap(s.layout.located)][i] = LocatedInstruction{Instruction: bad, PC: -0x5a5a}
	}
	s.layout.located = poisonReplacement(s.layout.located)
}
