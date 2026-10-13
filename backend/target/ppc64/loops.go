package ppc64

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// fuseLoops selects CTR for natural countdown loops whose counter has no
// other use in the body. DEC's flags must be dead on both outgoing paths.
// Every external entry initializes CTR; the backedge targets the body after
// that initialization. On the fallthrough edge the original GPR receives zero.
// Calls, side entries, early exits and overlapping CTR loops keep their original
// form. The pass runs after compaction and before branch relaxation.
func fuseLoops(stream *machine.Stream, labels []int, bound []bool) ([]int, []bool, error) {
	if stream == nil || len(labels) != len(bound) {
		return nil, nil, machine.ErrInvalidBranch
	}
	old := append([]machine.Instruction(nil), stream.Instructions()...)
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
			return nil, nil, machine.ErrInvalidBranch
		}
		labelIndex[label] = index
		protected[index] = true
	}
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
	// Carry can remain live across a CR-only writer. Preserve the individual
	// masks instead of treating every flag-writing instruction as a kill.
	live := make([]machine.FlagMask, len(old)+1)
	for changed := true; changed; {
		changed = false
		for i := len(old) - 1; i >= 0; i-- {
			next, err := successors(i, old[i])
			if err != nil {
				return nil, nil, err
			}
			var needed machine.FlagMask
			for _, index := range next {
				if index < 0 || index > len(old) {
					return nil, nil, machine.ErrInvalidBranch
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
		counter                               backend.Register
	}
	var loops []loop
	occupied := make([]bool, len(old))
	for branchIndex, branch := range old {
		if branch.Op != machine.OpJumpIf || branch.Condition != op.BranchNotEqual ||
			branch.Imm >= uint64(len(labels)) || protected[branchIndex] {
			continue
		}
		header := labelIndex[int(branch.Imm)]
		if header >= branchIndex || occupied[branchIndex] {
			continue
		}
		latch, compare := branchIndex-1, -1
		if old[latch].Op == machine.OpCompareImmediate && old[latch].Imm == 0 && old[latch].Bits == 64 {
			compare = latch
			counter := old[compare].A
			for latch--; latch >= header; latch-- {
				if touchesPPCRegister(old[latch], counter) {
					break
				}
			}
		}
		if latch < header || old[latch].Op != machine.OpDec || old[latch].A != old[latch].B ||
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
				(touchesPPCRegister(ins, dec.A) || usesPPCCount(ins) || ins.Op == machine.OpReturn) {
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
	var kept []machine.Instruction
	appendInsn := func(ins machine.Instruction) {
		kept = append(kept, ins)
		newPC += stream.Size(ins)
	}
	for i := 0; i < len(old); i++ {
		newPositions[i] = newPC
		if candidate, ok := headers[i]; ok {
			appendInsn(machine.Instruction{Op: machine.OpSetCount, A: candidate.counter, Bits: 64})
			outLabels[candidate.label] = newPC
		}
		if candidate, ok := branches[i]; ok {
			appendInsn(machine.Instruction{Op: machine.OpLoop, Bits: 64, Imm: uint64(candidate.label), Long: true})
			appendInsn(machine.Instruction{Op: machine.OpConst, A: candidate.counter, Imm: 0})
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

func ppcFlagReads(ins machine.Instruction) machine.FlagMask {
	if (ins.Op == machine.OpWordCarry || ins.Op == machine.OpCarryBit) && ins.CarryMode == machine.CarryFlags {
		return machine.FlagCarry
	}
	if ins.Op != machine.OpJumpIf && ins.Op != machine.OpSelectFlags {
		return 0
	}
	switch ins.Condition {
	case op.BranchEqual, op.BranchNotEqual:
		return machine.FlagZero
	case machine.BranchSign, machine.BranchNotSign:
		return machine.FlagSign
	case machine.BranchCarry, machine.BranchNotCarry, machine.BranchCarry32, machine.BranchNotCarry32:
		return machine.FlagCarry
	default:
		return machine.FlagSign | machine.FlagZero
	}
}

func usesPPCCount(ins machine.Instruction) bool {
	return ins.Op == machine.OpSetCount || ins.Op == machine.OpLoop || ins.Op == machine.OpCallC || ins.Op == machine.OpCallGo
}

// Operand roles are conservative: unknown operations can neither free a
// counter register nor establish that CTR survives the loop body.
func touchesPPCRegister(ins machine.Instruction, reg backend.Register) bool {
	switch ins.Op {
	case machine.OpConst, machine.OpSymbol, machine.OpReload, machine.OpLoadIncoming:
		return ins.A == reg
	case machine.OpMove, machine.OpInc, machine.OpDec, machine.OpNarrow, machine.OpSignExtend, machine.OpShift, machine.OpLoad64, machine.OpLoad8, machine.OpLoad16, machine.OpLoad32, machine.OpLoadOffset, machine.OpFloatUnary,
		machine.OpIntToFloat, machine.OpFloatToInt:
		return ins.A == reg || ins.B == reg
	case machine.OpCarryBit, machine.OpMulHigh, machine.OpMulHighSigned, machine.OpShiftBy, machine.OpBinary, machine.OpBinaryCarry, machine.OpScaled, machine.OpLoadAddress, machine.OpStoreAddress, machine.OpFloatBinary, machine.OpDivide,
		machine.OpFloatCompare:
		return ins.A == reg || ins.B == reg || ins.C == reg
	case machine.OpBinaryImmediate:
		return ins.A == reg || ins.B == reg
	case machine.OpWordCarry:
		return ins.A == reg || ins.B == reg || ins.C == reg || ins.D == reg || ins.E == reg
	case machine.OpMulAdd, machine.OpSelect, machine.OpSelectFlags, machine.OpFloatFMA:
		return ins.A == reg || ins.B == reg || ins.C == reg || ins.D == reg
	case machine.OpStore64, machine.OpStore8, machine.OpStore16, machine.OpStore32, machine.OpStoreOffset:
		return ins.A == reg || ins.B == reg
	case machine.OpSpill, machine.OpSaveCFloat, machine.OpRestoreCFloat, machine.OpSaveCVector, machine.OpRestoreCVector, machine.OpSetCount:
		return ins.A == reg
	case machine.OpCompareImmediate, machine.OpBranchZero, machine.OpBranchBit:
		return ins.A == reg
	case machine.OpCompareRegister:
		return ins.A == reg || ins.B == reg
	case machine.OpJump, machine.OpJumpIf, machine.OpFrameEnter, machine.OpFrameLeave, machine.OpSaveLink, machine.OpRestoreLink, machine.OpSaveCondition, machine.OpRestoreCondition:
		return false
	default:
		return true
	}
}

// FuseLoops implements machine.LoopFuser with CTR loops.
func (Policy) FuseLoops(s *machine.Stream, labels []int, bound []bool) ([]int, []bool, error) {
	labels, bound, err := fuseLoops(s, labels, bound)
	if err != nil {
		return nil, nil, fmt.Errorf("select CTR loops: %w", err)
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
