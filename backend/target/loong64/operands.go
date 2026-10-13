package loong64

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// operandField names a machine.Instruction field that an opcode reads.
type operandField uint32

const (
	fieldA operandField = 1 << iota
	fieldB
	fieldC
	fieldD
	fieldE
	fieldHighA
	fieldHighB
	fieldHighC
	fieldImm
	fieldDisp
	fieldBits
	fieldArithmetic
	fieldCondition
	fieldComparison
	fieldBarrier
	fieldCarryMode
	fieldFlagRequest
	fieldSlot
	fieldCallArea
	fieldLong
	fieldFar
	fieldIndexed
	fieldPreserveFlags
	fieldSymbol
	fieldData
	fieldTable
)

const (
	fieldsAB   = fieldA | fieldB
	fieldsABC  = fieldA | fieldB | fieldC
	fieldsABCD = fieldA | fieldB | fieldC | fieldD
)

// operandFields lists the fields each opcode reads; FORMS.md documents them.
// Size functions and encoders ignore every other field, so a stray value
// there changes neither the size nor the bytes, while an invalid value in a
// listed field makes both reject the instruction. Byte order is checked for
// every opcode, as machine.Visit does.
var operandFields = [...]operandField{
	machine.OpConst:            fieldA | fieldImm,
	machine.OpSymbol:           fieldA | fieldSymbol,
	machine.OpMove:             fieldsAB,
	machine.OpBinary:           fieldsABC | fieldBits | fieldArithmetic | fieldPreserveFlags,
	machine.OpBinaryImmediate:  fieldsAB | fieldBits | fieldImm | fieldArithmetic | fieldPreserveFlags,
	machine.OpBinaryCarry:      fieldsABC | fieldArithmetic,
	machine.OpInc:              fieldsAB | fieldPreserveFlags,
	machine.OpDec:              fieldsAB | fieldPreserveFlags,
	machine.OpNarrow:           fieldsAB | fieldImm,
	machine.OpSignExtend:       fieldsAB | fieldImm,
	machine.OpShift:            fieldsAB | fieldBits | fieldImm | fieldArithmetic,
	machine.OpScaled:           fieldsABC | fieldImm | fieldDisp,
	machine.OpMulAdd:           fieldsABCD,
	machine.OpSelect:           fieldsABCD,
	machine.OpSelectFlags:      fieldA | fieldC | fieldD | fieldCondition,
	machine.OpLoad64:           fieldsAB,
	machine.OpStore64:          fieldsAB,
	machine.OpLoad8:            fieldsAB,
	machine.OpStore8:           fieldsAB,
	machine.OpLoadAddress:      fieldsABC | fieldImm | fieldDisp,
	machine.OpStoreAddress:     fieldsABC | fieldImm | fieldDisp,
	machine.OpLoadOffset:       fieldsAB | fieldDisp,
	machine.OpStoreOffset:      fieldsAB | fieldDisp,
	machine.OpSaveLink:         fieldSlot,
	machine.OpRestoreLink:      fieldSlot,
	machine.OpSpill:            fieldA | fieldSlot,
	machine.OpReload:           fieldA | fieldSlot,
	machine.OpLoadIncoming:     fieldA | fieldImm,
	machine.OpFrameEnter:       fieldImm | fieldCallArea,
	machine.OpFrameLeave:       fieldImm,
	machine.OpCompareImmediate: fieldA | fieldImm | fieldBits | fieldComparison,
	machine.OpCompareRegister:  fieldsAB | fieldBits | fieldComparison,
	machine.OpJump:             fieldImm | fieldLong,
	machine.OpJumpIf:           fieldImm | fieldCondition | fieldLong | fieldFar,
	machine.OpBranchZero:       fieldA | fieldImm | fieldBits | fieldCondition | fieldLong | fieldFar,
	machine.OpBranchBit:        fieldA | fieldImm | fieldDisp | fieldBits | fieldCondition | fieldLong,
	machine.OpLoop:             fieldImm,
	machine.OpMulWide:          fieldA,
	machine.OpSaveCFloat:       fieldA | fieldSlot,
	machine.OpRestoreCFloat:    fieldA | fieldSlot,
	machine.OpSaveCVector:      fieldA | fieldSlot,
	machine.OpRestoreCVector:   fieldA | fieldSlot,
	machine.OpSaveCondition:    fieldA | fieldSlot,
	machine.OpRestoreCondition: fieldA | fieldSlot,
	machine.OpFloatBinary:      fieldsABC | fieldBits | fieldArithmetic,
	machine.OpFloatFMA:         fieldsABCD | fieldBits,
	machine.OpFloatUnary:       fieldsAB | fieldBits | fieldArithmetic,
	machine.OpSetCount:         fieldA,
	machine.OpShiftBy:          fieldsABC | fieldBits | fieldArithmetic,
	machine.OpMulHigh:          fieldsABC | fieldBits,
	machine.OpCarryBit:         fieldsABC | fieldBits | fieldArithmetic | fieldCarryMode,
	machine.OpWordCarry:        fieldsABC | fieldD | fieldE | fieldBits | fieldArithmetic | fieldCarryMode,
	machine.OpVector:           fieldsABC | fieldBits | fieldImm | fieldDisp | fieldArithmetic | fieldIndexed,
	machine.OpVectorMove:       fieldsAB,
	machine.OpVectorSpill:      fieldA | fieldSlot,
	machine.OpVectorReload:     fieldA | fieldSlot,
	machine.OpCountZeros:       fieldsAB | fieldBits | fieldArithmetic,
	machine.OpAndNot:           fieldsABC | fieldBits,
	machine.OpFloatMove:        fieldsAB | fieldBits,
	machine.OpLiteralLoad:      fieldA | fieldBits | fieldDisp,
	machine.OpLiteralData:      fieldData,
	machine.OpFloatSpill:       fieldA | fieldBits | fieldSlot,
	machine.OpFloatReload:      fieldA | fieldBits | fieldSlot,
	machine.OpFloatBits:        fieldsAB | fieldBits | fieldArithmetic,
	machine.OpLoad16:           fieldsAB,
	machine.OpStore16:          fieldsAB,
	machine.OpLoad32:           fieldsAB,
	machine.OpStore32:          fieldsAB,
	machine.OpBarrier:          fieldBarrier,
	machine.OpDivide:           fieldsABC | fieldBits | fieldImm | fieldArithmetic,
	machine.OpIntToFloat:       fieldsAB | fieldBits | fieldImm | fieldArithmetic,
	machine.OpFloatToInt:       fieldsAB | fieldBits | fieldImm | fieldDisp | fieldArithmetic,
	machine.OpMulHighSigned:    fieldsABC | fieldBits,
	machine.OpFloatCompare:     fieldsABC | fieldBits | fieldImm,
	machine.OpSlotAddress:      fieldA | fieldSlot,
	// Prefetch (§10).
	machine.OpPrefetch: fieldA | fieldImm,
	// Tail calls (§9).
	machine.OpTailCall:  fieldImm,
	machine.OpJumpTable: fieldsAB | fieldTable,
	// w7 cmp
	machine.OpCompareSet:   fieldsABC | fieldBits | fieldCondition,
	machine.OpReverseBytes: fieldsAB | fieldBits,
	machine.OpAtomic:       fieldsABCD | fieldBits | fieldImm | fieldArithmetic,
}

// clearUnused zeroes the fields i's opcode does not read.
func clearUnused(i *machine.Instruction) {
	if int(i.Op) >= len(operandFields) {
		return
	}
	used := operandFields[i.Op]
	if used&fieldA == 0 {
		i.A = 0
	}
	if used&fieldB == 0 {
		i.B = 0
	}
	if used&fieldC == 0 {
		i.C = 0
	}
	if used&fieldD == 0 {
		i.D = 0
	}
	if used&fieldE == 0 {
		i.E = 0
	}
	if used&fieldHighA == 0 {
		i.HighA = 0
	}
	if used&fieldHighB == 0 {
		i.HighB = 0
	}
	if used&fieldHighC == 0 {
		i.HighC = 0
	}
	if used&fieldImm == 0 {
		i.Imm = 0
	}
	if used&fieldDisp == 0 {
		i.Disp = 0
	}
	if used&fieldBits == 0 {
		i.Bits = 0
	}
	if used&fieldArithmetic == 0 {
		i.Arithmetic = 0
	}
	if used&fieldCondition == 0 {
		i.Condition = 0
	}
	if used&fieldComparison == 0 {
		i.Comparison = 0
	}
	if used&fieldBarrier == 0 {
		i.Barrier = 0
	}
	if used&fieldCarryMode == 0 {
		i.CarryMode = 0
	}
	if used&fieldFlagRequest == 0 {
		i.FlagRequest = 0
	}
	if used&fieldSlot == 0 {
		i.Slot = 0
	}
	if used&fieldCallArea == 0 {
		i.CallAreaBytes = 0
	}
	if used&fieldLong == 0 {
		i.Long = false
	}
	if used&fieldFar == 0 {
		i.Far = false
	}
	if used&fieldIndexed == 0 {
		i.Indexed = false
	}
	if used&fieldPreserveFlags == 0 {
		i.PreserveFlags = false
	}
	if used&fieldSymbol == 0 {
		i.Symbol = ""
	}
	if used&fieldData == 0 {
		i.Data = nil
	}
	if used&fieldTable == 0 {
		i.Table = nil
	}
	i.VectorType = abi.ValueType{} // no encoder reads it
}
