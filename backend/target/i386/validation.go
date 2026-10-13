package i386

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// Err returns the first invalid operand error; subsequent visits retain it.
func (e *ByteAssembler) Err() error { return e.err }
func (e *ByteAssembler) require(ok bool) bool {
	if !ok && e.err == nil {
		e.err = machine.ErrInvalidInstruction
	}
	return ok && e.err == nil
}
func validGeneralRegisters(regs ...backend.Register) bool {
	for _, r := range regs {
		if r >= 8 {
			return false
		}
	}
	return true
}
func validFloatRegisters(regs ...backend.Register) bool {
	for _, r := range regs {
		if r >= 8 && !(backend.IsVectorRegister(r) && backend.VectorNumber(r) < 8) {
			return false
		}
	}
	return true
}
func validInstructionRegisters(i machine.Instruction) bool {
	if !validGeneralRegisters(i.HighA, i.HighB, i.HighC) {
		return false
	}
	switch i.Op {
	case machine.OpFloatMove, machine.OpFloatBits, machine.OpFloatUnary, machine.OpFloatBinary, machine.OpFloatFMA, machine.OpFloatSpill, machine.OpFloatReload, machine.OpVector, machine.OpVectorMove, machine.OpVectorSpill, machine.OpVectorReload, machine.OpLiteralLoad,
		machine.OpIntToFloat, machine.OpFloatToInt, machine.OpFloatCompare:
		return validFloatRegisters(i.A, i.B, i.C, i.D, i.E)
	}
	return validGeneralRegisters(i.A, i.B, i.C, i.D, i.E)
}

// validFloatForm checks scalar float fields beyond their registers. GP
// operands carry binary32 words or binary64 pairs, but memory homes and the
// float-bits destination must be XMM registers, and XMM0 holds that mask.
func validFloatForm(i machine.Instruction) bool {
	if !validInstructionRegisters(i) || i.Bits != 32 && i.Bits != 64 {
		return false
	}
	xmm := backend.IsVectorRegister
	switch i.Op {
	case machine.OpFloatMove:
		return xmm(i.A) || xmm(i.B)
	case machine.OpFloatUnary:
		return i.Arithmetic == op.OpFSqrt || i.Arithmetic == op.OpFConvert
	case machine.OpFloatBinary:
		return i.Arithmetic == op.OpFAdd || i.Arithmetic == op.OpFSub || i.Arithmetic == op.OpFMul || i.Arithmetic == op.OpFDiv
	case machine.OpFloatBits:
		return xmm(i.A) && backend.VectorNumber(i.A) != 0 && (i.Arithmetic == op.OpAnd || i.Arithmetic == op.OpXor)
	case machine.OpFloatSpill, machine.OpFloatReload:
		return xmm(i.A)
	}
	return false
}
