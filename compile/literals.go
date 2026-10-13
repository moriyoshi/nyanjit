package compile

import (
	"encoding/binary"
	"fmt"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/regalloc"
)

// Compact existing long branches once, then jointly grow literal loads and
// branches. Every remaining change increases a form's size, so the loop
// terminates and no later growth can invalidate a previously checked load.
func relaxControlLayout(stream *machine.Stream, labels []int, bound []bool, target machineTarget) ([]int, error) {
	labels, err := machine.RelaxBranches(stream, labels, bound, target.BranchFits)
	if err != nil {
		return nil, fmt.Errorf("relax control branches: %w: %w", ErrInvalidFunction, err)
	}
	if target.Architecture() != "arm64" {
		return labels, nil
	}
	for {
		before := stream.PC()
		labels, err = relaxLiteralStream(stream, labels, bound, "arm64")
		if err != nil {
			return nil, err
		}
		labels, err = growControlBranches(stream, labels, bound, target.BranchFits)
		if err != nil {
			return nil, err
		}
		if stream.PC() == before {
			return labels, nil
		}
	}
}

// growControlBranches preserves label indices while promoting branches whose
// short forms no longer reach. It deliberately does not shrink again: literal
// growth can move a target outside a short branch's range on the next round.
func growControlBranches(stream *machine.Stream, labels []int, bound []bool,
	fits func(machine.Instruction, int, int, int) bool) ([]int, error) {
	insns := append([]machine.Instruction(nil), stream.Instructions()...)
	positions := make([]int, len(insns)+1)
	indices := make(map[int]int, len(insns)+1)
	for n, i := range insns {
		positions[n+1] = positions[n] + stream.Size(i)
		if _, ok := indices[positions[n]]; !ok {
			indices[positions[n]] = n
		}
	}
	indices[positions[len(insns)]] = len(insns)
	labelIndices := make([]int, len(labels))
	for n, pc := range labels {
		if bound[n] {
			id, ok := indices[pc]
			if !ok {
				return nil, machine.ErrInvalidBranch
			}
			labelIndices[n] = id
		}
	}
	changed := false
	for n, i := range insns {
		switch i.Op {
		case machine.OpJump, machine.OpJumpIf, machine.OpBranchZero, machine.OpBranchBit, machine.OpLoop:
		default:
			continue
		}
		if i.Imm >= uint64(len(labels)) || !bound[int(i.Imm)] {
			return nil, machine.ErrInvalidBranch
		}
		size := stream.Size(i)
		if fits(i, positions[n], labels[int(i.Imm)], size) {
			continue
		}
		larger := i
		if !i.Long {
			larger.Long = true
		} else if !i.Far {
			larger.Far = true
		}
		if stream.Size(larger) <= size {
			return nil, fmt.Errorf("arm64 branch at %#x cannot reach %#x: %w", positions[n], labels[int(i.Imm)], ErrBackendLimit)
		}
		insns[n] = larger
		changed = true
	}
	if !changed {
		return labels, nil
	}
	stream.Replace(insns)
	for n, i := range insns {
		positions[n+1] = positions[n] + stream.Size(i)
	}
	for n := range labels {
		if bound[n] {
			labels[n] = positions[labelIndices[n]]
		}
	}
	return labels, nil
}

// relaxLiteralStream grows ARM literal loads before branch relaxation. Labels
// follow instruction indices while the pool and code sizes reach a fixed point.
func relaxLiteralStream(stream *machine.Stream, labels []int, bound []bool, arch string) ([]int, error) {
	if arch != "arm64" {
		return labels, nil
	}
	insns := append([]machine.Instruction(nil), stream.Instructions()...)
	oldPC := 0
	indices := make(map[int]int, len(insns)+1)
	for n, i := range insns {
		if _, ok := indices[oldPC]; !ok {
			indices[oldPC] = n
		}
		oldPC += stream.Size(i)
	}
	indices[oldPC] = len(insns)
	labelIndices := make([]int, len(labels))
	for n, pc := range labels {
		if !bound[n] {
			continue
		}
		id, ok := indices[pc]
		if !ok {
			return nil, machine.ErrInvalidBranch
		}
		labelIndices[n] = id
	}
	poolOffsets := map[string]int{}
	poolBytes := 0
	for _, i := range insns {
		if i.Op != machine.OpLiteralLoad {
			continue
		}
		key := string(i.Data)
		if _, ok := poolOffsets[key]; !ok {
			offset := (poolBytes + 15) &^ 15
			poolOffsets[key] = offset
			poolBytes = offset + len(i.Data)
		}
	}
	positions := make([]int, len(insns)+1)
	for {
		for n, i := range insns {
			positions[n+1] = positions[n] + stream.Size(i)
		}
		base := (positions[len(insns)] + 15) &^ 15
		changed := false
		for n := range insns {
			i := &insns[n]
			if i.Op != machine.OpLiteralLoad {
				continue
			}
			delta := int64(base+poolOffsets[string(i.Data)]) - int64(positions[n])
			if delta < -(1<<31) || delta >= 1<<31 {
				return nil, fmt.Errorf("arm64 literal pool exceeds signed 32-bit reach: %w", ErrBackendLimit)
			}
			if !i.Long && (delta < -(1<<20) || delta >= 1<<20) {
				i.Long = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	stream.Replace(insns)
	for n := range labels {
		if bound[n] {
			labels[n] = positions[labelIndices[n]]
		}
	}
	return labels, nil
}

func relaxFunctionLiterals(program *physicalProgram, emitter machineEmitter, arch string) error {
	if arch != "arm64" {
		return nil
	}
	stream := machine.NewStream(machine.StreamOptions{Size: emitter.instructionSize, FlagEffect: func(machine.Instruction) bool { return false }})
	for _, i := range program.insns {
		stream.Append(i.ins)
	}
	if _, err := relaxLiteralStream(stream, nil, nil, arch); err != nil {
		return err
	}
	program.insns = program.insns[:0]
	program.codeBytes = 0
	for _, i := range stream.Instructions() {
		program.insns = append(program.insns, locatedInsn{pc: program.codeBytes, ins: i})
		program.codeBytes += stream.Size(i)
	}
	return nil
}

// finishLiterals places immutable, deduplicated 16-byte-aligned entries after
// the last return, then resolves each load against the final physical PCs.
func finishLiterals(program *physicalProgram) {
	entries := map[string]int{}
	var data []byte
	base := (program.codeBytes + 15) &^ 15
	for index := range program.insns {
		ins := &program.insns[index].ins
		if ins.Op != machine.OpLiteralLoad {
			continue
		}
		key := string(ins.Data)
		offset, ok := entries[key]
		if !ok {
			offset = (len(data) + 15) &^ 15
			data = append(data, make([]byte, offset-len(data))...)
			data = append(data, ins.Data...)
			entries[key] = offset
		}
		ins.Disp = uint64(base + offset)
	}
	if len(data) == 0 {
		return
	}
	data = append(data, make([]byte, ((len(data)+15)&^15)-len(data))...)
	pad := base - program.codeBytes
	body := append(make([]byte, pad), data...)
	program.insns = append(program.insns, locatedInsn{pc: program.codeBytes, ins: physicalInsn{Op: machine.OpLiteralData, Data: body}})
	program.codeBytes += len(body)
}
func literalBytes(bits uint8, value uint64, order ir.ByteOrder) []byte {
	var endian binary.ByteOrder = binary.LittleEndian
	if order == ir.ByteOrderBigEndian {
		endian = binary.BigEndian
	}
	if bits == 32 {
		out := make([]byte, 4)
		endian.PutUint32(out, uint32(value))
		return out
	}
	out := make([]byte, 8)
	endian.PutUint64(out, value)
	return out
}
func (e *physicalEmitter) literal(dst physReg, bits uint8, data []byte) {
	e.hasLiteral = true
	e.append(physicalInsn{Op: machine.OpLiteralLoad, A: dst, Bits: bits, Data: append([]byte(nil), data...)})
}

// foldFloatLiterals removes GPR-only constant materialization when an FP
// consumer can load the bit pattern directly from its code object's pool.
func foldFloatLiterals(program machineProgram) machineProgram {
	program.Instructions = append([]machineInstruction(nil), program.Instructions...)
	for n := range program.Instructions {
		i := &program.Instructions[n]
		switch i.Op {
		case mFloatBinary, mFloatUnary, mFloatFMA:
			ids := []int{i.A}
			if i.Op != mFloatUnary {
				ids = append(ids, i.B)
			}
			if i.Op == mFloatFMA {
				ids = append(ids, i.C)
			}
			for j, id := range ids {
				if program.Values[id].Class != regalloc.Float && program.Values[id].Constant && program.Values[id].Symbol == "" {
					i.LiteralMask |= 1 << j
				}
			}
		}
	}
	uses := make([]int, len(program.Values))
	for _, i := range program.Instructions {
		for _, id := range i.Inputs() {
			uses[id]++
		}
	}
	uses[program.Result]++
	for _, id := range program.Exports {
		uses[id]++
	}
	for _, id := range program.ResultParts {
		uses[id]++
	}
	out := program.Instructions[:0]
	boundary := make([]int, len(program.Instructions)+1)
	for pos, i := range program.Instructions {
		boundary[pos] = len(out)
		if i.Op == mConst && uses[i.Dst] == 0 {
			continue
		}
		out = append(out, i)
	}
	boundary[len(program.Instructions)] = len(out)
	program.Scopes = append([]machine.SelectedScope(nil), program.Scopes...)
	for n := range program.Scopes {
		s := &program.Scopes[n]
		s.Start, s.End = boundary[s.Start], boundary[s.End]
	}
	program.Instructions = out
	return program
}
