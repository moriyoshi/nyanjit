package machine

import (
	"fmt"
	"math/bits"

	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/regalloc"
)

// SelectionTarget supplies the architecture constraints needed before
// physical registers are assigned.
type SelectionTarget interface {
	Architecture() string
	RegisterBits() uint8
	NativeByteOrder() ir.ByteOrder
	CanScaled(uint64, uint64) bool
	CanCombineScaled() bool
	CanMulAdd() bool
	CanFoldAddress(uint64, uint64) bool
	CanFoldBaseOffset(uint64) bool
}

func selectedInstructionBits(ins ir.Instruction) uint8 {
	if ins.Bits == 0 {
		return 64
	}
	return ins.Bits
}

// SelectProgram converts target-independent IR into virtual machine operations.
// It does not assign physical registers or assemble instructions.
func SelectProgram(ops []ir.Instruction, leaves []ir.ScopeExit, arity, result int, exports []int, maxArgs int, target SelectionTarget) (SelectedProgram, error) {
	if target == nil || maxArgs < 0 || result < 0 || result >= len(ops) || arity < 0 || arity > maxArgs {
		return SelectedProgram{}, regalloc.ErrInvalidFunction
	}
	program := SelectedProgram{Values: make([]SelectedValue, len(ops)), Result: result, Exports: append([]int(nil), exports...)}
	fmaCapability, hasFMACapability := target.(interface{ SupportsFloatFMA() bool })
	supportsFMA := hasFMACapability && fmaCapability.SupportsFloatFMA()
	refs := make([]int, len(ops))
	params := 0
	for id, ins := range ops {
		if NativeVectorInstruction(ins) {
			if target.Architecture() != "amd64" && target.Architecture() != "arm64" {
				return SelectedProgram{}, regalloc.ErrBackendLimit
			}
			program.Values[id].Words = 1
			program.Values[id].Param = -1
			program.Values[id].EntrySlot = -1
			program.Values[id].EntryReg = -1
			if ins.Type.Kind == ir.VectorKind || ins.Type.Kind == ir.MaskKind {
				program.Values[id].Class = regalloc.Vector
			}
			for _, input := range ins.Inputs() {
				if input < 0 || input >= id {
					return SelectedProgram{}, regalloc.ErrInvalidFunction
				}
				refs[input]++
			}
			continue
		}
		if !ins.ByteOrder.Valid() || ins.ByteOrder != ir.ByteOrderNative &&
			ins.Op != ir.OpLoad64 && ins.Op != ir.OpStore64 && ins.Op != ir.OpLoad8 && ins.Op != ir.OpStore8 {
			return SelectedProgram{}, regalloc.ErrInvalidFunction
		}
		if ins.Contract && ins.Op != ir.OpFAdd {
			return SelectedProgram{}, regalloc.ErrInvalidFunction
		}
		if ins.Op == ir.OpFSqrt || ins.Op == ir.OpFConvert {
			if target.RegisterBits() != 64 {
				return SelectedProgram{}, fmt.Errorf("floating unary operation at value %d is unavailable on %s: %w", id, target.Architecture(), regalloc.ErrBackendLimit)
			}
			inputBits := ins.Bits
			if ins.Op == ir.OpFConvert {
				inputBits = 96 - ins.Bits
			}
			if ins.Bits != 32 && ins.Bits != 64 || ins.A < 0 || ins.A >= id || selectedInstructionBits(ops[ins.A]) != inputBits {
				return SelectedProgram{}, regalloc.ErrInvalidFunction
			}
		}
		if ins.Op == ir.OpFAdd || ins.Op == ir.OpFSub || ins.Op == ir.OpFMul || ins.Op == ir.OpFDiv || ins.Op == ir.OpFMA || ins.Op == ir.OpFProductError {
			if target.RegisterBits() != 64 {
				return SelectedProgram{}, fmt.Errorf("floating operation at value %d is unavailable on %s: %w", id, target.Architecture(), regalloc.ErrBackendLimit)
			}
			if ins.Op == ir.OpFProductError && ins.Bits != 64 {
				return SelectedProgram{}, regalloc.ErrInvalidFunction
			}
			if ins.Op == ir.OpFMA && !supportsFMA {
				return SelectedProgram{}, fmt.Errorf("FMA at value %d is unavailable for the %s target: %w", id, target.Architecture(), regalloc.ErrBackendLimit)
			}
			if (ins.Bits != 32 && ins.Bits != 64) || ins.A < 0 || ins.B < 0 || ins.A >= id || ins.B >= id ||
				selectedInstructionBits(ops[ins.A]) != ins.Bits || selectedInstructionBits(ops[ins.B]) != ins.Bits {
				return SelectedProgram{}, regalloc.ErrInvalidFunction
			}
			if (ins.Op == ir.OpFMA || ins.Op == ir.OpFProductError) && (ins.C < 0 || ins.C >= id || selectedInstructionBits(ops[ins.C]) != ins.Bits) {
				return SelectedProgram{}, regalloc.ErrInvalidFunction
			}
			if ins.Contract && (ins.Op != ir.OpFAdd || ops[ins.A].Op != ir.OpFMul && ops[ins.B].Op != ir.OpFMul) {
				return SelectedProgram{}, regalloc.ErrInvalidFunction
			}
		}
		if ins.Op == ir.OpCarryAdd || ins.Op == ir.OpBorrowSub || ins.Op == ir.OpAddWithCarry || ins.Op == ir.OpSubWithBorrow {
			if ins.A < 0 || ins.B < 0 || ins.A >= id || ins.B >= id || selectedInstructionBits(ops[ins.A]) != selectedInstructionBits(ins) || selectedInstructionBits(ops[ins.B]) != selectedInstructionBits(ins) ||
				(ins.Op == ir.OpAddWithCarry || ins.Op == ir.OpSubWithBorrow) && (ins.C < 0 || ins.C >= id || selectedInstructionBits(ops[ins.C]) != selectedInstructionBits(ins)) {
				return SelectedProgram{}, regalloc.ErrInvalidFunction
			}
		}
		if ins.Op == ir.OpLeadingZeros || ins.Op == ir.OpTrailingZeros {
			if ins.A < 0 || ins.A >= id || selectedInstructionBits(ops[ins.A]) != selectedInstructionBits(ins) {
				return SelectedProgram{}, regalloc.ErrInvalidFunction
			}
		}

		if ins.Op == ir.OpMulHigh && (ins.A < 0 || ins.B < 0 || ins.A >= id || ins.B >= id ||
			selectedInstructionBits(ops[ins.A]) != selectedInstructionBits(ins) || selectedInstructionBits(ops[ins.B]) != selectedInstructionBits(ins)) {
			return SelectedProgram{}, regalloc.ErrInvalidFunction
		}
		if _, by := ir.ShiftByOperation(ins.Op); by {
			if ins.A < 0 || ins.B < 0 || ins.A >= id || ins.B >= id ||
				selectedInstructionBits(ops[ins.A]) != selectedInstructionBits(ins) {
				return SelectedProgram{}, regalloc.ErrInvalidFunction
			}
		}
		words, err := regalloc.IntegerRegisterWords(selectedInstructionBits(ins), target.RegisterBits())
		if err != nil {
			return SelectedProgram{}, err
		}
		program.Values[id].Bits = selectedInstructionBits(ins)
		program.Values[id].Words = words
		program.Values[id].Param = -1
		program.Values[id].EntrySlot = -1
		program.Values[id].EntryReg = -1
		var inputs []int
		switch ins.Op {
		case ir.OpGet, ir.OpScopeLeave:
		case ir.OpSet:
			inputs = []int{ins.A}
		case ir.OpScopeEnter:
			inputs = ins.Args
		case ir.OpParam:
			program.Values[id].Param = params
			params++
		case ir.OpConst:
			if ins.Symbol == ir.MathPi4Symbol && target.RegisterBits() != 64 {
				return SelectedProgram{}, fmt.Errorf("inline math tables are unavailable on %s: %w", target.Architecture(), regalloc.ErrBackendLimit)
			}
			program.Values[id].Constant = ins.Symbol == ""
			program.Values[id].Symbol = ins.Symbol
			program.Values[id].Imm = ins.Imm
		case ir.OpLabelParam:
			if ins.EntryReg >= 0 {
				program.Values[id].EntryReg = ins.EntryReg
			} else {
				program.Values[id].EntrySlot = int(ins.Imm)
			}
		case ir.OpCarryAdd, ir.OpBorrowSub, ir.OpMulHigh, ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy, ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv, ir.OpAddScaled:
			inputs = []int{ins.A, ins.B}
		case ir.OpAddWithCarry, ir.OpSubWithBorrow, ir.OpFMA, ir.OpFProductError:
			inputs = []int{ins.A, ins.B, ins.C}
		case ir.OpLeadingZeros, ir.OpTrailingZeros, ir.OpFConvert, ir.OpFSqrt, ir.OpCastUnsigned, ir.OpSignExtend:
			inputs = []int{ins.A}
		case ir.OpLoad64, ir.OpLoad8:
			inputs = []int{ins.A}
		case ir.OpShiftRight, ir.OpShiftLeft, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight:
			inputs = []int{ins.A}
		case ir.OpStore64, ir.OpStore8:
			inputs = []int{ins.A, ins.B}
		case ir.OpSyscall, ir.OpCallC, ir.OpCallGo:
			inputs = ins.Args
			limit := 7
			if ins.Op == ir.OpCallC {
				limit = maxArgs + 1
			} else if ins.Op == ir.OpCallGo {
				goArgs := maxArgs
				if calls, ok := target.(interface{ GoCallMaxArguments() int }); ok {
					goArgs = calls.GoCallMaxArguments()
				}
				limit = goArgs + 1
			}
			if len(inputs) < 1 || len(inputs) > limit {
				return SelectedProgram{}, regalloc.ErrInvalidFunction
			}
		case ir.OpSelect:
			inputs = []int{ins.A, ins.B, ins.C}
		default:
			return SelectedProgram{}, regalloc.ErrInvalidFunction
		}
		for _, input := range inputs {
			if input < 0 || input >= id {
				return SelectedProgram{}, regalloc.ErrInvalidFunction
			}
			refs[input]++
		}
	}
	if params != arity {
		return SelectedProgram{}, regalloc.ErrInvalidFunction
	}
	paramRaw := make(map[int]int)
	for id, ins := range ops {
		if ins.Op != ir.OpParam || selectedInstructionBits(ins) == 64 {
			continue
		}
		paramRaw[id] = len(program.Values)
		rawBits := max(selectedInstructionBits(ins), target.RegisterBits())
		words, _ := regalloc.IntegerRegisterWords(rawBits, target.RegisterBits())
		program.Values = append(program.Values, SelectedValue{Bits: rawBits, Words: words, Param: program.Values[id].Param, EntrySlot: -1, EntryReg: -1})
		program.Values[id].Param = -1
	}
	for _, id := range exports {
		if id < 0 || id >= len(ops) {
			return SelectedProgram{}, regalloc.ErrInvalidFunction
		}
		refs[id]++
	}
	reachable := make([]bool, len(ops))
	var mark func(int)
	mark = func(id int) {
		if reachable[id] {
			return
		}
		reachable[id] = true
		ins := ops[id]
		if NativeVectorInstruction(ins) {
			for _, input := range ins.Inputs() {
				mark(input)
			}
			return
		}
		switch ins.Op {
		case ir.OpSet:
			mark(ins.A)
		case ir.OpScopeEnter:
			for _, input := range ins.Args {
				mark(input)
			}
		case ir.OpCarryAdd, ir.OpBorrowSub, ir.OpMulHigh, ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy, ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv, ir.OpAddScaled:
			mark(ins.A)
			mark(ins.B)
		case ir.OpAddWithCarry, ir.OpSubWithBorrow, ir.OpFMA, ir.OpFProductError:
			mark(ins.A)
			mark(ins.B)
			mark(ins.C)
		case ir.OpLeadingZeros, ir.OpTrailingZeros, ir.OpFConvert, ir.OpFSqrt, ir.OpLoad64, ir.OpLoad8, ir.OpShiftRight, ir.OpShiftLeft, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight, ir.OpCastUnsigned, ir.OpSignExtend:
			mark(ins.A)
		case ir.OpStore64, ir.OpStore8:
			mark(ins.A)
			mark(ins.B)
		case ir.OpSyscall, ir.OpCallC, ir.OpCallGo:
			for _, input := range ins.Args {
				mark(input)
			}
		case ir.OpSelect:
			mark(ins.A)
			mark(ins.B)
			mark(ins.C)
		}
	}
	mark(result)
	for _, id := range exports {
		mark(id)
	}
	for id, ins := range ops {
		if ins.Op == ir.OpGet || ins.Op == ir.OpSet || ins.Op == ir.OpScopeEnter || ins.Op == ir.OpScopeLeave || ins.Op == ir.OpVLoad || ins.Op == ir.OpVStore || ins.Op == ir.OpLoad64 || ins.Op == ir.OpLoad8 || ins.Op == ir.OpStore64 || ins.Op == ir.OpStore8 || ins.Op == ir.OpSyscall || ins.Op == ir.OpCallC || ins.Op == ir.OpCallGo {
			mark(id)
		}
	}

	newValueBits := func(width uint8, constant bool, imm uint64) int {
		id := len(program.Values)
		words, _ := regalloc.IntegerRegisterWords(width, target.RegisterBits())
		program.Values = append(program.Values, SelectedValue{Words: words, Constant: constant, Imm: imm, Param: -1, EntrySlot: -1, EntryReg: -1})
		return id
	}
	newRegisterValue := func() int { return newValueBits(target.RegisterBits(), false, 0) }
	canBinaryImmediate := func(operation ir.Opcode, width uint8, value uint64) bool {
		capability, ok := target.(interface {
			CanBinaryImmediate(ir.Opcode, uint8, uint64) bool
		})
		return ok && capability.CanBinaryImmediate(operation, width, value)
	}
	selectBinaryImmediate := func(operation ir.Opcode, width uint8, value uint64) (uint64, bool) {
		if canBinaryImmediate(operation, width, value) {
			return value, true
		}
		if width == 8 || width == 16 || width == 32 {
			// Arithmetic and bitwise results are taken modulo the value width.
			// A width-limited literal can therefore use a signed instruction
			// immediate when its unsigned form does not fit.
			signed := uint64(int64(value<<(64-width)) >> (64 - width))
			if signed != value && canBinaryImmediate(operation, width, signed) {
				return signed, true
			}
		}
		return 0, false
	}
	immediateZeroExtends := func(operation ir.Opcode, width uint8, value uint64) bool {
		capability, ok := target.(interface {
			BinaryImmediateZeroExtends(ir.Opcode, uint8, uint64) bool
		})
		return ok && capability.BinaryImmediateZeroExtends(operation, width, value)
	}
	appendScaled := func(dst, base, index int, scale, disp uint64) {
		width := selectedInstructionBits(ops[dst])
		if width < target.RegisterBits() {
			original := dst
			dst = newRegisterValue()
			defer func() {
				program.Instructions = append(program.Instructions, SelectedInstruction{
					Op: SelectedNarrow, Dst: original, A: dst, Imm: uint64(width),
				})
			}()
		}
		if target.Architecture() == "386" && width == 32 {
			// Arithmetic at native width discards the upper scale bits.
			scale = uint64(uint32(scale))
		}
		newScaledValue := func(constant bool, value uint64) int {
			id := len(program.Values)
			words, _ := regalloc.IntegerRegisterWords(width, target.RegisterBits())
			program.Values = append(program.Values, SelectedValue{Words: words, Constant: constant, Imm: value, Param: -1, EntrySlot: -1, EntryReg: -1})
			return id
		}
		appendConst := func(value uint64) int {
			id := newScaledValue(true, value)
			program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedConst, Dst: id, Imm: value})
			return id
		}
		appendDisplacement := func(destination, source int, value uint64) {
			if width == target.RegisterBits() && canBinaryImmediate(ir.OpAdd, width, value) {
				program.Instructions = append(program.Instructions, SelectedInstruction{
					Op: SelectedBinaryImmediate, Arithmetic: ir.OpAdd, Bits: width,
					Dst: destination, A: source, B: -1, Imm: value,
				})
				return
			}
			constant := appendConst(value)
			program.Instructions = append(program.Instructions, SelectedInstruction{
				Op: SelectedBinary, Arithmetic: ir.OpAdd, Bits: width,
				Dst: destination, A: source, B: constant,
			})
		}
		if target.CanScaled(scale, disp) {
			program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedScaled, Dst: dst, A: base, B: index, Imm: scale, Disp: disp})
			return
		}
		if scale == 0 {
			if disp == 0 {
				program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedMove, Dst: dst, A: base})
			} else {
				appendDisplacement(dst, base, disp)
			}
			return
		}
		negativeOne := ^uint64(0)
		if width == 32 {
			negativeOne = uint64(^uint32(0))
		}
		if target.Architecture() == "386" && scale == negativeOne {
			difference := dst
			if disp != 0 {
				difference = newScaledValue(false, 0)
			}
			program.Instructions = append(program.Instructions, SelectedInstruction{
				Op: SelectedBinary, Arithmetic: ir.OpSub, Bits: width,
				Dst: difference, A: base, B: index,
			})
			if disp != 0 {
				constant := appendConst(disp)
				program.Instructions = append(program.Instructions, SelectedInstruction{
					Op: SelectedBinary, Arithmetic: ir.OpAdd, Bits: width,
					Dst: dst, A: difference, B: constant,
				})
			}
			return
		}
		if target.Architecture() == "386" && scale&(scale-1) == 0 {
			shifted := newScaledValue(false, 0)
			program.Instructions = append(program.Instructions, SelectedInstruction{
				Op: SelectedShift, Arithmetic: ir.OpShiftLeft, Bits: width,
				Dst: shifted, A: index, Imm: uint64(bits.TrailingZeros64(scale)),
			})
			sum := dst
			if disp != 0 {
				sum = newScaledValue(false, 0)
			}
			program.Instructions = append(program.Instructions, SelectedInstruction{
				Op: SelectedBinary, Arithmetic: ir.OpAdd, Bits: width,
				Dst: sum, A: base, B: shifted,
			})
			if disp != 0 {
				constant := appendConst(disp)
				program.Instructions = append(program.Instructions, SelectedInstruction{
					Op: SelectedBinary, Arithmetic: ir.OpAdd, Bits: width,
					Dst: dst, A: sum, B: constant,
				})
			}
			return
		}
		if target.CanCombineScaled() {
			// Two address calculations can sum two encodable index scales
			// without materializing a multiplier or touching CPU flags.
			scales := [...]uint64{1, 2, 4, 8}
			for _, first := range scales {
				if first >= scale || !target.CanScaled(first, 0) || !target.CanScaled(scale-first, disp) {
					continue
				}
				intermediate := newScaledValue(false, 0)
				program.Instructions = append(program.Instructions,
					SelectedInstruction{Op: SelectedScaled, Dst: intermediate, A: base, B: index, Imm: first},
					SelectedInstruction{Op: SelectedScaled, Dst: dst, A: intermediate, B: index, Imm: scale - first, Disp: disp},
				)
				return
			}
			// An index-only LEA can form (1+inner)*index. A second LEA
			// scales that intermediate and adds the original base.
			for _, inner := range scales {
				for _, outer := range scales {
					if (1+inner)*outer != scale || !target.CanScaled(inner, 0) || !target.CanScaled(outer, disp) {
						continue
					}
					intermediate := newScaledValue(false, 0)
					program.Instructions = append(program.Instructions,
						SelectedInstruction{Op: SelectedScaled, Dst: intermediate, A: index, B: index, Imm: inner},
						SelectedInstruction{Op: SelectedScaled, Dst: dst, A: base, B: intermediate, Imm: outer, Disp: disp},
					)
					return
				}
			}
		}
		var sum int
		if target.CanScaled(scale, 0) {
			if disp == 0 {
				program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedScaled, Dst: dst, A: base, B: index, Imm: scale})
				return
			}
			sum = newScaledValue(false, 0)
			program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedScaled, Dst: sum, A: base, B: index, Imm: scale})
		} else if target.CanMulAdd() {
			factor := appendConst(scale)
			sum = dst
			if disp != 0 {
				sum = newScaledValue(false, 0)
			}
			program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedMulAdd, Dst: sum, A: base, B: index, C: factor})
			if disp == 0 {
				return
			}
		} else {
			product := index
			if scale != 1 {
				constant := appendConst(scale)
				product = newScaledValue(false, 0)
				program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedBinary, Arithmetic: ir.OpMul, Bits: width, Dst: product, A: index, B: constant})
			}
			if disp == 0 {
				program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedBinary, Arithmetic: ir.OpAdd, Bits: width, Dst: dst, A: base, B: product})
				return
			}
			sum = newScaledValue(false, 0)
			program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedBinary, Arithmetic: ir.OpAdd, Bits: width, Dst: sum, A: base, B: product})
		}
		appendDisplacement(dst, sum, disp)
	}

	scheduled := make([]bool, len(ops))
	numericConst := func(id int) bool { return ops[id].Op == ir.OpConst && ops[id].Symbol == "" }
	// An address can be folded only when this memory access is its sole
	// consumer. Otherwise it remains a regular virtual value, shared by uses.
	foldAddress := func(address int) (base, index int, scale, disp uint64, ok bool) {
		// Hardware address formation uses a full register. A narrower
		// pointer must retain its arithmetic truncation before dereference.
		if selectedInstructionBits(ops[address]) != target.RegisterBits() ||
			refs[address] != 1 || scheduled[address] {
			return
		}
		scaledID := address
		if ops[address].Op == ir.OpAdd {
			found := false
			for _, pair := range [][2]int{{ops[address].A, ops[address].B}, {ops[address].B, ops[address].A}} {
				if ops[pair[0]].Op == ir.OpAddScaled && numericConst(pair[1]) && refs[pair[0]] == 1 && !scheduled[pair[0]] {
					scaledID, disp, found = pair[0], ops[pair[1]].Imm, true
					break
				}
			}
			if !found {
				// A plain base + index is a scale-one address candidate.
				// Keep ordinary arithmetic as Add when it has other uses.
				plain := ops[address]
				if !numericConst(plain.A) && !numericConst(plain.B) && target.CanFoldAddress(1, 0) {
					return plain.A, plain.B, 1, 0, true
				}
				return
			}
		}
		if ops[scaledID].Op != ir.OpAddScaled {
			return
		}
		scaled := ops[scaledID]
		if !target.CanFoldAddress(scaled.Imm, disp) {
			return
		}
		return scaled.A, scaled.B, scaled.Imm, disp, true
	}
	foldBaseOffset := func(address int) (base int, disp uint64, ok bool) {
		// Hardware address formation uses a full register. A narrower
		// pointer must retain its arithmetic truncation before dereference.
		if selectedInstructionBits(ops[address]) != target.RegisterBits() ||
			refs[address] != 1 || scheduled[address] {
			return
		}
		ins := ops[address]
		switch ins.Op {
		case ir.OpAdd:
			if numericConst(ins.B) {
				base, disp = ins.A, ops[ins.B].Imm
			} else if numericConst(ins.A) {
				base, disp = ins.B, ops[ins.A].Imm
			} else {
				return
			}
		case ir.OpSub:
			if !numericConst(ins.B) {
				return
			}
			base, disp = ins.A, -ops[ins.B].Imm
		case ir.OpAddScaled:
			if ins.Imm != 0 {
				return
			}
			base = ins.A
		default:
			return
		}
		return base, disp, target.CanFoldBaseOffset(disp)
	}
	var schedule func(int)
	schedule = func(id int) {
		if scheduled[id] {
			return
		}
		scheduled[id] = true
		ins := ops[id]
		if NativeVectorInstruction(ins) {
			for _, input := range ins.Inputs() {
				schedule(input)
			}
			dst := id
			if ins.Op == ir.OpVStore {
				dst = -1
			}
			program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedVector, Data: append([]byte(nil), ins.Data...), Type: ins.Type, Arithmetic: ins.Op, Bits: ins.Bits, Dst: dst, A: ins.A, B: ins.B, Imm: ins.Imm, ByteOrder: ins.ByteOrder})
			return
		}
		switch ins.Op {
		case ir.OpGet:
			program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedHomeGet, Dst: id, Bits: ins.Bits, Variable: ins.Variable, Imm: ins.Imm})
		case ir.OpSet:
			schedule(ins.A)
			program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedHomeSet, Dst: -1, A: ins.A, Bits: ins.Bits, Variable: ins.Variable, Imm: ins.Imm})
		case ir.OpScopeEnter, ir.OpScopeLeave:
			for _, input := range ins.Args {
				schedule(input)
			}
			program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedHomeBoundary, Dst: -1, Arithmetic: ins.Op, Imm: ins.Imm, Args: append([]int(nil), ins.Args...)})

		case ir.OpParam:
			if raw, ok := paramRaw[id]; ok {
				program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedNarrow, Dst: id, A: raw, Imm: uint64(selectedInstructionBits(ins))})
			}
			return
		case ir.OpLabelParam:
			return
		case ir.OpConst:
			if ins.Symbol != "" {
				program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedSymbol, Dst: id, Symbol: ins.Symbol})
			} else {
				program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedConst, Dst: id, Imm: ins.Imm})
			}
		case ir.OpAddScaled:
			schedule(ins.A)
			if ins.Imm != 0 {
				schedule(ins.B)
			}
			appendScaled(id, ins.A, ins.B, ins.Imm, 0)
		case ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv:
			if ins.Op == ir.OpFAdd && ins.Contract && supportsFMA {
				for _, pair := range [][2]int{{ins.A, ins.B}, {ins.B, ins.A}} {
					mulID, addID := pair[0], pair[1]
					if ops[mulID].Op != ir.OpFMul || refs[mulID] != 1 || scheduled[mulID] {
						continue
					}
					mul := ops[mulID]
					schedule(mul.A)
					schedule(mul.B)
					schedule(addID)
					program.Instructions = append(program.Instructions, SelectedInstruction{
						Op: SelectedFloatFMA, Bits: selectedInstructionBits(ins), Dst: id,
						A: mul.A, B: mul.B, C: addID,
					})
					return
				}
			}
			schedule(ins.A)
			schedule(ins.B)
			program.Instructions = append(program.Instructions, SelectedInstruction{
				Op: SelectedFloatBinary, Arithmetic: ins.Op, Bits: selectedInstructionBits(ins),
				Dst: id, A: ins.A, B: ins.B,
			})
		case ir.OpFSqrt, ir.OpFConvert:
			schedule(ins.A)
			program.Instructions = append(program.Instructions, SelectedInstruction{
				Op: SelectedFloatUnary, Arithmetic: ins.Op, Bits: ins.Bits, Dst: id, A: ins.A,
			})
		case ir.OpFProductError:
			schedule(ins.A)
			schedule(ins.B)
			schedule(ins.C)
			appendProductError(&program, id, ins.A, ins.B, ins.C, supportsFMA)
		case ir.OpFMA:
			schedule(ins.A)
			schedule(ins.B)
			schedule(ins.C)
			program.Instructions = append(program.Instructions, SelectedInstruction{
				Op: SelectedFloatFMA, Bits: selectedInstructionBits(ins), Dst: id,
				A: ins.A, B: ins.B, C: ins.C,
			})
		case ir.OpCarryAdd, ir.OpBorrowSub, ir.OpAddWithCarry, ir.OpSubWithBorrow:
			schedule(ins.A)
			schedule(ins.B)
			width := selectedInstructionBits(ins)
			operation := ir.OpAdd
			if ins.Op == ir.OpBorrowSub || ins.Op == ir.OpSubWithBorrow {
				operation = ir.OpSub
			}
			capability, native := target.(interface{ CanWordCarry(uint8) bool })
			if native && capability.CanWordCarry(width) {
				if ins.Op == ir.OpCarryAdd || ins.Op == ir.OpBorrowSub {
					program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedCarryBit, Arithmetic: operation, Bits: width, Dst: id, A: ins.A, B: ins.B})
				} else {
					carryOp := ir.OpCarryAdd
					if operation == ir.OpSub {
						carryOp = ir.OpBorrowSub
					}
					selected := SelectedInstruction{Op: SelectedWordCarry, Arithmetic: operation, Bits: width, Dst: id, A: ins.A, B: ins.B, C: ins.C}
					if ops[ins.C].Op == carryOp && refs[ins.C] == 1 && !scheduled[ins.C] {
						low := ops[ins.C]
						schedule(low.A)
						schedule(low.B)
						scheduled[ins.C] = true
						selected.Args = []int{ins.A, ins.B, low.A, low.B}
					} else {
						schedule(ins.C)
						if ops[ins.C].Op == ir.OpCarryAdd || ops[ins.C].Op == ir.OpBorrowSub {
							selected.Imm = 1
						}
					}
					program.Instructions = append(program.Instructions, selected)
				}
			} else {
				if ins.Op == ir.OpAddWithCarry || ins.Op == ir.OpSubWithBorrow {
					schedule(ins.C)
				}
				appendWordCarryFallback(&program, id, ins.A, ins.B, ins.C, ins.Op, width, target.RegisterBits())
			}
		case ir.OpLeadingZeros, ir.OpTrailingZeros:
			schedule(ins.A)
			width := selectedInstructionBits(ins)
			if capability, ok := target.(interface{ CanCountZeros(ir.Opcode, uint8) bool }); ok && capability.CanCountZeros(ins.Op, width) {
				program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedCountZeros, Arithmetic: ins.Op, Bits: width, Dst: id, A: ins.A})
			} else {
				appendCountZerosFallback(&program, id, ins.A, ins.Op, width, target.RegisterBits())
			}

		case ir.OpMulHigh:
			schedule(ins.A)
			schedule(ins.B)
			width := selectedInstructionBits(ins)
			if capability, ok := target.(interface{ CanMulHigh(uint8) bool }); ok && capability.CanMulHigh(width) {
				program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedMulHigh, Bits: width, Dst: id, A: ins.A, B: ins.B})
			} else {
				appendMulHighFallback(&program, id, ins.A, ins.B, width, target.RegisterBits())
			}
		case ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy:
			schedule(ins.A)
			schedule(ins.B)
			operation, _ := ir.ShiftByOperation(ins.Op)
			width := selectedInstructionBits(ins)
			if capability, ok := target.(interface{ CanShiftBy(uint8) bool }); ok && capability.CanShiftBy(width) {
				program.Instructions = append(program.Instructions, SelectedInstruction{
					Op: SelectedShiftBy, Arithmetic: operation, Bits: width, Dst: id, A: ins.A, B: ins.B,
				})
			} else {
				appendShiftByFallback(&program, id, ins.A, ins.B, operation, width, selectedInstructionBits(ops[ins.B]), target.RegisterBits())
			}
		case ir.OpShiftRight, ir.OpShiftLeft, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight:
			schedule(ins.A)
			width := selectedInstructionBits(ins)
			if (target.Architecture() == "arm64" || target.Architecture() == "386") && width < 32 &&
				(ins.Op == ir.OpRotateLeft || ins.Op == ir.OpRotateRight) {
				if ins.Imm == 0 {
					program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedMove, Dst: id, A: ins.A})
					return
				}
				left, right, combined := newRegisterValue(), newRegisterValue(), newRegisterValue()
				leftCount, rightCount := uint64(width)-ins.Imm, ins.Imm
				if ins.Op == ir.OpRotateLeft {
					leftCount, rightCount = ins.Imm, uint64(width)-ins.Imm
				}
				program.Instructions = append(program.Instructions,
					SelectedInstruction{Op: SelectedShift, Arithmetic: ir.OpShiftLeft, Bits: width, Dst: left, A: ins.A, Imm: leftCount},
					SelectedInstruction{Op: SelectedShift, Arithmetic: ir.OpShiftRight, Bits: width, Dst: right, A: ins.A, Imm: rightCount},
					SelectedInstruction{Op: SelectedBinary, Arithmetic: ir.OpOr, Bits: width, Dst: combined, A: left, B: right},
					SelectedInstruction{Op: SelectedNarrow, Dst: id, A: combined, Imm: uint64(width)},
				)
				return
			}
			dst := id
			if width < target.RegisterBits() {
				dst = newRegisterValue()
			}
			program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedShift, Arithmetic: ins.Op, Bits: width, Dst: dst, A: ins.A, Imm: ins.Imm})
			if dst != id {
				program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedNarrow, Dst: id, A: dst, Imm: uint64(width)})
			}
		case ir.OpCastUnsigned:
			schedule(ins.A)
			if selectedInstructionBits(ins) < selectedInstructionBits(ops[ins.A]) {
				program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedNarrow, Dst: id, A: ins.A, Imm: uint64(selectedInstructionBits(ins))})
			} else {
				program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedMove, Bits: selectedInstructionBits(ins), Dst: id, A: ins.A})
			}
		case ir.OpSignExtend:
			schedule(ins.A)
			program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedSignExtend, Dst: id, A: ins.A, Imm: ins.Imm})
		case ir.OpAdd:
			if target.CanMulAdd() {
				for _, pair := range [][2]int{{ins.A, ins.B}, {ins.B, ins.A}} {
					mulID, addID := pair[0], pair[1]
					if ops[mulID].Op != ir.OpMul || refs[mulID] != 1 || scheduled[mulID] {
						continue
					}
					product := ops[mulID]
					schedule(addID)
					schedule(product.A)
					schedule(product.B)
					dst := id
					width := selectedInstructionBits(ins)
					if width < target.RegisterBits() {
						dst = newRegisterValue()
					}
					program.Instructions = append(program.Instructions, SelectedInstruction{
						Op: SelectedMulAdd, Bits: width, Dst: dst, A: addID, B: product.A, C: product.B,
					})
					if dst != id {
						program.Instructions = append(program.Instructions, SelectedInstruction{
							Op: SelectedNarrow, Dst: id, A: dst, Imm: uint64(width),
						})
					}
					return
				}
			}
			for _, pair := range [][2]int{{ins.A, ins.B}, {ins.B, ins.A}} {
				scaledID, constID := pair[0], pair[1]
				if ops[scaledID].Op != ir.OpAddScaled || !numericConst(constID) || refs[scaledID] != 1 || scheduled[scaledID] {
					continue
				}
				scaled := ops[scaledID]
				schedule(scaled.A)
				if scaled.Imm != 0 {
					schedule(scaled.B)
				}
				appendScaled(id, scaled.A, scaled.B, scaled.Imm, ops[constID].Imm)
				return
			}
			width := selectedInstructionBits(ins)
			if (target.Architecture() == "amd64" && width == 64 || target.Architecture() == "386" && width == 32) &&
				((numericConst(ins.A) && ops[ins.A].Imm == 1) || (numericConst(ins.B) && ops[ins.B].Imm == 1)) {
				source := ins.A
				if numericConst(ins.A) && ops[ins.A].Imm == 1 {
					source = ins.B
				}
				schedule(source)
				program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedInc, Dst: id, A: source})
				return
			}
			fallthrough
		case ir.OpSub:
			if ins.Op == ir.OpSub && (target.Architecture() != "386" && selectedInstructionBits(ins) == 64 ||
				target.Architecture() == "386" && selectedInstructionBits(ins) == 32) && numericConst(ins.B) && ops[ins.B].Imm == 1 {
				schedule(ins.A)
				dst := id
				if selectedInstructionBits(ins) < target.RegisterBits() {
					dst = newRegisterValue()
				}
				program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedDec, Dst: dst, A: ins.A})
				if dst != id {
					program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedNarrow, Dst: id, A: dst, Imm: uint64(selectedInstructionBits(ins))})
				}
				return
			}
			fallthrough
		case ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor:

			if ins.Op == ir.OpAnd {
				if capability, ok := target.(interface{ CanAndNot(uint8) bool }); ok && capability.CanAndNot(selectedInstructionBits(ins)) {
					for _, pair := range [][2]int{{ins.A, ins.B}, {ins.B, ins.A}} {
						inv, plain := pair[0], pair[1]
						node := ops[inv]
						if node.Op != ir.OpXor || refs[inv] != 1 || scheduled[inv] {
							continue
						}
						for _, args := range [][2]int{{node.A, node.B}, {node.B, node.A}} {
							if !numericConst(args[1]) || ops[args[1]].Imm != verifyIntegerMask(selectedInstructionBits(ins)) {
								continue
							}
							schedule(plain)
							schedule(args[0])
							program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedAndNot, Bits: selectedInstructionBits(ins), Dst: id, A: plain, B: args[0]})
							return
						}
					}
				}
			}
			if ins.Op != ir.OpMul {
				width := selectedInstructionBits(ins)
				pairs := [][2]int{{ins.A, ins.B}}
				if ins.Op != ir.OpSub {
					pairs = append(pairs, [2]int{ins.B, ins.A})
				}
				for _, pair := range pairs {
					if !numericConst(pair[1]) {
						continue
					}
					if ops[pair[1]].Imm == 0 && (ins.Op == ir.OpAdd || ins.Op == ir.OpSub) {
						schedule(pair[0])
						program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedMove, Arithmetic: ins.Op, Bits: width, Dst: id, A: pair[0]})
						return
					}
					immediate, ok := selectBinaryImmediate(ins.Op, width, ops[pair[1]].Imm)
					if !ok {
						continue
					}
					schedule(pair[0])
					dst := id
					zeroExtended := immediateZeroExtends(ins.Op, width, immediate)
					if width < target.RegisterBits() && !zeroExtended && (ins.Op == ir.OpAdd || ins.Op == ir.OpSub ||
						(immediate>>63 != 0 && (ins.Op == ir.OpOr || ins.Op == ir.OpXor))) {
						dst = newRegisterValue()
					}
					program.Instructions = append(program.Instructions, SelectedInstruction{
						Op: SelectedBinaryImmediate, Arithmetic: ins.Op, Bits: width, ZeroExtended: zeroExtended,
						Dst: dst, A: pair[0], B: pair[1], Imm: immediate,
					})
					if dst != id {
						program.Instructions = append(program.Instructions, SelectedInstruction{
							Op: SelectedNarrow, Dst: id, A: dst, Imm: uint64(width),
						})
					}
					return
				}
			}
			schedule(ins.A)
			schedule(ins.B)
			dst := id
			if selectedInstructionBits(ins) < target.RegisterBits() && (ins.Op == ir.OpAdd || ins.Op == ir.OpSub || ins.Op == ir.OpMul) {
				dst = newRegisterValue()
			}
			program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedBinary, Arithmetic: ins.Op, Bits: selectedInstructionBits(ins), Dst: dst, A: ins.A, B: ins.B})
			if dst != id {
				program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedNarrow, Dst: id, A: dst, Imm: uint64(selectedInstructionBits(ins))})
			}
		case ir.OpSelect:
			schedule(ins.A)
			schedule(ins.B)
			schedule(ins.C)
			program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedSelect, Dst: id, A: ins.A, B: ins.B, C: ins.C})
		case ir.OpLoad64:
			if base, index, scale, disp, ok := foldAddress(ins.A); ok {
				schedule(base)
				schedule(index)
				program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedLoadAddress, Dst: id, A: base, B: index, Imm: scale, Disp: disp, ByteOrder: ins.ByteOrder})
				return
			}
			if base, disp, ok := foldBaseOffset(ins.A); ok {
				schedule(base)
				program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedLoadOffset, Dst: id, A: base, Disp: disp, ByteOrder: ins.ByteOrder})
				return
			}
			schedule(ins.A)
			program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedLoad64, Dst: id, A: ins.A, ByteOrder: ins.ByteOrder})
		case ir.OpLoad8:
			schedule(ins.A)
			program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedLoad8, Dst: id, A: ins.A})
		case ir.OpStore64:
			if base, index, scale, disp, ok := foldAddress(ins.A); ok {
				schedule(base)
				schedule(index)
				schedule(ins.B)
				program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedStoreAddress, Dst: -1, A: base, B: index, C: ins.B, Imm: scale, Disp: disp, ByteOrder: ins.ByteOrder})
				return
			}
			if base, disp, ok := foldBaseOffset(ins.A); ok {
				schedule(base)
				schedule(ins.B)
				program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedStoreOffset, Dst: -1, A: base, B: ins.B, Disp: disp, ByteOrder: ins.ByteOrder})
				return
			}
			schedule(ins.A)
			schedule(ins.B)
			program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedStore64, Dst: -1, A: ins.A, B: ins.B, ByteOrder: ins.ByteOrder})
		case ir.OpStore8:
			schedule(ins.A)
			schedule(ins.B)
			program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedStore8, Dst: -1, A: ins.A, B: ins.B})
		case ir.OpSyscall:
			for _, input := range ins.Args {
				schedule(input)
			}
			program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedSyscall, Dst: id, Args: append([]int(nil), ins.Args...)})
		case ir.OpCallC, ir.OpCallGo:
			for _, input := range ins.Args {
				schedule(input)
			}
			kind := SelectedCallC
			if ins.Op == ir.OpCallGo {
				kind = SelectedCallGo
			}
			program.Instructions = append(program.Instructions, SelectedInstruction{Op: kind, Dst: id, Args: append([]int(nil), ins.Args...), Effect: ins.Effect})
		}
	}
	lastPosition := 0
	for _, leave := range leaves {
		if leave.Position < lastPosition || leave.Position > len(ops) {
			return SelectedProgram{}, regalloc.ErrInvalidFunction
		}
		lastPosition = leave.Position
	}
	leaveIndex := 0
	boundaries := make([]int, len(ops)+1)
	for position := 0; position <= len(ops); position++ {
		for leaveIndex < len(leaves) && leaves[leaveIndex].Position == position {
			leave := leaves[leaveIndex]
			for _, id := range leave.Exports {
				if id < 0 || id >= len(ops) {
					return SelectedProgram{}, regalloc.ErrInvalidFunction
				}
				// Leave is the handoff boundary for values escaping this scope.
				// Schedule them here, before later users can occupy their homes.
				if reachable[id] {
					schedule(id)
				}
			}
			leaveIndex++
		}
		boundaries[position] = len(program.Instructions)
		if position < len(ops) && (ops[position].Op == ir.OpGet || ops[position].Op == ir.OpSet || ops[position].Op == ir.OpScopeEnter || ops[position].Op == ir.OpScopeLeave || ops[position].Op == ir.OpVLoad || ops[position].Op == ir.OpVStore || ops[position].Op == ir.OpLoad64 || ops[position].Op == ir.OpLoad8 || ops[position].Op == ir.OpStore64 || ops[position].Op == ir.OpStore8 || ops[position].Op == ir.OpSyscall || ops[position].Op == ir.OpCallC || ops[position].Op == ir.OpCallGo) {
			schedule(position)
		}
	}
	schedule(result)
	for _, id := range exports {
		schedule(id)
	}
	program.Scopes = append(program.Scopes, SelectedScope{ID: 0, Parent: -1, End: len(program.Instructions)})
	presentScopes := make(map[int]bool, len(leaves))
	for _, leave := range leaves {
		presentScopes[leave.ID] = true
	}
	for _, leave := range leaves {
		if leave.Enter < 0 || leave.Enter > leave.Position || leave.ID <= 0 {
			return SelectedProgram{}, regalloc.ErrInvalidFunction
		}
		parent := leave.Parent
		if !presentScopes[parent] {
			parent = 0
		}
		program.Scopes = append(program.Scopes, SelectedScope{
			ID: leave.ID, Parent: parent,
			Start: boundaries[leave.Enter], End: boundaries[leave.Position],
		})
	}
	nativeOrder := target.NativeByteOrder()
	if nativeOrder != ir.ByteOrderLittleEndian && nativeOrder != ir.ByteOrderBigEndian {
		return SelectedProgram{}, regalloc.ErrInvalidFunction
	}
	if err := verifySelectedProgram(ops, program, result, maxArgs, target.RegisterBits(), nativeOrder); err != nil {
		return SelectedProgram{}, err
	}
	return program, nil
}
