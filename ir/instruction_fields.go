package ir

func integerWidthMask(bits uint8) uint64 {
	if bits >= 64 {
		return ^uint64(0)
	}
	return uint64(1)<<bits - 1
}

// Field permissions are computed once, rather than rediscovered with several
// opcode switches for every instruction in every Validate call.
type instructionFields uint32

const (
	fieldA instructionFields = 1 << iota
	fieldB
	fieldC
	fieldImm
	fieldContract
	fieldEntryReg
	fieldVariable
	fieldData
	fieldSymbol
	fieldEffect
	fieldArgs
	fieldOrder
	fieldAligned
	fieldScope
	checkConstant
	checkLoad64
	checkLoad8
	checkVariable
	checkLabelParameter
	checkParameter
)

var instructionFieldRules = func() [opcodeEnd]instructionFields {
	var rules [opcodeEnd]instructionFields
	for op := Opcode(0); op < opcodeEnd; op++ {
		flags := fieldA
		switch op {
		case OpParam, OpLabelParam, OpConst, OpGet, OpScopeEnter, OpScopeLeave, OpVConst, OpVZero, OpSyscall, OpCallC, OpCallGo:
			flags &^= fieldA
		}
		switch op {
		case OpAdd, OpSub, OpMul, OpAnd, OpOr, OpXor, OpAddScaled, OpSelect, OpFAdd, OpFSub, OpFMul, OpFDiv, OpFMA, OpFProductError, OpMulHigh, OpMulHighSigned, OpFCompare, OpCarryAdd, OpBorrowSub, OpAddWithCarry, OpSubWithBorrow, OpShiftLeftBy, OpShiftRightBy, OpShiftRightArithmeticBy, OpRotateLeftBy, OpRotateRightBy, OpStore64, OpStore8, OpStore16, OpStore32, OpVStore, OpVInsert, OpVInsertZero, OpVCompare, OpVCarrylessMul,
			OpDiv, OpDivSigned, OpRem, OpRemSigned:
			flags |= fieldB
		}
		switch op {
		case OpSelect, OpFMA, OpFProductError, OpAddWithCarry, OpSubWithBorrow:
			flags |= fieldC
		}
		switch op {
		case OpConst, OpLabelParam, OpGet, OpSet, OpScopeEnter, OpScopeLeave, OpAddScaled, OpShiftLeft, OpShiftRight, OpShiftRightArithmetic, OpRotateLeft, OpRotateRight, OpSignExtend, OpVExtract, OpVInsert, OpVInsertZero, OpVCompare, OpVCarrylessMul,
			OpDiv, OpDivSigned, OpRem, OpRemSigned, OpFToSigned, OpFToUnsigned, OpFCompare:
			flags |= fieldImm
		}
		switch op {
		case OpFAdd:
			flags |= fieldContract
		case OpLabelParam:
			flags |= fieldEntryReg
		case OpGet, OpSet:
			flags |= fieldVariable
		case OpVConst:
			flags |= fieldData
		case OpConst:
			flags |= fieldSymbol
		}
		switch op {
		case OpCallC, OpCallGo, OpSyscall:
			flags |= fieldEffect | fieldArgs
		case OpScopeEnter, OpScopeLeave:
			flags |= fieldArgs | fieldScope
		}
		switch op {
		case OpLoad64, OpStore64, OpLoad8, OpStore8:
			flags |= fieldOrder
		case OpLoad16, OpStore16, OpLoad32, OpStore32:
			flags |= fieldOrder | fieldAligned
		}
		switch op {
		case OpConst:
			flags |= checkConstant
		case OpLoad64:
			flags |= checkLoad64
		case OpLoad8:
			flags |= checkLoad8
		case OpGet, OpSet, OpScopeEnter, OpScopeLeave:
			flags |= checkVariable
		case OpLabelParam:
			flags |= checkLabelParameter
		case OpParam:
			flags |= checkParameter
		}
		rules[op] = flags
	}
	return rules
}()

// ruleInputsInto uses the already-loaded dense operand rule. Keeping this
// small avoids copying an Instruction or redispatching its opcode in each
// validation walk. The rule table and public InputsInto agree on every opcode.
func ruleInputsInto(fields instructionFields, a, b, c int, args, dst []int) []int {
	if fields&fieldArgs != 0 {
		return args
	}
	switch fields & (fieldA | fieldB | fieldC) {
	case fieldA | fieldB | fieldC:
		return append(dst, a, b, c)
	case fieldA | fieldB:
		return append(dst, a, b)
	case fieldA:
		return append(dst, a)
	default:
		return nil
	}
}

// Source IR contains semantic operands only. Physical entry registers are
// assigned by lowering after source validation, never supplied by callers.
// This runs inside the ordinary instruction-validation pass and reads the
// instruction in place, including its less common audit-sensitive fields.
func validateInstructionFields(i *Instruction, fields instructionFields) error {
	if i.A != 0 && fields&fieldA == 0 || i.B != 0 && fields&fieldB == 0 ||
		i.C != 0 && fields&fieldC == 0 || i.Imm != 0 && fields&fieldImm == 0 {
		return invalidInstructionField(i.Op, "operand/immediate")
	}
	if i.Contract && fields&fieldContract == 0 {
		return invalidInstructionField(i.Op, "Contract")
	}
	if i.EntryReg != 0 && (fields&fieldEntryReg == 0 || i.EntryReg != -1) {
		return invalidInstructionField(i.Op, "EntryReg")
	}
	if i.Variable != 0 && fields&fieldVariable == 0 {
		return invalidInstructionField(i.Op, "Variable")
	}
	if len(i.Data) != 0 && fields&fieldData == 0 {
		return invalidInstructionField(i.Op, "Data")
	}
	if i.Symbol != "" && fields&fieldSymbol == 0 {
		return invalidInstructionField(i.Op, "Symbol")
	}
	if i.Effect != nil && fields&fieldEffect == 0 {
		return invalidInstructionField(i.Op, "Effect")
	}
	if len(i.Args) != 0 && fields&fieldArgs == 0 {
		return invalidInstructionField(i.Op, "Args")
	}
	if i.Aligned && fields&fieldAligned == 0 {
		return invalidInstructionField(i.Op, "Aligned")
	}
	if !i.ByteOrder.Valid() || i.ByteOrder != ByteOrderNative && fields&fieldOrder == 0 ||
		i.ByteOrder == ByteOrderBigEndian && (i.Op == OpLoad64 || i.Op == OpStore64) {
		return invalidInstructionField(i.Op, "ByteOrder")
	}
	if fields&fieldScope != 0 && (i.Type != (Type{}) || i.Bits != 0) {
		return invalidInstructionField(i.Op, "scope marker shape")
	}
	return nil
}

// invalidInstructionField reports a field rule; the caller locates it with
// atInstruction.
func invalidInstructionField(op Opcode, field string) error {
	return &ValidationError{Index: -1, Op: op, Rule: "unused or invalid " + field}
}
