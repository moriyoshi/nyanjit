package machine

import "github.com/moriyoshi/nyanjit/ir"

// FusePPC64Loops selects CTR for natural countdown loops whose counter has no
// other use in the body. DEC's flags must be dead on both outgoing paths.
// Every external entry initializes CTR; the backedge targets the body after
// that initialization. On the fallthrough edge the original GPR receives zero.
// Calls, side entries, early exits and overlapping CTR loops keep their original
// form. The pass runs after compaction and before branch relaxation.
func FusePPC64Loops(stream *Stream, labels []int, bound []bool) ([]int, []bool, error) {
	if stream == nil || len(labels) != len(bound) {
		return nil, nil, ErrInvalidBranch
	}
	old := append([]Instruction(nil), stream.Instructions()...)
	positions := make([]int, len(old)+1)
	indexAt := make(map[int]int, len(old)+1)
	for i, ins := range old {
		positions[i+1] = positions[i] + stream.Size(ins)
		if _, exists := indexAt[positions[i]]; !exists {
			indexAt[positions[i]] = i
		}
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
			return nil, nil, ErrInvalidBranch
		}
		labelIndex[label] = index
		protected[index] = true
	}
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
	// Carry can remain live across a CR-only writer. Preserve the individual
	// masks instead of treating every flag-writing instruction as a kill.
	live := make([]FlagMask, len(old)+1)
	for changed := true; changed; {
		changed = false
		for i := len(old) - 1; i >= 0; i-- {
			next, err := successors(i, old[i])
			if err != nil {
				return nil, nil, err
			}
			var needed FlagMask
			for _, index := range next {
				if index < 0 || index > len(old) {
					return nil, nil, ErrInvalidBranch
				}
				needed |= live[index]
			}
			needed = ppcFlagReads(old[i]) | (needed &^ stream.ModifiedFlags(old[i]))
			if needed != live[i] {
				live[i], changed = needed, true
			}
		}
	}
	type loop struct {
		header, latch, branch, compare, label int
		counter                               Register
	}
	var loops []loop
	occupied := make([]bool, len(old))
	for branchIndex, branch := range old {
		if branch.Op != OpJumpIf || branch.Condition != ir.BranchNotEqual ||
			branch.Imm >= uint64(len(labels)) || protected[branchIndex] {
			continue
		}
		header := labelIndex[int(branch.Imm)]
		if header >= branchIndex || occupied[branchIndex] {
			continue
		}
		latch, compare := branchIndex-1, -1
		if old[latch].Op == OpCompareImmediate && old[latch].Imm == 0 && old[latch].Bits == 64 {
			compare = latch
			counter := old[compare].A
			for latch--; latch >= header; latch-- {
				if touchesPPCRegister(old[latch], counter) {
					break
				}
			}
		}
		if latch < header || old[latch].Op != OpDec || old[latch].A != old[latch].B ||
			compare >= 0 && old[compare].A != old[latch].A {
			continue
		}
		dec := old[latch]
		if compare < 0 && latch != branchIndex-1 {
			continue
		}
		if live[header] != 0 || live[branchIndex+1] != 0 {
			continue
		}
		valid := true
		affected := stream.ModifiedFlags(dec)
		for j := latch + 1; j < branchIndex; j++ {
			if ppcFlagReads(old[j])&affected != 0 {
				valid = false
			}
			affected &^= stream.ModifiedFlags(old[j])
		}
		for j, ins := range old {
			inside := j >= header && j < branchIndex
			if occupied[j] && inside {
				valid = false
			}
			if inside && j != latch && j != compare &&
				(touchesPPCRegister(ins, dec.A) || usesPPCCount(ins) || ins.Op == OpReturn) {
				valid = false
			}
			if !isBranch(ins.Op) {
				continue
			}
			target := labelIndex[int(ins.Imm)]
			if j != branchIndex && j >= header && j <= branchIndex && target == header {
				valid = false
			}
			if !inside && j != branchIndex && target > header && target <= branchIndex {
				valid = false
			}
			if inside && (target < header || target >= latch) {
				valid = false
			}
		}
		if !valid {
			continue
		}
		loops = append(loops, loop{header: header, latch: latch, branch: branchIndex, compare: compare, counter: dec.A})
		for j := header; j <= branchIndex; j++ {
			occupied[j] = true
		}
	}

	if len(loops) == 0 {
		return labels, bound, nil
	}
	outLabels := append([]int(nil), labels...)
	outBound := append([]bool(nil), bound...)
	headers := make(map[int]loop)
	branches := make(map[int]loop)
	remove := make(map[int]bool)
	for _, candidate := range loops {
		candidate.label = len(outLabels)
		outLabels = append(outLabels, 0)
		outBound = append(outBound, true)
		headers[candidate.header] = candidate
		branches[candidate.branch] = candidate
		remove[candidate.latch] = true
		if candidate.compare >= 0 {
			remove[candidate.compare] = true
		}
	}
	newPC := 0
	newPositions := make([]int, len(old)+1)
	var kept []Instruction
	appendInsn := func(ins Instruction) {
		kept = append(kept, ins)
		newPC += stream.Size(ins)
	}
	for i := 0; i < len(old); i++ {
		newPositions[i] = newPC
		if candidate, ok := headers[i]; ok {
			appendInsn(Instruction{Op: OpSetCount, A: candidate.counter, Bits: 64})
			outLabels[candidate.label] = newPC
		}
		if candidate, ok := branches[i]; ok {
			appendInsn(Instruction{Op: OpLoop, Bits: 64, Imm: uint64(candidate.label), Long: true})
			appendInsn(Instruction{Op: OpConst, A: candidate.counter, Imm: 0})
			continue
		}
		if !remove[i] {
			appendInsn(old[i])
		}
	}
	newPositions[len(old)] = newPC
	for label, index := range labelIndex {
		if bound[label] {
			outLabels[label] = newPositions[index]
		}
	}
	stream.Replace(kept)
	return outLabels, outBound, nil
}

func ppcFlagReads(ins Instruction) FlagMask {
	if (ins.Op == OpWordCarry || ins.Op == OpCarryBit) && ins.CarryMode == CarryFlags {
		return FlagCarry
	}
	if ins.Op != OpJumpIf && ins.Op != OpSelectFlags {
		return 0
	}
	switch ins.Condition {
	case ir.BranchEqual, ir.BranchNotEqual:
		return FlagZero
	case BranchSign, BranchNotSign:
		return FlagSign
	case BranchCarry, BranchNotCarry, BranchCarry32, BranchNotCarry32:
		return FlagCarry
	default:
		return FlagSign | FlagZero
	}
}

func usesPPCCount(ins Instruction) bool {
	return ins.Op == OpSetCount || ins.Op == OpLoop || ins.Op == OpCallC || ins.Op == OpCallGo
}

// Operand roles are conservative: unknown operations can neither free a
// counter register nor establish that CTR survives the loop body.
func touchesPPCRegister(ins Instruction, reg Register) bool {
	switch ins.Op {
	case OpConst, OpSymbol, OpReload, OpLoadIncoming:
		return ins.A == reg
	case OpMove, OpInc, OpDec, OpNarrow, OpSignExtend, OpShift, OpLoad64, OpLoad8, OpLoadOffset, OpFloatUnary:
		return ins.A == reg || ins.B == reg
	case OpCarryBit, OpMulHigh, OpShiftBy, OpBinary, OpBinaryCarry, OpScaled, OpLoadAddress, OpStoreAddress, OpFloatBinary:
		return ins.A == reg || ins.B == reg || ins.C == reg
	case OpBinaryImmediate:
		return ins.A == reg || ins.B == reg
	case OpWordCarry:
		return ins.A == reg || ins.B == reg || ins.C == reg || ins.D == reg || ins.E == reg
	case OpMulAdd, OpSelect, OpSelectFlags, OpFloatFMA:
		return ins.A == reg || ins.B == reg || ins.C == reg || ins.D == reg
	case OpStore64, OpStore8, OpStoreOffset:
		return ins.A == reg || ins.B == reg
	case OpSpill, OpSaveCFloat, OpRestoreCFloat, OpSaveCVector, OpRestoreCVector, OpSetCount:
		return ins.A == reg
	case OpCompareImmediate, OpBranchZero, OpBranchBit:
		return ins.A == reg
	case OpCompareRegister:
		return ins.A == reg || ins.B == reg
	case OpJump, OpJumpIf, OpFrameEnter, OpFrameLeave, OpSaveLink, OpRestoreLink, OpSaveCondition, OpRestoreCondition:
		return false
	default:
		return true
	}
}
