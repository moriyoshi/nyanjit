package arm64

import (
	"math/bits"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// The first word of a framed function is reserved for the incoming link
// register. The Go arm64 unwinder reads it from 0(SP) while a callee runs.
// Ordinary spill slots start at 8(SP), even in a leaf with a spill, so a
// shared control-flow frame uses the same slot numbering on every path. The
// top word is the caller's saved frame pointer (Go's extrasize); it is never
// available to spills or outgoing argument homes.
func FrameBytes(slots int, hasCall bool) int {
	if slots < 0 || uint64(slots) > uint64(^uint(0)>>1)/16-2 {
		return 0
	}
	if slots == 0 && !hasCall {
		return 0
	}
	return ((slots+2)*8 + 15) &^ 15
}

func AdjustSize(frame int) int {
	if frame < 0 || frame > 1<<20 {
		return 0
	}
	return ((frame + 4079) / 4080) * 4
}

// validFrame reports whether frame is a 16-byte aligned size of at most 1 MiB;
// SP must stay aligned for every memory access through it.
func validFrame(frame int) bool { return frame >= 0 && frame <= 1<<20 && frame%16 == 0 }

func FrameEnterSize(frame int) int {
	if !validFrame(frame) {
		return 0
	}
	if frame <= 240 {
		return 12 // pre-indexed LR store, saved FP, updated FP
	}
	return AdjustSize(frame) + 20 // X17, LR, FP, SP, and FP updates
}

func FrameLeaveSize(frame int) int {
	if !validFrame(frame) {
		return 0
	}
	if frame <= 240 {
		return 8 // saved FP and post-indexed LR load
	}
	return AdjustSize(frame) + 8 // saved FP and LR loads
}

func FlagEffect(ins machine.Instruction) bool {
	switch ins.Op {
	case machine.OpCarryBit, machine.OpWordCarry, machine.OpShiftBy, machine.OpDec, machine.OpSelect, machine.OpSyscall, machine.OpCallC, machine.OpCallGo, machine.OpCompareImmediate, machine.OpCompareRegister, machine.OpFloatCompare,
		machine.OpJumpTable, machine.OpCompareSet:
		return true
	case machine.OpDivide:
		return divisionWritesFlags(ins)
	case machine.OpAtomic:
		return atomicWritesFlags(ins)
	default:
		return false
	}
}

type ImmediatePart struct {
	Op    string
	Imm   uint16
	Shift uint8
}

// ImmediatePlan returns a caller-owned expansion. Internal visitors use a
// fixed-capacity value plan, avoiding heap storage for size and byte queries.
func ImmediatePlan(value uint64) []ImmediatePart {
	p := immediatePlan(value)
	out := make([]ImmediatePart, p.n, 4)
	for i, part := range p.parts[:p.n] {
		out[i] = ImmediatePart{Op: part.op.String(), Imm: part.imm, Shift: part.shift}
	}
	return out
}

// immediateOp is a wide move of an immediate plan; its value is the
// instruction's opc field.
type immediateOp uint8

const (
	immediateMovN immediateOp = 0
	immediateMovZ immediateOp = 2
	immediateMovK immediateOp = 3
)

func (o immediateOp) String() string {
	switch o {
	case immediateMovN:
		return "movn"
	case immediateMovZ:
		return "movz"
	}
	return "movk"
}

// word encodes the 64-bit wide move of part into rd.
func (part immediateStep) word(rd uint32) uint32 {
	return 0x92800000 | uint32(part.op)<<29 | uint32(part.shift/16)<<21 | uint32(part.imm)<<5 | rd
}

type immediateStep struct {
	op    immediateOp
	imm   uint16
	shift uint8
}

type immediateParts struct {
	parts [4]immediateStep
	n     int
}

// nonzeroHalves counts the 16-bit halves of v that are not zero. Folding
// each half onto its lowest bit keeps the halves apart.
func nonzeroHalves(v uint64) int {
	v |= v >> 8
	v |= v >> 4
	v |= v >> 2
	v |= v >> 1
	return bits.OnesCount64(v & 0x0001_0001_0001_0001)
}

// immediateCount returns immediatePlan(value).n without building the plan.
func immediateCount(value uint64) int {
	return max(1, min(nonzeroHalves(value), nonzeroHalves(^value)))
}

func immediatePlan(value uint64) immediateParts {
	var halves [4]uint16
	zeros, ones := 0, 0
	for i := range halves {
		halves[i] = uint16(value >> (16 * i))
		if halves[i] != 0 {
			zeros++
		}
		if halves[i] != 0xffff {
			ones++
		}
	}
	useMovN := ones < zeros
	var parts immediateParts
	for i, half := range halves {
		if useMovN && half == 0xffff || !useMovN && half == 0 {
			continue
		}
		part := immediateStep{op: immediateMovK, imm: half, shift: uint8(16 * i)}
		if parts.n == 0 {
			if useMovN {
				part.op, part.imm = immediateMovN, ^half
			} else {
				part.op = immediateMovZ
			}
		}
		parts.parts[parts.n] = part
		parts.n++
	}
	if parts.n == 0 {
		op := immediateMovZ
		if useMovN {
			op = immediateMovN
		}
		parts.parts[parts.n] = immediateStep{op: op}
		parts.n++
	}
	return parts
}

// PhysicalSize measures instructions for the stack policy and number of Go
// argument homes. Use StackNoUse and zero homes for a workspace-only layout.
// It reports 0 for an instruction the byte assembler rejects, except for a
// branch or literal target out of reach, which only layout can see. Fields
// the opcode does not read are ignored (see operandFields).
func PhysicalSize(ins machine.Instruction, stack abi.StackPractice, goHomes int) int {
	if !ins.Op.Valid() || !validVisitFields(&ins) || goHomes < 0 || goHomes > 4094 ||
		stack != abi.StackNoUse && stack != abi.StackPlatform {
		return 0
	}
	// The specialized public size routines already validate their operands
	// and clear the fields their opcode does not read. Dispatch before the
	// scalar validation pass to avoid checking FP/vector registers twice for
	// every layout query.
	switch ins.Op {
	case machine.OpVector, machine.OpVectorMove, machine.OpVectorSpill, machine.OpVectorReload:
		return VectorSize(ins, stack, goHomes)
	case machine.OpFloatBinary, machine.OpFloatUnary, machine.OpFloatFMA, machine.OpFloatMove, machine.OpFloatBits:
		return FloatSize(ins)
	case machine.OpFloatSpill, machine.OpFloatReload:
		return FloatMemorySize(ins, stack, goHomes)
	case machine.OpLiteralLoad:
		return LiteralSize(ins)
	case machine.OpIntToFloat, machine.OpFloatToInt:
		if !validFloatConvert(&ins) {
			return 0
		}
		return 4
	case machine.OpFloatCompare:
		if !validFloatCompare(&ins) {
			return 0
		}
		return floatCompareSize(op.FloatCondition(ins.Imm), ins.A)
	case machine.OpSlotAddress:
		return slotAddressSize(ins.A, ins.Slot, stack, goHomes)
	case machine.OpJumpTable:
		if !validJumpTableOperands(&ins) {
			return 0
		}
		return jumpTableSize(&ins)
	case machine.OpCompareSet:
		if !validCompareSet(ins.Condition, ins.Bits, ins.A, ins.B, ins.C) {
			return 0
		}
		return compareSetSize(ins.Condition, ins.Bits)
	case machine.OpReverseBytes:
		if !validReverseBytes(ins.Bits, ins.A, ins.B) {
			return 0
		}
		return 4
	case machine.OpAtomic:
		return atomicSize(ins, false)
	}
	// Only GP instructions and literal data remain. Validate their common
	// fields directly, then validate operands in the same dispatch that sizes
	// them. This avoids copying or redispatching the instruction through the
	// general-purpose float/vector register validator on every scalar query.
	// Each case reads only fields its opcode uses, so only the register check
	// consults operandFields instead of clearing the instruction.
	used := operandFields[ins.Op]
	if ins.A&registerMask(used, fieldA)|ins.B&registerMask(used, fieldB)|ins.C&registerMask(used, fieldC)|
		ins.D&registerMask(used, fieldD)|ins.E&registerMask(used, fieldE) >= 32 {
		return 0
	}
	switch ins.Op {
	case machine.OpBinary:
		if ins.Bits != 0 && !validWidth(ins.Bits) || !validBinary(ins.Arithmetic) {
			return 0
		}
		return 4
	case machine.OpBinaryImmediate:
		if ins.PreserveFlags && ins.A == 31 && binaryFlagBits(ins.Arithmetic) != 0 {
			return 0
		}
		switch ins.Arithmetic {
		case op.OpAnd, op.OpOr, op.OpXor:
			if _, _, ok := logicalImmediatePlan(ins.Bits, ins.Imm); !ok {
				return 0
			}
		default:
			if _, _, _, ok := addSubImmediate(ins.Arithmetic, ins.Bits, ins.Imm); !ok {
				return 0
			}
		}
		return 4
	case machine.OpBinaryCarry:
		// machine.Visit rewrites this as a 32-bit OpWordCarry on the flags.
		if ins.Arithmetic != op.OpAdd && ins.Arithmetic != op.OpSub {
			return 0
		}
		return 4
	case machine.OpShift:
		if !validWidth(ins.Bits) || !validShift(ins.Arithmetic) || ins.Imm >= uint64(ins.Bits) {
			return 0
		}
		return 4
	case machine.OpNarrow, machine.OpSignExtend:
		if ins.Imm < 1 || ins.Imm > 64 {
			return 0
		}
		return 4
	case machine.OpScaled:
		if ins.Disp != 0 || ins.Imm == 0 || ins.Imm&(ins.Imm-1) != 0 {
			return 0
		}
		return 4
	case machine.OpLoadAddress, machine.OpStoreAddress:
		if ins.Disp != 0 || ins.Imm != 1 && ins.Imm != 8 {
			return 0
		}
		return 4
	case machine.OpLoadOffset, machine.OpStoreOffset:
		if !(int64(ins.Disp) >= -256 && int64(ins.Disp) <= 255 || ins.Disp <= 4095*8 && ins.Disp%8 == 0) {
			return 0
		}
		return 4
	case machine.OpSaveLink, machine.OpRestoreLink:
		if !validSlot(ins.Slot, goHomes) || stack == abi.StackNoUse && ins.Slot > 4095 {
			return 0
		}
		return 4
	case machine.OpSpill, machine.OpReload:
		slot := ins.Slot
		if stack == abi.StackPlatform {
			slot += 1 + goHomes // LR at 0(SP), Go call homes above it.
		}
		if ins.Slot < 0 || ins.Slot > 4095 || slot > 4095 {
			return 0
		}
		return 4
	case machine.OpSaveCFloat, machine.OpRestoreCFloat:
		if ins.Slot < 0 || ins.Slot > 4095 || ins.Slot+1+goHomes > 4095 {
			return 0
		}
		return 4
	case machine.OpCountZeros:
		if !validWidth(ins.Bits) || ins.Arithmetic != op.OpLeadingZeros && ins.Arithmetic != op.OpTrailingZeros {
			return 0
		}
		return len(countZerosPlan(ins.Arithmetic, ins.Bits, ins.A, ins.B)) * 4
	case machine.OpCarryBit, machine.OpWordCarry:
		if ins.CarryMode > machine.CarryFlags || ins.Arithmetic != op.OpAdd && ins.Arithmetic != op.OpSub ||
			ins.Bits != 32 && ins.Bits != 64 {
			return 0
		}
		return wordCarrySize(ins)
	case machine.OpMulHighSigned:
		if !validWidth(ins.Bits) {
			return 0
		}
		return len(mulHighSignedWords(ins.Bits, ins.A, ins.B, ins.C)) * 4
	case machine.OpMulHigh:
		if !validWidth(ins.Bits) {
			return 0
		}
		if ins.Bits == 64 {
			return 4
		}
		return 8
	case machine.OpDivide:
		return divisionPlan(ins, false).n * 4
	case machine.OpShiftBy:
		if !validWidth(ins.Bits) || !validShift(ins.Arithmetic) {
			return 0
		}
		return len(shiftByPlan(ins.Arithmetic, ins.Bits, ins.A, ins.B, ins.C)) * 4
	case machine.OpConst:
		return immediateCount(ins.Imm) * 4
	case machine.OpSymbol:
		return 16
	case machine.OpMove:
		if ins.A == ins.B {
			return 0
		}
		return 4
	case machine.OpLiteralData:
		if len(ins.Data)%4 != 0 {
			return 0 // the assembler accepts whole words only
		}
		return len(ins.Data)
	case machine.OpBarrier:
		return BarrierSize(ins.Barrier)
	case machine.OpSelect:
		return 8
	case machine.OpSelectFlags:
		if !validBranchCondition(ins.Condition) {
			return 0
		}
		return 4
	case machine.OpFrameEnter:
		if ins.CallAreaBytes != 0 {
			return 0 // no outgoing area form
		}
		return FrameEnterSize(int(ins.Imm))
	case machine.OpFrameLeave:
		if ins.Imm > 1<<20 {
			return 0
		}
		return FrameLeaveSize(int(ins.Imm))
	case machine.OpCompareImmediate:
		if ins.Comparison > machine.CompareUnsigned || ins.Bits != 0 && ins.Bits != 64 ||
			int64(ins.Imm) < -4095 || int64(ins.Imm) > 4095 {
			return 0
		}
		return 4
	case machine.OpCompareRegister:
		if ins.Comparison > machine.CompareUnsigned || ins.Bits != 0 && ins.Bits != 64 {
			return 0
		}
		return 4
	case machine.OpJumpIf:
		if !validBranchCondition(ins.Condition) {
			return 0
		}
		if ins.Long {
			return 8
		}
		return 4
	case machine.OpBranchZero, machine.OpBranchBit:
		if ins.Condition != op.BranchEqual && ins.Condition != op.BranchNotEqual || !validWidth(ins.Bits) ||
			ins.Op == machine.OpBranchBit && ins.Disp >= uint64(ins.Bits) {
			return 0
		}
		if ins.Long {
			return 8
		}
		return 4
	case machine.OpDec, machine.OpMulAdd, machine.OpLoad64, machine.OpStore64, machine.OpLoad8, machine.OpStore8,
		machine.OpLoad16, machine.OpStore16, machine.OpLoad32, machine.OpStore32,
		machine.OpSyscall, machine.OpCallC, machine.OpCallGo, machine.OpReturn, machine.OpJump:
		return 4
	case machine.OpPrefetch:
		return 4
	case machine.OpTailCall:
		return tailCallSize(ins.Imm)
	}
	// OpInc, OpAndNot, OpLoop, OpLoadIncoming, OpSetCount, OpMulWide and
	// the C vector and condition saves have no arm64 encoding.
	return 0
}

// ImplicitWrites reports reserved expansion temporaries for arithmetic plans.
func ImplicitWrites(ins machine.Instruction) []backend.Register {
	// Other operations have no implicit arithmetic temporaries, regardless of
	// operand validity. Avoid validating instructions whose result is always nil.
	switch ins.Op {
	case machine.OpLiteralLoad, machine.OpShiftBy, machine.OpCountZeros, machine.OpCarryBit, machine.OpWordCarry, machine.OpDivide, machine.OpMulHighSigned, machine.OpAtomic:
	case machine.OpVector:
		// A shuffle longer than one instruction builds its TBL table from X16.
		if ins.Arithmetic == op.OpVShuffle && VectorSize(ins, abi.StackNoUse, 0) > 4 {
			return []backend.Register{16}
		}
		// Mask bits build their periodic weights in X16.
		if ins.Arithmetic == op.OpVMaskBits {
			return []backend.Register{16}
		}
		return nil
	case machine.OpJumpTable:
		return []backend.Register{16, 17}
	default:
		return nil
	}
	if PhysicalSize(ins, abi.StackNoUse, 0) == 0 {
		return nil // no plan
	}
	clearUnused(&ins)
	var plan []shiftByInstruction
	switch ins.Op {
	case machine.OpLiteralLoad:
		if ins.Long {
			return []backend.Register{16, 17}
		}
	case machine.OpShiftBy:
		plan = shiftByPlan(ins.Arithmetic, ins.Bits, ins.A, ins.B, ins.C)
	case machine.OpCountZeros:
		plan = countZerosPlan(ins.Arithmetic, ins.Bits, ins.A, ins.B)
	case machine.OpDivide:
		p := divisionPlan(ins, false)
		for _, i := range p.parts[:p.n] {
			plan = append(plan, shiftByInstruction{word: i.word})
		}
	case machine.OpMulHighSigned:
		if ins.Bits < 32 {
			return []backend.Register{16, 17}
		}
		return nil
	case machine.OpAtomic:
		p := atomicPlan(ins, false, false)
		for _, i := range p.parts[:p.n] {
			plan = append(plan, shiftByInstruction{word: i.word})
		}
	case machine.OpCarryBit, machine.OpWordCarry:
		if ins.CarryMode != machine.CarryFlags {
			return []backend.Register{16}
		}
	}
	var x16, x17 bool
	for _, i := range plan {
		dst := i.word & 31
		x16 = x16 || dst == 16
		x17 = x17 || dst == 17
	}
	var out []backend.Register
	if x16 {
		out = append(out, 16)
	}
	if x17 {
		out = append(out, 17)
	}
	return out
}
