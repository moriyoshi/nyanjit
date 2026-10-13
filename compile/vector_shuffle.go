package compile

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	targetamd64 "github.com/moriyoshi/nyanjit/backend/target/amd64"
	targetarm64 "github.com/moriyoshi/nyanjit/backend/target/arm64"
	targeti386 "github.com/moriyoshi/nyanjit/backend/target/i386"
	targetloong64 "github.com/moriyoshi/nyanjit/backend/target/loong64"
	"github.com/moriyoshi/nyanjit/ir"
)

// laneMove reports a shuffle or widening extend, which legalization lowers
// per 128-bit chunk or lane by lane (lanePermutation).
func laneMove(operation ir.Opcode) bool {
	return operation == ir.OpVShuffle || operation == ir.OpVExtendUnsigned || operation == ir.OpVExtendSigned
}

// shuffleImmediates packs the lane indices of a 128-bit machine shuffle as
// the OpVector form takes them: lane i's index is byte i of Imm, and byte
// i-8 of Disp from lane 8 on.
func shuffleImmediates(indices []byte) (imm, disp uint64) {
	for i, index := range indices {
		if i < 8 {
			imm |= uint64(index) << (8 * i)
		} else {
			disp |= uint64(index) << (8 * (i - 8))
		}
	}
	return imm, disp
}

// nativeLaneMove reports whether arch's assembler encodes the 128-bit
// shuffle (indices non-nil) or widening extend of bits-wide result lanes.
// SSE2 lacks a general byte shuffle, so its answer depends on the indices;
// asking the encoder keeps selection and encoding in agreement.
func nativeLaneMove(arch string, operation ir.Opcode, bits uint8, indices []byte, half uint64) bool {
	ins := machine.Instruction{Op: machine.OpVector, Arithmetic: operation, Bits: bits, A: backend.VectorRegister(3), B: backend.VectorRegister(4), C: backend.VectorRegister(5), Imm: half}
	if operation == ir.OpVShuffle {
		ins.Imm, ins.Disp = shuffleImmediates(indices)
	}
	switch arch {
	case "amd64":
		return targetamd64.VectorSize(ins, abi.StackPlatform, 0) > 0
	case "386":
		return targeti386.VectorSize(ins, abi.StackPlatform, 0) > 0
	case "arm64":
		return targetarm64.VectorSize(ins, abi.StackPlatform, 0) > 0
	case "loong64":
		return targetloong64.PhysicalSize(ins, abi.StackPlatform, 0) > 0
	}
	return false
}

// laneMoveLowering is the shape-only answer of VectorInfo.Lowering for a
// shuffle or extend of result type t: it holds for every index set, so a
// shuffle wider than 128 bits, which may cross chunks, is Scalarized.
func laneMoveLowering(arch string, operation ir.Opcode, t ir.Type) abi.VectorLowering {
	if operation == ir.OpVShuffle {
		if t.Bits() != 128 {
			return abi.Scalarized
		}
		// An SSE2 byte or halfword shuffle may need the missing PSHUFB.
		if (arch == "amd64" || arch == "386") && t.LaneBits < 32 {
			return abi.Scalarized
		}
		if arch == "amd64" || arch == "386" || arch == "arm64" || arch == "loong64" {
			return abi.Expanded
		}
		return abi.Scalarized
	}
	// A 128-bit result widens a 64-bit vector, which has no native piece.
	if t.Bits() < 256 || t.Bits()%128 != 0 || !nativeLaneMove(arch, operation, t.LaneBits, nil, 0) {
		return abi.Scalarized
	}
	return abi.Split
}

// lanePermutation lowers a shuffle or widening extend. With native vectors,
// each 128-bit chunk whose lanes come from at most two input chunks becomes
// one machine shuffle the target encodes, and each chunk of a widened
// vector extends half an input chunk; other chunks are built lane by lane.
// Without native vectors every lane is a scalar copy or extension.
func (l *vectorLegalizer) lanePermutation(id int, ins ir.Instruction) error {
	t := ins.ValueType()
	source := l.source.Instructions[ins.A].ValueType()
	pieces := int(t.Bits() / 128)
	native := l.native && t.Bits()%128 == 0 && source.Bits()%128 == 0 && len(l.values[ins.A].pieces) == int(source.Bits()/128)
	if ins.Op == ir.OpVShuffle {
		native = native && len(l.values[ins.B].pieces) == pieces
	}
	classification := abi.Scalarized
	if native {
		classification = abi.Native
		if pieces > 1 {
			classification = abi.Split
		}
	}
	chunk := int(128 / uint16(t.LaneBits))
	if native && ins.Op == ir.OpVShuffle {
		out := legalizedValue{pieces: carve(&l.pieceArena, pieces)}
		shape := ir.Vec(uint16(chunk), t.LaneBits)
		for n := 0; n < pieces; n++ {
			piece, ok := l.shuffleChunk(ins, n, chunk, shape)
			if !ok {
				// Not encodable: insert this chunk's lanes into a zero vector.
				classification = abi.Scalarized
				a, b := l.unpack(ins.A), l.unpack(ins.B)
				v := l.emit(ir.Instruction{Op: ir.OpVZero, Type: shape, Bits: t.LaneBits})
				for i, index := range ins.Data[n*chunk : (n+1)*chunk] {
					lane := 0
					if int(index) < len(a) {
						lane = a[index]
					} else {
						lane = b[int(index)-len(a)]
					}
					v = l.emit(ir.Instruction{Op: ir.OpVInsertZero, Type: shape, Bits: t.LaneBits, A: v, B: lane, Imm: uint64(i)})
				}
				piece = vectorPiece{v, shape, true}
			}
			out.pieces = append(out.pieces, piece)
		}
		l.reports = append(l.reports, abi.VectorOperation{Position: id, Operation: uint8(ins.Op), Type: t, Lowering: classification})
		l.values[id] = out
		return nil
	}
	// The lanes an extend widens: all of A, or its half Imm.
	first := int(ins.Imm) * int(t.Lanes)
	if native {
		out := legalizedValue{pieces: carve(&l.pieceArena, pieces)}
		shape := ir.Vec(uint16(chunk), t.LaneBits)
		for n := 0; n < pieces; n++ {
			lane := first + n*chunk
			input := l.values[ins.A].pieces[lane/(2*chunk)]
			half := uint64(lane % (2 * chunk) / chunk)
			if !nativeLaneMove(l.arch, ins.Op, t.LaneBits, nil, half) {
				return ErrBackendLimit
			}
			v := l.emit(ir.Instruction{Op: ins.Op, Type: shape, Bits: t.LaneBits, A: input.id, Imm: half})
			out.pieces = append(out.pieces, vectorPiece{v, shape, true})
		}
		l.reports = append(l.reports, abi.VectorOperation{Position: id, Operation: uint8(ins.Op), Type: t, Lowering: classification})
		l.values[id] = out
		return nil
	}
	l.reports = append(l.reports, abi.VectorOperation{Position: id, Operation: uint8(ins.Op), Type: t, Lowering: abi.Scalarized})
	lanes := l.lanesFor(int(t.Lanes))
	a := l.unpack(ins.A)
	switch ins.Op {
	case ir.OpVShuffle:
		b := l.unpack(ins.B)
		for _, index := range ins.Data {
			if int(index) < len(a) {
				lanes = append(lanes, a[index])
			} else {
				lanes = append(lanes, b[int(index)-len(a)])
			}
		}
	case ir.OpVExtendUnsigned:
		for _, lane := range a[first : first+int(t.Lanes)] {
			lanes = append(lanes, l.cast(lane, t.LaneBits))
		}
	case ir.OpVExtendSigned:
		for _, lane := range a[first : first+int(t.Lanes)] {
			lanes = append(lanes, l.emit(ir.Instruction{Op: ir.OpSignExtend, Bits: t.LaneBits, A: lane, Imm: uint64(t.LaneBits / 2)}))
		}
	}
	l.values[id] = l.pack(t, lanes)
	return nil
}

// shuffleChunk lowers output chunk n of a native shuffle to a piece: an
// input piece unchanged, or one machine shuffle of at most two input pieces.
func (l *vectorLegalizer) shuffleChunk(ins ir.Instruction, n, chunk int, shape ir.Type) (vectorPiece, bool) {
	inputs := [2][]vectorPiece{l.values[ins.A].pieces, l.values[ins.B].pieces}
	count := len(inputs[0])
	var used [2]vectorPiece
	sources := 0
	var local [16]byte
	for i, index := range ins.Data[n*chunk : (n+1)*chunk] {
		whole := int(index) / chunk
		piece := inputs[whole/count][whole%count]
		k := 0
		for k < sources && used[k].id != piece.id {
			k++
		}
		if k == sources {
			if sources == 2 {
				return vectorPiece{}, false
			}
			used[sources] = piece
			sources++
		}
		local[i] = byte(k*chunk + int(index)%chunk)
	}
	identity := sources == 1
	for i := 0; i < chunk && identity; i++ {
		identity = int(local[i]) == i
	}
	if identity {
		return used[0], true
	}
	if sources == 1 {
		used[1] = used[0]
	}
	if !nativeLaneMove(l.arch, ir.OpVShuffle, shape.LaneBits, local[:chunk], 0) {
		return vectorPiece{}, false
	}
	v := l.emit(ir.Instruction{Op: ir.OpVShuffle, Type: shape, Bits: shape.LaneBits, A: used[0].id, B: used[1].id, Data: append([]byte(nil), local[:chunk]...)})
	return vectorPiece{v, shape, true}, true
}

// laneMoveScratch appends the expansion temporaries a machine shuffle or
// extend may write besides A: XMM0 on SSE2, and on NEON V30 and V31 for a
// TBL table, which only an expansion longer than one instruction builds.
func laneMoveScratch(arch string, ins machine.Instruction, registers []backend.Register) []backend.Register {
	switch arch {
	case "amd64", "386":
		return append(registers, backend.VectorRegister(0))
	case "arm64":
		if ins.Arithmetic == ir.OpVShuffle && targetarm64.VectorSize(ins, abi.StackPlatform, 0) > 4 {
			return append(registers, backend.VectorRegister(30), backend.VectorRegister(31))
		}
	}
	return registers
}
