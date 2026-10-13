package riscv64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// require records an encoding error; invalid assemblers expose no machine bytes.
func (e *ByteAssembler) require(ok bool) bool {
	if !ok && e.err == nil {
		e.err = machine.ErrInvalidInstruction
	}
	return ok && e.err == nil
}

func (e *ByteAssembler) displacement(from, to, bits, alignment int) bool {
	if from < 0 || to < 0 {
		return e.require(false)
	}
	delta := int64(to) - int64(from)
	return e.require(delta%int64(alignment) == 0 && delta >= -(int64(1)<<(bits-1)) && delta < int64(1)<<(bits-1))
}

func validGeneralRegisters(regs ...backend.Register) bool {
	for _, r := range regs {
		if r >= 32 {
			return false
		}
	}
	return true
}
func validVectorRegister(r backend.Register) bool {
	return backend.IsVectorRegister(r) && backend.VectorNumber(r) < 32
}
func validFloatRegisters(regs ...backend.Register) bool {
	for _, r := range regs {
		if !validGeneralRegisters(r) && !validVectorRegister(r) {
			return false
		}
	}
	return true
}
func validWidth(w uint8) bool { return w == 8 || w == 16 || w == 32 || w == 64 }
func validBinary(operation op.Opcode) bool {
	return operation == op.OpAdd || operation == op.OpSub || operation == op.OpMul || operation == op.OpAnd || operation == op.OpOr || operation == op.OpXor
}
func validShift(operation op.Opcode) bool {
	return operation == op.OpShiftLeft || operation == op.OpShiftRight || operation == op.OpShiftRightArithmetic || operation == op.OpRotateLeft || operation == op.OpRotateRight
}

// Bound slot arithmetic before any addition/multiplication or narrowing to an
// ISA displacement. Expanded address materialization still accepts large slots.
func validSlot(slot, homes int) bool {
	limit := uint64(^uint(0)>>1) / 16
	return slot >= 0 && homes >= 0 && uint64(homes)+2 <= limit && uint64(slot) <= limit-uint64(homes)-2
}

func validInstructionRegisters(i machine.Instruction) bool {
	if !i.ByteOrder.Valid() {
		return false
	}
	switch i.Op {
	case machine.OpLiteralData:
		return true
	case machine.OpVector, machine.OpVectorMove, machine.OpVectorSpill, machine.OpVectorReload:
		return validVectorOperands(i)
	case machine.OpFloatSpill, machine.OpFloatReload:
		return validVectorRegister(i.A) && (i.Bits == 0 || i.Bits == 32 || i.Bits == 64) && validSlot(i.Slot, 0)
	case machine.OpLiteralLoad:
		return validVectorRegister(i.A) && (i.Bits == 32 || i.Bits == 64)
	case machine.OpFloatMove:
		return validFloatRegisters(i.A, i.B) && (validVectorRegister(i.A) || validVectorRegister(i.B))
	case machine.OpFloatBits, machine.OpFloatUnary:
		return validFloatRegisters(i.A, i.B)
	case machine.OpFloatBinary:
		return validFloatRegisters(i.A, i.B, i.C)
	case machine.OpFloatFMA:
		return validFloatRegisters(i.A, i.B, i.C, i.D)
	case machine.OpIntToFloat:
		return validVectorRegister(i.A) && validGeneralRegisters(i.B, i.C, i.D, i.E)
	case machine.OpFloatToInt:
		return validVectorRegister(i.B) && validGeneralRegisters(i.A, i.C, i.D, i.E)
	case machine.OpFloatCompare:
		return validVectorRegister(i.B) && validVectorRegister(i.C) && validGeneralRegisters(i.A, i.D, i.E)
	case machine.OpCarryBit, machine.OpWordCarry:
		// There are no carry flags to consume.
		return validGeneralRegisters(i.A, i.B, i.C, i.D, i.E) && i.CarryMode <= machine.CarryOperands && (i.Arithmetic == op.OpAdd || i.Arithmetic == op.OpSub) && (i.Bits == 64)
	}
	return validGeneralRegisters(i.A, i.B, i.C, i.D, i.E)
}
func validVectorOperands(i machine.Instruction) bool {
	gp, vec := validGeneralRegisters, validVectorRegister
	switch i.Op {
	case machine.OpVectorMove:
		return vec(i.A) && vec(i.B)
	case machine.OpVectorSpill, machine.OpVectorReload:
		return vec(i.A) && validSlot(i.Slot, 0)
	case machine.OpVector:
	default:
		return false
	}
	switch i.Arithmetic {
	case op.OpVLoad:
		return vec(i.A) && gp(i.B) && (!i.Indexed || gp(i.C))
	case op.OpVStore:
		return gp(i.A) && vec(i.B) && (!i.Indexed || gp(i.C))
	case op.OpVZero:
		return vec(i.A)
	case op.OpVMaskBits:
		return gp(i.A) && vec(i.B)
	}
	if !validWidth(i.Bits) {
		return false
	}
	switch i.Arithmetic {
	case op.OpVSplat:
		return vec(i.A) && gp(i.B)
	case op.OpVExtract:
		return gp(i.A) && vec(i.B) && i.Imm < uint64(128/i.Bits)
	case op.OpVInsert, op.OpVInsertZero:
		return vec(i.A) && vec(i.B) && gp(i.C) && i.Imm < uint64(128/i.Bits)
	case op.OpShiftLeft, op.OpShiftRight, op.OpShiftRightArithmetic, op.OpRotateLeft, op.OpRotateRight:
		return vec(i.A) && vec(i.B)
	case op.OpShiftLeftBy, op.OpShiftRightBy, op.OpShiftRightArithmeticBy, op.OpRotateLeftBy, op.OpRotateRightBy:
		return vec(i.A) && vec(i.B) && (vec(i.C) || gp(i.C))
	case op.OpVCarrylessMul:
		return i.Bits == 64 && i.Imm < 4 && vec(i.A) && vec(i.B) && vec(i.C)
	case op.OpAdd, op.OpSub, op.OpMul, op.OpAnd, op.OpOr, op.OpXor, op.OpVCompare:
		return vec(i.A) && vec(i.B) && vec(i.C)
	}
	return false
}

// validSizeFields protects the public size/plan entry points before they enter
// expansions or narrow an immediate. Zero width is the legacy native default.
func validSizeFields(i machine.Instruction) bool {
	if !i.Op.Valid() || !validInstructionRegisters(i) {
		return false
	}
	switch i.Op {
	case machine.OpBarrier:
		return BarrierSize(i.Barrier) > 0
	case machine.OpBinaryImmediate:
		return i.Arithmetic != op.OpMul && (i.Bits == 0 || validWidth(i.Bits)) && validBinary(i.Arithmetic)
	case machine.OpBinary:
		return (i.Bits == 0 || validWidth(i.Bits)) && validBinary(i.Arithmetic)
	case machine.OpShift:
		return validWidth(i.Bits) && validShift(i.Arithmetic) && i.Imm < uint64(i.Bits)
	case machine.OpShiftBy:
		return validWidth(i.Bits) && validShift(i.Arithmetic)
	case machine.OpNarrow, machine.OpSignExtend:
		return i.Imm >= 1 && i.Imm <= 64
	case machine.OpCountZeros:
		return validWidth(i.Bits) && (i.Arithmetic == op.OpLeadingZeros || i.Arithmetic == op.OpTrailingZeros)
	case machine.OpMulHigh, machine.OpMulHighSigned, machine.OpAndNot:
		return validWidth(i.Bits)
	case machine.OpDivide:
		return validDivide(i)
	case machine.OpIntToFloat, machine.OpFloatToInt:
		return validFloatConvert(i)
	case machine.OpFloatCompare:
		return validFloatCompare(i)
	case machine.OpJumpIf, machine.OpSelectFlags:
		return i.Condition < 12
	case machine.OpFrameEnter, machine.OpFrameLeave:
		return i.Imm <= 1<<20
	case machine.OpSaveLink, machine.OpRestoreLink, machine.OpSpill, machine.OpReload, machine.OpSaveCFloat, machine.OpRestoreCFloat:
		return validSlot(i.Slot, 0)
	}
	return true
}
