package compile

import (
	"encoding/binary"
	"fmt"
	"math"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
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
	insns := stream.Instructions()
	layout := stream.Layout()
	labelIndices := make([]int, len(labels))
	for n, pc := range labels {
		if bound[n] {
			id, ok := layout.InstructionAt(pc)
			if !ok {
				return nil, machine.ErrInvalidBranch
			}
			labelIndices[n] = id
		}
	}
	// Every decision reads the layout before growth; nothing is rewritten
	// until all of them succeed.
	var grown []int
	var wider []machine.Instruction
	for n, i := range insns {
		switch i.Op {
		case machine.OpJump, machine.OpJumpIf, machine.OpBranchZero, machine.OpBranchBit, machine.OpLoop:
		default:
			continue
		}
		if i.Imm >= uint64(len(labels)) || !bound[int(i.Imm)] {
			return nil, machine.ErrInvalidBranch
		}
		size := stream.SizeAt(n)
		if fits(i, layout.PC(n), labels[int(i.Imm)], size) {
			continue
		}
		larger := i
		if !i.Long {
			larger.Long = true
		} else if !i.Far {
			larger.Far = true
		}
		if stream.Size(larger) <= size {
			return nil, &LimitError{Kind: LimitBranchRange, Needed: distance(layout.PC(n), labels[int(i.Imm)]), Arch: "arm64",
				Detail: fmt.Sprintf("branch at %#x to %#x", layout.PC(n), labels[int(i.Imm)])}
		}
		grown, wider = append(grown, n), append(wider, larger)
	}
	if grown == nil {
		return labels, nil
	}
	for k, n := range grown {
		stream.ReplaceAt(n, wider[k])
	}
	layout = stream.Layout()
	for n := range labels {
		if bound[n] {
			labels[n] = layout.PC(labelIndices[n])
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
	hasLiteral := false
	for _, ins := range stream.Instructions() {
		hasLiteral = hasLiteral || ins.Op == machine.OpLiteralLoad
	}
	if !hasLiteral {
		return labels, nil
	}
	original := stream.Instructions()
	var layout machine.Layout
	if len(labels) != 0 {
		layout = stream.Layout()
	}
	positions := make([]int, len(original)+1)
	labelIndices := make([]int, len(labels))
	for n, pc := range labels {
		if !bound[n] {
			continue
		}
		id, ok := layout.InstructionAt(pc)
		if !ok {
			return nil, machine.ErrInvalidBranch
		}
		labelIndices[n] = id
	}
	poolOffsets := map[string]int{}
	poolBytes := 0
	var loads []int
	for n, i := range original {
		if i.Op != machine.OpLiteralLoad {
			continue
		}
		loads = append(loads, n)
		if _, ok := poolOffsets[string(i.Data)]; !ok {
			offset := (poolBytes + 15) &^ 15
			poolOffsets[string(i.Data)] = offset
			poolBytes = offset + len(i.Data)
		}
	}
	// longSize holds the size of each load promoted to the long form, or
	// zero; promotions are installed once the layout settles.
	longSize := make([]int, len(loads))
	promoted := false
	for {
		next := 0
		for n := range original {
			// The stream already validated and measured each original
			// instruction. Literal promotion is the only rewrite.
			size := stream.SizeAt(n)
			if next < len(loads) && loads[next] == n {
				if longSize[next] != 0 {
					size = longSize[next]
				}
				next++
			}
			positions[n+1] = positions[n] + size
		}
		base := (positions[len(original)] + 15) &^ 15
		changed := false
		for k, n := range loads {
			i := original[n]
			delta := int64(base+poolOffsets[string(i.Data)]) - int64(positions[n])
			if delta < -(1<<31) || delta >= 1<<31 {
				return nil, &LimitError{Kind: LimitBranchRange, Needed: int(min(max(delta, -delta), math.MaxInt)), Limit: 1<<31 - 1, Arch: "arm64",
					Detail: fmt.Sprintf("literal pool load at %#x", positions[n])}
			}
			if !i.Long && longSize[k] == 0 && (delta < -(1<<20) || delta >= 1<<20) {
				i.Long = true
				longSize[k] = stream.Size(i)
				changed, promoted = true, true
			}
		}
		if !changed {
			break
		}
	}
	if !promoted {
		return labels, nil
	}
	for k, n := range loads {
		if longSize[k] != 0 {
			i := original[n]
			i.Long = true
			stream.ReplaceAt(n, i)
		}
	}
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
	hasLiteral := false
	for _, located := range program.insns {
		hasLiteral = hasLiteral || located.ins.Op == machine.OpLiteralLoad
	}
	if !hasLiteral {
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
	for n, i := range stream.Instructions() {
		program.insns = append(program.insns, locatedInsn{pc: program.codeBytes, ins: i})
		program.codeBytes += stream.SizeAt(n)
	}
	return nil
}

// finishOrMeasureLiterals returns the code size including the literal pool.
// Assembling kinds finish the pool now; unit layouts only measure it and
// leave finishing to the first listing or encoding.
func finishOrMeasureLiterals(program *physicalProgram, kind emissionKind) int {
	if kind.layout() {
		program.literalsPending = true
		return measureLiteralBytes(*program)
	}
	finishLiterals(program)
	return program.codeBytes
}

// measureLiteralBytes accounts for the final pool without copying payloads,
// patching literal operands, or appending data instructions.
func measureLiteralBytes(program physicalProgram) int {
	entries := map[string]bool{}
	bytes := 0
	for _, located := range program.insns {
		i := located.ins
		if i.Op != machine.OpLiteralLoad {
			continue
		}
		// Literal payloads never change once selected, so this local map
		// can key on them without copying.
		key := unsafe.String(unsafe.SliceData(i.Data), len(i.Data))
		if entries[key] {
			continue
		}
		entries[key] = true
		bytes = (bytes + 15) &^ 15
		bytes += len(i.Data)
	}
	if bytes == 0 {
		return program.codeBytes
	}
	return ((program.codeBytes + 15) &^ 15) + ((bytes + 15) &^ 15)
}

// finishLiterals places immutable, deduplicated 16-byte-aligned entries after
// the last return, then resolves each load against the final physical PCs.
func finishLiterals(program *physicalProgram) {
	program.literalsPending = false
	entries := map[string]int{}
	var data []byte
	base := (program.codeBytes + 15) &^ 15
	for index := range program.insns {
		ins := &program.insns[index].ins
		if ins.Op != machine.OpLiteralLoad {
			continue
		}
		offset, ok := entries[string(ins.Data)]
		if !ok {
			offset = (len(data) + 15) &^ 15
			data = append(data, make([]byte, offset-len(data))...)
			data = append(data, ins.Data...)
			entries[string(ins.Data)] = offset
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

// literalBytes renders a literal in the little-endian layout shared by every
// supported target.
func literalBytes(bits uint8, value uint64) []byte {
	endian := binary.LittleEndian
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

// literalValue loads a scalar literal, rendering it straight into storage
// carved from the emitter's literal arena. Literal data is never modified.
func (e *physicalEmitter) literalValue(dst physReg, bits uint8, value uint64) {
	e.hasLiteral = true
	size := 8
	if bits == 32 {
		size = 4
	}
	data := carve(&e.literalArena, size)[:size]
	if bits == 32 {
		binary.LittleEndian.PutUint32(data, uint32(value))
	} else {
		binary.LittleEndian.PutUint64(data, value)
	}
	e.append(physicalInsn{Op: machine.OpLiteralLoad, A: dst, Bits: bits, Data: data})
}

// foldFloatLiterals removes GPR-only constant materialization when an FP
// consumer can load the bit pattern directly from its code object's pool.
func foldFloatLiterals(program machineProgram, temps *allocationTemps) machineProgram {
	needsOperands := false
	for n := range program.Instructions {
		i := &program.Instructions[n]
		if i.Op == mFloatBinary || i.Op == mFloatUnary || i.Op == mFloatFMA {
			needsOperands = true
			break
		}
	}
	if needsOperands {
		program.Instructions = append([]machineInstruction(nil), program.Instructions...)
		for n := range program.Instructions {
			i := &program.Instructions[n]
			switch i.Op {
			case mFloatBinary, mFloatUnary, mFloatFMA:
				ids := [3]int{i.A, i.B, i.C}
				count := 3
				if i.Op == mFloatUnary {
					count = 1
				} else if i.Op != mFloatFMA {
					count = 2
				}
				for j, id := range ids[:count] {
					if program.Values[id].Class != backend.Float && program.Values[id].Constant && program.Values[id].Symbol == "" {
						i.LiteralMask |= 1 << j
					}
				}
			}
		}
	}
	var operandBuffer [4]int
	uses := resetBufferTracked(&temps.literalUses, &temps.history.literalUses, len(program.Values))
	for n := range program.Instructions {
		i := &program.Instructions[n]
		for _, id := range i.InputsInto(operandBuffer[:0]) {
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
	dead := false
	for n := range program.Instructions {
		i := &program.Instructions[n]
		if i.Op == mConst && uses[i.Dst] == 0 {
			dead = true
			break
		}
	}
	if !dead {
		return program
	}
	if !needsOperands {
		program.Instructions = append([]machineInstruction(nil), program.Instructions...)
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
