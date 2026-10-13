package machine

import (
	"errors"
	"github.com/moriyoshi/nyanjit/ir"
)

var ErrUnsupportedOperation = errors.New("machine: assembler does not support operation")

// Assembler receives allocated operations from Visit.
// The emitter assigns PCs before this interface is called.
type Assembler interface {
	SetPC(int)
	AssembledSize() int
	Constant(Register, uint64)
	Symbol(Register, string)
	Move(Register, Register)
	Binary(ir.Opcode, uint8, Register, Register, Register)
	Decrement(Register, Register)
	Narrow(Register, Register, uint8)
	SignExtend(Register, Register, uint8)
	Shift(ir.Opcode, uint8, Register, Register, uint8)
	Scaled(Register, Register, Register, uint64, uint64)
	SelectValue(Register, Register, Register, Register)
	Load64(Register, Register)
	Store64(Register, Register)
	Load8(Register, Register)
	Store8(Register, Register)
	LoadAddress(Register, Register, Register, uint64, uint64)
	StoreAddress(Register, Register, Register, uint64, uint64)
	LoadOffset(Register, Register, uint64)
	StoreOffset(Register, Register, uint64)
	Syscall()
	CallC()
	CallGo()
	SaveLink(int)
	RestoreLink(int)
	Spill(Register, int)
	Reload(Register, int)
	FrameEnter(int)
	FrameLeave(int)
	Ret()
	CompareImmediate(Register, int64)
	CompareRegister(Register, Register)
	Jump(int)
	JumpIf(ir.BranchCondition, int)
}

// Visit dispatches an allocated instruction to a target assembler.
func Visit(ins Instruction, assembler Assembler) error {
	if !ins.ByteOrder.Valid() {
		return ErrUnsupportedOperation
	}
	if ins.ByteOrder != ir.ByteOrderNative {
		switch ins.Op {
		case OpLoad64, OpStore64, OpLoadAddress, OpStoreAddress, OpLoadOffset, OpStoreOffset:
			if ordered, ok := assembler.(interface{ OrderedMemory(Instruction) error }); ok {
				return ordered.OrderedMemory(ins)
			}
			native, ok := assembler.(interface{ NativeByteOrder() ir.ByteOrder })
			if !ok || native.NativeByteOrder() != ins.ByteOrder {
				return ErrUnsupportedOperation
			}
		case OpLoad8, OpStore8:
			// Byte order does not affect a single-byte access.
		default:
			return ErrUnsupportedOperation
		}
	}
	if ins.Op == OpCompareImmediate || ins.Op == OpCompareRegister {
		if ins.Comparison > CompareUnsigned {
			return ErrUnsupportedOperation
		}
		if comparison, ok := assembler.(interface{ Compare(Instruction) error }); ok {
			return comparison.Compare(ins)
		}
		if ins.Comparison != CompareEquality || ins.Bits != 0 && ins.Bits != 64 {
			return ErrUnsupportedOperation
		}
	}
	switch ins.Op {
	case OpFloatSpill, OpFloatReload:
		v, ok := assembler.(interface{ FloatMemory(Instruction) error })
		if !ok {
			return ErrUnsupportedOperation
		}
		return v.FloatMemory(ins)
	case OpLiteralLoad, OpLiteralData:
		v, ok := assembler.(interface{ Literal(Instruction) error })
		if !ok {
			return ErrUnsupportedOperation
		}
		return v.Literal(ins)
	case OpFloatMove, OpFloatBits:
		v, ok := assembler.(interface{ Float(Instruction) })
		if !ok {
			return ErrUnsupportedOperation
		}
		v.Float(ins)
	case OpVector, OpVectorMove, OpVectorSpill, OpVectorReload:
		v, ok := assembler.(interface{ Vector(Instruction) error })
		if !ok {
			return ErrUnsupportedOperation
		}
		return v.Vector(ins)
	case OpConst:
		assembler.Constant(ins.A, ins.Imm)
	case OpSymbol:
		assembler.Symbol(ins.A, ins.Symbol)
	case OpMove:
		assembler.Move(ins.A, ins.B)
	case OpBinary:
		assembler.Binary(ins.Arithmetic, ins.Bits, ins.A, ins.B, ins.C)
	case OpFloatUnary:
		fp, ok := assembler.(interface {
			FloatUnary(ir.Opcode, uint8, Register, Register)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		fp.FloatUnary(ins.Arithmetic, ins.Bits, ins.A, ins.B)
	case OpFloatBinary:
		fp, ok := assembler.(interface {
			FloatBinary(ir.Opcode, uint8, Register, Register, Register)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		fp.FloatBinary(ins.Arithmetic, ins.Bits, ins.A, ins.B, ins.C)
	case OpFloatFMA:
		fp, ok := assembler.(interface {
			FloatFMA(uint8, Register, Register, Register, Register)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		fp.FloatFMA(ins.Bits, ins.A, ins.B, ins.C, ins.D)
	case OpCarryBit, OpWordCarry:
		target, ok := assembler.(interface{ WordCarry(Instruction) })
		if !ok {
			return ErrUnsupportedOperation
		}
		target.WordCarry(ins)
	case OpMulHigh:
		target, ok := assembler.(interface {
			MulHigh(uint8, Register, Register, Register)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		target.MulHigh(ins.Bits, ins.A, ins.B, ins.C)
	case OpAndNot:
		target, ok := assembler.(interface {
			AndNot(uint8, Register, Register, Register)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		target.AndNot(ins.Bits, ins.A, ins.B, ins.C)
	case OpCountZeros:
		target, ok := assembler.(interface {
			CountZeros(ir.Opcode, uint8, Register, Register)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		target.CountZeros(ins.Arithmetic, ins.Bits, ins.A, ins.B)
	case OpShiftBy:
		if target, ok := assembler.(interface{ ShiftByInstruction(Instruction) }); ok {
			target.ShiftByInstruction(ins)
			break
		}
		target, ok := assembler.(interface {
			ShiftBy(ir.Opcode, uint8, Register, Register, Register)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		target.ShiftBy(ins.Arithmetic, ins.Bits, ins.A, ins.B, ins.C)
	case OpBinaryImmediate:
		immediate, ok := assembler.(interface {
			BinaryImmediate(ir.Opcode, uint8, Register, Register, uint64)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		immediate.BinaryImmediate(ins.Arithmetic, ins.Bits, ins.A, ins.B, ins.Imm)
	case OpBinaryCarry:
		// Compatibility opcodes canonicalize to the shared arithmetic visitors.
		ins.Op, ins.Bits, ins.CarryMode = OpWordCarry, 32, CarryFlags
		return Visit(ins, assembler)
	case OpMulWide:
		ins.Op, ins.Bits, ins.A, ins.B, ins.C = OpMulHigh, 32, 2, 0, ins.A
		return Visit(ins, assembler)
	case OpInc:
		increment, ok := assembler.(interface{ Increment(Register, Register) })
		if !ok {
			return ErrUnsupportedOperation
		}
		increment.Increment(ins.A, ins.B)
	case OpDec:
		if ins.PreserveFlags {
			if a, ok := assembler.(interface{ DecrementNoFlags(Register, Register) }); ok {
				a.DecrementNoFlags(ins.A, ins.B)
				break
			}
			return ErrUnsupportedOperation
		}
		assembler.Decrement(ins.A, ins.B)
	case OpNarrow:
		assembler.Narrow(ins.A, ins.B, uint8(ins.Imm))
	case OpSignExtend:
		assembler.SignExtend(ins.A, ins.B, uint8(ins.Imm))
	case OpShift:
		assembler.Shift(ins.Arithmetic, ins.Bits, ins.A, ins.B, uint8(ins.Imm))
	case OpScaled:
		assembler.Scaled(ins.A, ins.B, ins.C, ins.Imm, ins.Disp)
	case OpMulAdd:
		fused, ok := assembler.(interface {
			MulAdd(Register, Register, Register, Register)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		fused.MulAdd(ins.A, ins.B, ins.C, ins.D)
	case OpSelect:
		assembler.SelectValue(ins.A, ins.B, ins.C, ins.D)
	case OpSelectFlags:
		selection, ok := assembler.(interface {
			SelectFlags(Register, Register, Register, ir.BranchCondition)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		selection.SelectFlags(ins.A, ins.C, ins.D, ins.Condition)
	case OpLoad64:
		assembler.Load64(ins.A, ins.B)
	case OpStore64:
		assembler.Store64(ins.A, ins.B)
	case OpLoad8:
		assembler.Load8(ins.A, ins.B)
	case OpStore8:
		assembler.Store8(ins.A, ins.B)
	case OpLoadAddress:
		assembler.LoadAddress(ins.A, ins.B, ins.C, ins.Imm, ins.Disp)
	case OpStoreAddress:
		assembler.StoreAddress(ins.A, ins.B, ins.C, ins.Imm, ins.Disp)
	case OpLoadOffset:
		assembler.LoadOffset(ins.A, ins.B, ins.Disp)
	case OpStoreOffset:
		assembler.StoreOffset(ins.A, ins.B, ins.Disp)
	case OpSyscall:
		assembler.Syscall()
	case OpCallC:
		assembler.CallC()
	case OpCallGo:
		assembler.CallGo()
	case OpSaveLink:
		assembler.SaveLink(ins.Slot)
	case OpRestoreLink:
		assembler.RestoreLink(ins.Slot)
	case OpSpill:
		assembler.Spill(ins.A, ins.Slot)
	case OpReload:
		assembler.Reload(ins.A, ins.Slot)
	case OpSaveCFloat:
		fp, ok := assembler.(interface{ SaveCFloat(Register, int) })
		if !ok {
			return ErrUnsupportedOperation
		}
		fp.SaveCFloat(ins.A, ins.Slot)
	case OpRestoreCFloat:
		fp, ok := assembler.(interface{ RestoreCFloat(Register, int) })
		if !ok {
			return ErrUnsupportedOperation
		}
		fp.RestoreCFloat(ins.A, ins.Slot)
	case OpLoadIncoming:
		incoming, ok := assembler.(interface{ IncomingWord(Register, int) })
		if !ok {
			return ErrUnsupportedOperation
		}
		incoming.IncomingWord(ins.A, int(ins.Imm))
	case OpFrameEnter:
		if frame, ok := assembler.(interface{ FrameEnterArea(int, int) }); ok {
			frame.FrameEnterArea(int(ins.Imm), ins.CallAreaBytes)
		} else if ins.CallAreaBytes != 0 {
			return ErrUnsupportedOperation
		} else {
			assembler.FrameEnter(int(ins.Imm))
		}
	case OpFrameLeave:
		assembler.FrameLeave(int(ins.Imm))
	case OpReturn:
		assembler.Ret()
	case OpCompareImmediate:
		assembler.CompareImmediate(ins.A, int64(ins.Imm))
	case OpCompareRegister:
		assembler.CompareRegister(ins.A, ins.B)
	case OpJump:
		if forms, ok := assembler.(interface{ JumpForm(int, bool) }); ok {
			forms.JumpForm(int(ins.Imm), ins.Long)
		} else {
			assembler.Jump(int(ins.Imm))
		}
	case OpJumpIf:
		if forms, ok := assembler.(interface {
			JumpIfRange(ir.BranchCondition, int, bool, bool)
		}); ok {
			forms.JumpIfRange(ins.Condition, int(ins.Imm), ins.Long, ins.Far)
		} else if forms, ok := assembler.(interface {
			JumpIfForm(ir.BranchCondition, int, bool)
		}); ok {
			forms.JumpIfForm(ins.Condition, int(ins.Imm), ins.Long)
		} else {
			assembler.JumpIf(ins.Condition, int(ins.Imm))
		}
	case OpBranchZero:
		if a, ok := assembler.(interface {
			BranchZeroRange(Register, uint8, bool, int, bool, bool)
		}); ok {
			a.BranchZeroRange(ins.A, ins.Bits, ins.Condition == ir.BranchNotEqual, int(ins.Imm), ins.Long, ins.Far)
			break
		}
		branch, ok := assembler.(interface {
			BranchZero(Register, uint8, bool, int, bool)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		branch.BranchZero(ins.A, ins.Bits, ins.Condition == ir.BranchNotEqual, int(ins.Imm), ins.Long)
	case OpBranchBit:
		branch, ok := assembler.(interface {
			BranchBit(Register, uint8, uint8, bool, int, bool)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		branch.BranchBit(ins.A, ins.Bits, uint8(ins.Disp), ins.Condition == ir.BranchNotEqual, int(ins.Imm), ins.Long)
	case OpLoop:
		branch, ok := assembler.(interface{ Loop(int, bool) })
		if !ok {
			return ErrUnsupportedOperation
		}
		branch.Loop(int(ins.Imm), ins.Long)
	default:
		return ErrUnsupportedOperation
	}
	return nil
}
