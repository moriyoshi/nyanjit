package compile

import (
	"encoding/binary"
	"fmt"
	"runtime"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

type vectorPiece struct {
	id     int
	t      ir.Type
	native bool
}
type legalizedValue struct {
	pieces []vectorPiece
	lanes  []int
}
type vectorLegalizer struct {
	source      *ir.Program
	out         *ir.Program
	values      []legalizedValue
	boundary    []int
	arch        string
	bits        uint8
	native      bool
	byteMemory  bool
	info        VectorInfo
	reports     []abi.VectorOperation
	variableIDs [][]int
	// The arenas back lane and piece lists, which are short and live as long
	// as the legalizer; carving them from shared chunks avoids one allocation
	// each.
	laneArena  []int
	pieceArena []vectorPiece
}

// lanesFor returns an empty lane list with room for n lanes. Appending past
// n reallocates, so lists never overlap.
func (l *vectorLegalizer) lanesFor(n int) []int { return carve(&l.laneArena, n) }

// lane returns a one-lane list holding value.
func (l *vectorLegalizer) lane(value int) []int { return append(l.lanesFor(1), value) }

// legalizedBudget bounds the scalar program legalization may produce, so
// compile cost follows the IR size a caller limits. Lane-wise lowering stays
// far below it; a carryless multiply without native support costs about 500
// instructions per 64-bit pair, so many of them in a small program reach it.
func legalizedBudget(sourceInstructions int) int {
	return 64*sourceInstructions + 1<<17
}

func legalizeVectors(source *ir.Program, options compileOptions) (*ir.Program, []abi.VectorOperation, error) {
	return legalizeVectorsWithMemoryFallback(source, options, false)
}

func legalizeVectorsWithMemoryFallback(source *ir.Program, options compileOptions, byteMemory bool) (*ir.Program, []abi.VectorOperation, error) {
	if err := validateVectorFeatures(options); err != nil {
		return nil, nil, err
	}
	has := false
	for _, ins := range source.Instructions {
		has = has || ins.IsVector() || narrowMemoryFallback(ins, options.Architecture) || byteMemory && ins.NarrowMemoryBits() != 0
	}
	if !has {
		return source, nil, nil
	}
	arch := options.Architecture
	if arch == "" {
		arch = runtime.GOARCH
	}
	bits := uint8(64)
	if arch == "386" {
		bits = 32
	}
	info := vectorInfo(options)
	native := false
	for _, f := range info.Features {
		native = native || f == "sse2" || f == "neon" || f == "lsx"
	}
	// Phase 1 scalarizes labeled vector homes. Entry and call ABI remain scalar.
	native = native && source.Control == nil && len(source.Variables) == 0
	l := &vectorLegalizer{source: source, out: &ir.Program{Arity: source.Arity, Result: -1, Instructions: make([]ir.Instruction, 0, len(source.Instructions))}, values: make([]legalizedValue, len(source.Instructions)), boundary: make([]int, len(source.Instructions)+1), arch: arch, bits: bits, native: native, byteMemory: byteMemory, info: info}
	l.initializeVariables()
	l.out.ParameterTypes = source.ParameterTypes
	budget := legalizedBudget(len(source.Instructions))
	for id, ins := range source.Instructions {
		l.boundary[id] = len(l.out.Instructions)
		if err := l.operation(id, ins); err != nil {
			return nil, nil, fmt.Errorf("SIMD value %d: %w", id, err)
		}
		if len(l.out.Instructions) > budget {
			return nil, nil, fmt.Errorf("SIMD value %d: scalar lowering exceeds %d instructions for a %d-instruction program: %w",
				id, budget, len(source.Instructions), ErrBackendLimit)
		}
	}
	l.boundary[len(source.Instructions)] = len(l.out.Instructions)
	l.finishVariables()
	scalar := func(id int) int {
		if id < 0 {
			return id
		}
		return l.values[id].lanes[0]
	}
	if source.Control == nil {
		l.out.Result = scalar(source.Result)
		for _, id := range source.ResultParts {
			l.out.ResultParts = append(l.out.ResultParts, scalar(id))
		}
	}
	for _, exit := range source.ScopeExits {
		copy := exit
		copy.Enter, copy.Position = l.boundary[exit.Enter], l.boundary[exit.Position]
		copy.Hidden, copy.Exports = nil, nil
		for _, id := range exit.Hidden {
			for _, p := range l.values[id].pieces {
				copy.Hidden = append(copy.Hidden, p.id)
			}
			if len(l.values[id].pieces) == 0 {
				copy.Hidden = append(copy.Hidden, l.values[id].lanes...)
			}
		}
		for _, id := range exit.Exports {
			for _, p := range l.values[id].pieces {
				copy.Exports = append(copy.Exports, p.id)
			}
			if len(l.values[id].pieces) == 0 {
				copy.Exports = append(copy.Exports, l.values[id].lanes...)
			}
		}
		l.out.ScopeExits = append(l.out.ScopeExits, copy)
	}
	if source.Control != nil {
		// Clone only control: the detached source instruction graph is read-only.
		c := (&ir.Program{Control: source.Control}).Clone().Control
		l.out.Control = c
		for label, widths := range c.ParamBits {
			expanded := []uint8{}
			for i, w := range widths {
				t := ir.Scalar(w)
				if len(source.Control.ParamTypes) != 0 {
					t = source.Control.ParamTypes[label][i]
				}
				if t.Kind == ir.VectorKind {
					for n := uint16(0); n < t.Lanes; n++ {
						expanded = append(expanded, t.LaneBits)
					}
				} else if t.Kind == ir.MaskKind {
					for n := uint16(0); n < t.Lanes; n++ {
						expanded = append(expanded, 8)
					}
				} else {
					expanded = append(expanded, w)
				}
			}
			c.ParamBits[label] = expanded
			c.Params[label] = len(expanded)
		}
		c.ParamTypes = nil
		for i, step := range c.Steps {
			if step.IsLabel {
				continue
			}
			step.Start, step.End = l.boundary[step.Start], l.boundary[step.End]
			if step.Term != ir.TermJump {
				step.Result = scalar(step.Result)
			}
			for j, id := range step.ResultParts {
				step.ResultParts[j] = scalar(id)
			}
			expand := func(ids []int) []int {
				out := []int{}
				for _, id := range ids {
					out = append(out, l.values[id].lanes...)
				}
				return out
			}
			step.Values, step.Args, step.OtherArgs = expand(step.Values), expand(step.Args), expand(step.OtherArgs)
			var mapped *ir.PredicateExpr
			var err error
			if step.Term == ir.TermBranch {
				mapped, err = mapVectorPredicate(step.Predicate, scalar)
			}
			if err != nil {
				return nil, nil, err
			}
			step.Predicate = mapped
			c.Steps[i] = step
		}
	}
	if err := l.out.Validate(); err != nil {
		return nil, nil, err
	}
	return l.out, l.reports, nil
}
func mapVectorPredicate(p *ir.PredicateExpr, scalar func(int) int) (*ir.PredicateExpr, error) {
	if p == nil {
		return nil, nil
	}
	p = ir.RebasePredicate(p, 0)
	for node, err := range predicateComparisons(p) {
		if err != nil {
			return nil, err
		}
		node.Value = scalar(node.Value)
		if node.WithValue {
			node.Other = scalar(node.Other)
		}
		if node.HasAnchor {
			node.Anchor = scalar(node.Anchor)
		}
	}
	return p, nil
}

func (l *vectorLegalizer) emit(ins ir.Instruction) int {
	id := len(l.out.Instructions)
	l.out.Instructions = append(l.out.Instructions, ins)
	return id
}
func (l *vectorLegalizer) constant(bits uint8, x uint64) int {
	return l.emit(ir.Instruction{Op: ir.OpConst, Bits: bits, Imm: x & (^uint64(0) >> (64 - bits))})
}
func (l *vectorLegalizer) cast(id int, bits uint8) int {
	return l.emit(ir.Instruction{Op: ir.OpCastUnsigned, Bits: bits, A: id})
}
func (l *vectorLegalizer) binary(op ir.Opcode, bits uint8, a, b int) int {
	return l.emit(ir.Instruction{Op: op, Bits: bits, A: a, B: b})
}
func (l *vectorLegalizer) address(base int, offset int) int {
	if l.out.Instructions[base].ValueType() != ir.Scalar(l.bits) {
		base = l.cast(base, l.bits)
	}
	if offset == 0 {
		return base
	}
	return l.binary(ir.OpAdd, l.bits, base, l.constant(l.bits, uint64(offset)))
}
func (l *vectorLegalizer) unpack(id int) []int {
	value := &l.values[id]
	if len(value.lanes) != 0 {
		return value.lanes
	}
	for _, p := range value.pieces {
		for n := uint16(0); n < p.t.Lanes; n++ {
			value.lanes = append(value.lanes, l.emit(ir.Instruction{Op: ir.OpVExtract, Bits: p.t.LaneBits, A: p.id, Imm: uint64(n)}))
		}
	}
	return value.lanes
}
func (l *vectorLegalizer) pack(t ir.Type, lanes []int) legalizedValue {
	if !l.native || t.Kind != ir.VectorKind || t.Bits()%128 != 0 {
		return legalizedValue{lanes: lanes}
	}
	chunk := int(128 / uint16(t.LaneBits))
	out := legalizedValue{pieces: carve(&l.pieceArena, len(lanes)/chunk)}
	for start := 0; start < len(lanes); start += chunk {
		shape := ir.Vec(uint16(chunk), t.LaneBits)
		v := l.emit(ir.Instruction{Op: ir.OpVZero, Type: shape, Bits: t.LaneBits})
		for i, scalar := range lanes[start : start+chunk] {
			v = l.emit(ir.Instruction{Op: ir.OpVInsertZero, Type: shape, Bits: t.LaneBits, A: v, B: scalar, Imm: uint64(i)})
		}
		out.pieces = append(out.pieces, vectorPiece{v, shape, true})
	}
	return out
}
func (l *vectorLegalizer) scalarLoad(bits uint8, address int, order ir.ByteOrder) int {
	if bits == 64 {
		return l.emit(ir.Instruction{Op: ir.OpLoad64, Bits: 64, A: address, ByteOrder: order})
	}
	result := l.constant(bits, 0)
	big := order == ir.ByteOrderBigEndian
	for n := 0; n < int(bits/8); n++ {
		byteValue := l.emit(ir.Instruction{Op: ir.OpLoad8, Bits: 8, A: l.address(address, n)})
		byteValue = l.cast(byteValue, bits)
		shift := n * 8
		if big {
			shift = int(bits) - 8 - shift
		}
		if shift != 0 {
			byteValue = l.emit(ir.Instruction{Op: ir.OpShiftLeft, Bits: bits, A: byteValue, Imm: uint64(shift)})
		}
		result = l.binary(ir.OpOr, bits, result, byteValue)
	}
	return result
}
func (l *vectorLegalizer) scalarStore(bits uint8, address, value int, order ir.ByteOrder) {
	if bits == 64 {
		l.emit(ir.Instruction{Op: ir.OpStore64, Bits: 64, A: address, B: value, ByteOrder: order})
		return
	}
	big := order == ir.ByteOrderBigEndian
	for n := 0; n < int(bits/8); n++ {
		byteValue := value
		shift := n * 8
		if big {
			shift = int(bits) - 8 - shift
		}
		if shift != 0 {
			byteValue = l.emit(ir.Instruction{Op: ir.OpShiftRight, Bits: bits, A: value, Imm: uint64(shift)})
		}
		l.emit(ir.Instruction{Op: ir.OpStore8, Bits: bits, A: l.address(address, n), B: byteValue})
	}
}
func (l *vectorLegalizer) compare(bits uint8, condition ir.BranchCondition, a, b int) int {
	one := l.constant(bits, 1)
	switch condition {
	case ir.BranchEqual, ir.BranchNotEqual:
		xor := l.binary(ir.OpXor, bits, a, b)
		yes, no := l.constant(bits, 0), one
		if condition == ir.BranchNotEqual {
			yes, no = no, yes
		}
		return l.emit(ir.Instruction{Op: ir.OpSelect, Bits: bits, A: xor, B: yes, C: no})
	}
	signed := condition == ir.BranchLess || condition == ir.BranchGreater || condition == ir.BranchLessEqual || condition == ir.BranchGreaterEqual
	if signed {
		sign := l.constant(bits, 1<<(bits-1))
		a, b = l.binary(ir.OpXor, bits, a, sign), l.binary(ir.OpXor, bits, b, sign)
	}
	swap := condition == ir.BranchGreater || condition == ir.BranchLessEqual || condition == ir.BranchAbove || condition == ir.BranchBelowEqual
	if swap {
		a, b = b, a
	}
	result := l.emit(ir.Instruction{Op: ir.OpBorrowSub, Bits: bits, A: a, B: b})
	if condition == ir.BranchGreaterEqual || condition == ir.BranchLessEqual || condition == ir.BranchAboveEqual || condition == ir.BranchBelowEqual {
		result = l.binary(ir.OpXor, bits, result, one)
	}
	return result
}
func (l *vectorLegalizer) operation(id int, ins ir.Instruction) error {
	var inputBuffer [4]int
	if handled, err := l.variableOperation(id, ins); handled {
		return err
	}
	t := ins.ValueType()
	if !ins.IsVector() {
		copy := ins.Clone()
		for _, input := range ins.InputsInto(inputBuffer[:0]) {
			if len(l.values[input].lanes) != 1 {
				return ErrInvalidFunction
			}
		}
		if ins.Op != ir.OpCallC && ins.Op != ir.OpCallGo && ins.Op != ir.OpSyscall {
			switch len(ins.InputsInto(inputBuffer[:0])) {
			case 1:
				copy.A = l.values[ins.A].lanes[0]
			case 2:
				copy.A, copy.B = l.values[ins.A].lanes[0], l.values[ins.B].lanes[0]
			case 3:
				copy.A, copy.B, copy.C = l.values[ins.A].lanes[0], l.values[ins.B].lanes[0], l.values[ins.C].lanes[0]
			}
		}
		if ins.Op == ir.OpCallC || ins.Op == ir.OpCallGo || ins.Op == ir.OpSyscall {
			for i, input := range ins.Args {
				copy.Args[i] = l.values[input].lanes[0]
			}
		}
		if ins.Op == ir.OpLabelParam {
			copy.Imm = uint64(l.labelSlot(id, int(ins.Imm)))
		}
		if narrowMemoryFallback(copy, l.arch) || l.byteMemory && copy.NarrowMemoryBits() != 0 {
			var result int
			switch copy.Op {
			case ir.OpLoad16, ir.OpLoad32:
				result = l.scalarLoad(copy.Bits, copy.A, copy.ByteOrder)
			default:
				value := l.cast(copy.B, copy.Bits)
				if l.byteMemory && l.arch != "386" {
					l.fallbackStore(copy.Bits, copy.A, value, copy.ByteOrder)
				} else {
					l.scalarStore(copy.Bits, copy.A, value, copy.ByteOrder)
				}
				result = len(l.out.Instructions) - 1
			}
			l.values[id] = legalizedValue{lanes: l.lane(result)}
			return nil
		}
		result := l.emit(copy)
		l.values[id] = legalizedValue{lanes: l.lane(result)}
		return nil
	}
	reportType := t
	if ins.Op == ir.OpVExtract || ins.Op == ir.OpVMaskBits {
		reportType = l.source.Instructions[ins.A].ValueType()
	}
	classification := abi.Scalarized
	info := l.info
	if ins.Op == ir.OpVCompare {
		classification = info.Lowering(ins.Op, l.source.Instructions[ins.A].ValueType(), ir.BranchCondition(ins.Imm))
	} else {
		classification = info.Lowering(ins.Op, reportType)
	}
	if !l.native {
		classification = abi.Scalarized
	}
	// The shape-only query conservatively allows a scalar count splat. A
	// per-lane LSX right rotation is exactly one hardware instruction.
	if l.native && l.arch == "loong64" && ins.Op == ir.OpRotateRightBy && t.Bits() == 128 && l.source.Instructions[ins.B].ValueType().Kind == ir.VectorKind {
		classification = abi.Native
	}
	native := l.native && t.Kind == ir.VectorKind && t.Bits()%128 == 0 && nativeVectorOperation(l.arch, ins.Op, t, info.Features)
	if ins.Op == ir.OpVCompare {
		native = l.native && ins.Bits == 8 && ins.Imm == uint64(ir.BranchEqual) && t.Lanes == 16
	}
	if ins.Op == ir.OpVMaskBits {
		native = l.native && len(l.values[ins.A].pieces) == 1 && l.values[ins.A].pieces[0].native
	}
	if ins.Op == ir.OpVExtract {
		native = l.native && len(l.values[ins.A].pieces) > 0
	}
	if ins.ByteOrder != ir.ByteOrderNative && ins.ByteOrder != ir.ByteOrderLittleEndian {
		native = false
	}
	if !native {
		classification = abi.Scalarized
	}
	if native && reportType.Bits() > 128 {
		classification = abi.Split
	}
	l.reports = append(l.reports, abi.VectorOperation{Position: id, Operation: uint8(ins.Op), Type: reportType, Lowering: classification})
	if ins.Op == ir.OpLabelParam {
		lanes := int(t.Lanes)
		bits := t.LaneBits
		if t.Kind == ir.MaskKind {
			bits = 8
		}
		out := legalizedValue{lanes: l.lanesFor(lanes)}
		slot := l.labelSlot(id, int(ins.Imm))
		for n := 0; n < lanes; n++ {
			out.lanes = append(out.lanes, l.emit(ir.Instruction{Op: ir.OpLabelParam, Bits: bits, Imm: uint64(slot + n), EntryReg: -1}))
		}
		l.values[id] = out
		return nil
	}
	if native {
		out := legalizedValue{}
		if ins.Op == ir.OpVMaskBits {
			copy := ins
			copy.Type = ir.Type{}
			copy.A = l.values[ins.A].pieces[0].id
			out.lanes = l.lane(l.emit(copy))
			l.values[id] = out
			return nil
		}
		if ins.Op == ir.OpVExtract {
			n := int(ins.Imm)
			for _, p := range l.values[ins.A].pieces {
				if n < int(p.t.Lanes) {
					copy := ins
					copy.A = p.id
					copy.Imm = uint64(n)
					out.lanes = l.lane(l.emit(copy))
					l.values[id] = out
					return nil
				}
				n -= int(p.t.Lanes)
			}
			return ErrInvalidFunction
		}
		chunk := int(128 / uint16(ins.Bits))
		if ins.Op == ir.OpVCompare {
			chunk = 16
		}
		count := int(t.Lanes) / chunk
		if ins.Op == ir.OpVConst {
			shape := ir.Vec(128/uint16(t.LaneBits), t.LaneBits)
			out.pieces = carve(&l.pieceArena, len(ins.Data)/16)
			for n := 0; n < len(ins.Data); n += 16 {
				id := l.emit(ir.Instruction{Op: ir.OpVConst, Type: shape, Bits: t.LaneBits, Data: append([]byte(nil), ins.Data[n:n+16]...)})
				out.pieces = append(out.pieces, vectorPiece{id, shape, true})
			}
			l.values[id] = out
			return nil
		}
		if ins.Op != ir.OpVStore {
			out.pieces = carve(&l.pieceArena, count)
		}
		for n := 0; n < count; n++ {
			copy := ins.Clone()
			shape := ir.Vec(uint16(chunk), ins.Bits)
			copy.Type = shape
			switch ins.Op {
			case ir.OpVZero: // no input values
			case ir.OpVLoad:
				copy.A = l.address(l.values[ins.A].lanes[0], n*16)
			case ir.OpVStore:
				copy.A = l.address(l.values[ins.A].lanes[0], n*16)
				copy.B = l.values[ins.B].pieces[n].id
			case ir.OpVSplat:
				copy.A = l.values[ins.A].lanes[0]
			case ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight:
				copy.A = l.values[ins.A].pieces[n].id
			case ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy:
				copy.A = l.values[ins.A].pieces[n].id
				if l.source.Instructions[ins.B].ValueType().Kind == ir.ScalarKind {
					copy.B = l.values[ins.B].lanes[0]
				} else {
					copy.B = l.values[ins.B].pieces[n].id
				}
			case ir.OpVCompare:
				copy.A, copy.B = l.values[ins.A].pieces[0].id, l.values[ins.B].pieces[0].id
				copy.Type = ir.Mask(16)
			case ir.OpVInsert, ir.OpVInsertZero:
				// General insert is currently native on NEON and SSE2 halfwords.
				copy.A = l.values[ins.A].pieces[n].id
				copy.B = l.values[ins.B].lanes[0]
				if int(ins.Imm)/chunk != n {
					out.pieces = append(out.pieces, l.values[ins.A].pieces[n])
					continue
				}
				copy.Imm = ins.Imm % uint64(chunk)
			default:
				copy.A, copy.B = l.values[ins.A].pieces[n].id, l.values[ins.B].pieces[n].id
			}
			result := l.emit(copy)
			if ins.Op != ir.OpVStore {
				out.pieces = append(out.pieces, vectorPiece{result, shape, true})
			}
		}
		l.values[id] = out
		return nil
	}
	if ins.Op == ir.OpVStore {
		lanes := l.unpack(ins.B)
		for n, lane := range lanes {
			l.scalarStore(ins.Bits, l.address(l.values[ins.A].lanes[0], n*int(ins.Bits/8)), lane, ins.ByteOrder)
		}
		return nil
	}
	var lanes []int
	switch ins.Op {
	case ir.OpVExtract, ir.OpVMaskBits:
	default:
		lanes = l.lanesFor(int(t.Lanes))
	}
	switch ins.Op {
	case ir.OpVZero:
		for range int(t.Lanes) {
			lanes = append(lanes, l.constant(t.LaneBits, 0))
		}
	case ir.OpVLoad:
		for n := 0; n < int(t.Lanes); n++ {
			lanes = append(lanes, l.scalarLoad(t.LaneBits, l.address(l.values[ins.A].lanes[0], n*int(t.LaneBits/8)), ins.ByteOrder))
		}
	case ir.OpVSplat:
		for n := uint16(0); n < t.Lanes; n++ {
			lanes = append(lanes, l.values[ins.A].lanes[0])
		}
	case ir.OpVConst:
		for n := 0; n < int(t.Lanes); n++ {
			var bytes [8]byte
			copy(bytes[:], ins.Data[n*int(t.LaneBits/8):(n+1)*int(t.LaneBits/8)])
			lanes = append(lanes, l.constant(t.LaneBits, binary.LittleEndian.Uint64(bytes[:])))
		}
	case ir.OpVExtract:
		l.values[id] = legalizedValue{lanes: l.lane(l.unpack(ins.A)[ins.Imm])}
		return nil
	case ir.OpVInsert, ir.OpVInsertZero:
		lanes = append(lanes, l.unpack(ins.A)...)
		lanes[ins.Imm] = l.values[ins.B].lanes[0]
	case ir.OpVCarrylessMul:
		a, b := l.unpack(ins.A), l.unpack(ins.B)
		zero, one := l.constant(64, 0), l.constant(64, 1)
		for chunk := 0; chunk < len(a); chunk += 2 {
			x, y := a[chunk+int(ins.Imm&1)], b[chunk+int(ins.Imm>>1)]
			low, high := zero, zero
			for bit := uint64(0); bit < 64; bit++ {
				selectBit := y
				if bit != 0 {
					selectBit = l.emit(ir.Instruction{Op: ir.OpShiftRight, Bits: 64, A: y, Imm: bit})
				}
				mask := l.binary(ir.OpSub, 64, zero, l.binary(ir.OpAnd, 64, selectBit, one))
				word := x
				if bit != 0 {
					word = l.emit(ir.Instruction{Op: ir.OpShiftLeft, Bits: 64, A: x, Imm: bit})
				}
				low = l.binary(ir.OpXor, 64, low, l.binary(ir.OpAnd, 64, word, mask))
				if bit != 0 {
					upper := l.emit(ir.Instruction{Op: ir.OpShiftRight, Bits: 64, A: x, Imm: 64 - bit})
					high = l.binary(ir.OpXor, 64, high, l.binary(ir.OpAnd, 64, upper, mask))
				}
			}
			lanes = append(lanes, low, high)
		}
	case ir.OpVCompare:
		a, b := l.unpack(ins.A), l.unpack(ins.B)
		for n := range a {
			lanes = append(lanes, l.cast(l.compare(ins.Bits, ir.BranchCondition(ins.Imm), a[n], b[n]), 8))
		}
		l.values[id] = legalizedValue{lanes: lanes}
		return nil
	case ir.OpVMaskBits:
		result := l.constant(ins.Bits, 0)
		for n, lane := range l.unpack(ins.A) {
			bit := l.cast(lane, ins.Bits)
			if n != 0 {
				bit = l.emit(ir.Instruction{Op: ir.OpShiftLeft, Bits: ins.Bits, A: bit, Imm: uint64(n)})
			}
			result = l.binary(ir.OpOr, ins.Bits, result, bit)
		}
		l.values[id] = legalizedValue{lanes: l.lane(l.cast(result, ins.Bits))}
		return nil
	case ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight:
		for _, lane := range l.unpack(ins.A) {
			count := ins.Imm
			op := ins.Op
			if op == ir.OpRotateLeft || op == ir.OpRotateRight {
				count %= uint64(ins.Bits)
			} else if count >= uint64(ins.Bits) {
				if op == ir.OpShiftRightArithmetic {
					count = uint64(ins.Bits - 1)
				} else {
					lanes = append(lanes, l.constant(ins.Bits, 0))
					continue
				}
			}
			lanes = append(lanes, l.emit(ir.Instruction{Op: op, Bits: ins.Bits, A: lane, Imm: count}))
		}
	case ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy:
		a, b := l.unpack(ins.A), l.unpack(ins.B)
		for n, lane := range a {
			count := b[0]
			if len(b) > 1 {
				count = b[n]
			}
			lanes = append(lanes, l.emit(ir.Instruction{Op: ins.Op, Bits: ins.Bits, A: lane, B: count}))
		}
	case ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor:
		a, b := l.unpack(ins.A), l.unpack(ins.B)
		for n := range a {
			lanes = append(lanes, l.binary(ins.Op, ins.Bits, a[n], b[n]))
		}
	default:
		return ErrBackendLimit
	}
	l.values[id] = l.pack(t, lanes)
	return nil
}
func (l *vectorLegalizer) labelSlot(id, slot int) int {
	if l.source.Control == nil {
		return slot
	}
	label := -1
	for _, step := range l.source.Control.Steps {
		if step.IsLabel {
			label = step.Label
			continue
		}
		if id >= step.Start && id < step.End {
			break
		}
	}
	if label < 0 || len(l.source.Control.ParamTypes) == 0 {
		return slot
	}
	out := 0
	for i := 0; i < slot; i++ {
		t := l.source.Control.ParamTypes[label][i]
		if t.Kind == ir.VectorKind || t.Kind == ir.MaskKind {
			out += int(t.Lanes)
		} else {
			out++
		}
	}
	return out
}

// Unaligned narrow accesses use byte operations on strict-alignment baselines.
// Big-endian narrow IR is also scalarized because native encodings are LE.
func narrowMemoryFallback(ins ir.Instruction, arch string) bool {
	switch ins.Op {
	case ir.OpLoad16, ir.OpLoad32, ir.OpStore16, ir.OpStore32:
	default:
		return false
	}
	if ins.ByteOrder == ir.ByteOrderBigEndian {
		return true
	}
	if arch == "" {
		arch = runtime.GOARCH
	}
	return !ins.Aligned && (arch == "riscv64" || arch == "loong64" || arch == "ppc64le")
}
