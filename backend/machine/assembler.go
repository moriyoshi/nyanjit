package machine

import (
	"errors"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/op"
)

var ErrUnsupportedOperation = errors.New("machine: assembler does not support operation")

// ErrInvalidInstruction identifies operands outside an encoder's valid range.
var ErrInvalidInstruction = errors.New("machine: invalid instruction operand")

// Assembler receives allocated operations from Visit.
// The emitter assigns PCs before this interface is called. Byte encoders reject
// invalid register banks, opcode/width combinations, immediate fields and
// displacements before exposing bytes. Callers still own ABI conventions,
// memory validity, label binding and operand normalization. Out-of-range fields
// are not a request to truncate; explicit arithmetic must express truncation.
// Expanded frame adjustments are bounded to 1 MiB per encoder operation.
type Assembler interface {
	SetPC(int)
	AssembledSize() int
	Constant(backend.Register, uint64)
	Symbol(backend.Register, string)
	Move(backend.Register, backend.Register)
	Binary(op.Opcode, uint8, backend.Register, backend.Register, backend.Register)
	Decrement(backend.Register, backend.Register)
	Narrow(backend.Register, backend.Register, uint8)
	SignExtend(backend.Register, backend.Register, uint8)
	Shift(op.Opcode, uint8, backend.Register, backend.Register, uint8)
	Scaled(backend.Register, backend.Register, backend.Register, uint64, uint64)
	SelectValue(backend.Register, backend.Register, backend.Register, backend.Register)
	Load64(backend.Register, backend.Register)
	Store64(backend.Register, backend.Register)
	Load8(backend.Register, backend.Register)
	Store8(backend.Register, backend.Register)
	LoadAddress(backend.Register, backend.Register, backend.Register, uint64, uint64)
	StoreAddress(backend.Register, backend.Register, backend.Register, uint64, uint64)
	LoadOffset(backend.Register, backend.Register, uint64)
	StoreOffset(backend.Register, backend.Register, uint64)
	Syscall()
	CallC()
	CallGo()
	SaveLink(int)
	RestoreLink(int)
	Spill(backend.Register, int)
	Reload(backend.Register, int)
	FrameEnter(int)
	FrameLeave(int)
	Ret()
	CompareImmediate(backend.Register, int64)
	CompareRegister(backend.Register, backend.Register)
	Jump(int)
	JumpIf(op.BranchCondition, int)
}

// maxFrameAdjustment is the per-operation frame bound documented on Assembler.
const maxFrameAdjustment = 1 << 20

// Visit dispatches an allocated instruction to a target assembler.
func Visit(ins Instruction, assembler Assembler) error {
	checked, _ := assembler.(interface{ Err() error })
	if checked != nil {
		if err := checked.Err(); err != nil {
			return err
		}
	}

	switch ins.Op {
	case OpNarrow, OpSignExtend, OpShift:
		if ins.Imm > 255 {
			return ErrInvalidInstruction
		}
	case OpBranchBit:
		if ins.Disp > 63 || ins.Bits == 0 || ins.Disp >= uint64(ins.Bits) || (ins.Condition != op.BranchEqual && ins.Condition != op.BranchNotEqual) {
			return ErrInvalidInstruction
		}
	case OpBranchZero:
		if ins.Condition != op.BranchEqual && ins.Condition != op.BranchNotEqual {
			return ErrInvalidInstruction
		}
	case OpJump, OpJumpIf, OpLoop, OpLoadIncoming:
		if ins.Imm > uint64(^uint(0)>>1) {
			return ErrInvalidInstruction
		}
	case OpFrameEnter, OpFrameLeave:
		// Enforce the Assembler bound here as well, so assemblers that only
		// format text cannot expand an unbounded frame adjustment.
		if ins.Imm > maxFrameAdjustment || ins.CallAreaBytes < 0 || ins.CallAreaBytes > maxFrameAdjustment {
			return ErrInvalidInstruction
		}
	}
	if !ins.ByteOrder.Valid() {
		return ErrUnsupportedOperation
	}
	if ins.Op == OpLoad16 || ins.Op == OpLoad32 || ins.Op == OpStore16 || ins.Op == OpStore32 {
		narrow, ok := assembler.(interface{ NarrowMemory(Instruction) error })
		if !ok {
			return ErrUnsupportedOperation
		}
		return checkedAssemblyError(narrow.NarrowMemory(ins), checked)
	}
	if ins.ByteOrder != op.ByteOrderNative {
		switch ins.Op {
		case OpLoad64, OpStore64, OpLoadAddress, OpStoreAddress, OpLoadOffset, OpStoreOffset:
			if ordered, ok := assembler.(interface{ OrderedMemory(Instruction) error }); ok {
				return checkedAssemblyError(ordered.OrderedMemory(ins), checked)
			}
			native, ok := assembler.(interface{ NativeByteOrder() op.ByteOrder })
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
			return checkedAssemblyError(comparison.Compare(ins), checked)
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
		return checkedAssemblyError(v.FloatMemory(ins), checked)
	case OpLiteralLoad, OpLiteralData:
		v, ok := assembler.(interface{ Literal(Instruction) error })
		if !ok {
			return ErrUnsupportedOperation
		}
		return checkedAssemblyError(v.Literal(ins), checked)
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
		return checkedAssemblyError(v.Vector(ins), checked)
	case OpConst:
		assembler.Constant(ins.A, ins.Imm)
	case OpSymbol:
		if v, ok := assembler.(interface{ SymbolInstruction(Instruction) }); ok {
			v.SymbolInstruction(ins)
		} else {
			assembler.Symbol(ins.A, ins.Symbol)
		}
	case OpMove:
		assembler.Move(ins.A, ins.B)
	case OpBinary:
		if ins.PreserveFlags {
			a, ok := assembler.(interface {
				BinaryNoFlags(op.Opcode, uint8, backend.Register, backend.Register, backend.Register)
			})
			if !ok {
				return ErrUnsupportedOperation
			}
			a.BinaryNoFlags(ins.Arithmetic, ins.Bits, ins.A, ins.B, ins.C)
			break
		}
		assembler.Binary(ins.Arithmetic, ins.Bits, ins.A, ins.B, ins.C)
	case OpFloatUnary:
		// A full visitor preserves paired GP operands on 32-bit targets.
		if fp, ok := assembler.(interface{ Float(Instruction) }); ok {
			fp.Float(ins)
			break
		}
		fp, ok := assembler.(interface {
			FloatUnary(op.Opcode, uint8, backend.Register, backend.Register)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		fp.FloatUnary(ins.Arithmetic, ins.Bits, ins.A, ins.B)
	case OpFloatBinary:
		// A full visitor preserves paired GP operands on 32-bit targets.
		if fp, ok := assembler.(interface{ Float(Instruction) }); ok {
			fp.Float(ins)
			break
		}
		fp, ok := assembler.(interface {
			FloatBinary(op.Opcode, uint8, backend.Register, backend.Register, backend.Register)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		fp.FloatBinary(ins.Arithmetic, ins.Bits, ins.A, ins.B, ins.C)
	case OpFloatFMA:
		fp, ok := assembler.(interface {
			FloatFMA(uint8, backend.Register, backend.Register, backend.Register, backend.Register)
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
	case OpDivide:
		target, ok := assembler.(interface{ Divide(Instruction) })
		if !ok {
			return ErrUnsupportedOperation
		}
		target.Divide(ins)
	case OpIntToFloat, OpFloatToInt:
		target, ok := assembler.(interface{ FloatConvert(Instruction) })
		if !ok {
			return ErrUnsupportedOperation
		}
		target.FloatConvert(ins)
	case OpMulHigh:
		target, ok := assembler.(interface {
			MulHigh(uint8, backend.Register, backend.Register, backend.Register)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		target.MulHigh(ins.Bits, ins.A, ins.B, ins.C)
	case OpMulHighSigned:
		target, ok := assembler.(interface {
			MulHighSigned(uint8, backend.Register, backend.Register, backend.Register)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		target.MulHighSigned(ins.Bits, ins.A, ins.B, ins.C)
	case OpFloatCompare:
		target, ok := assembler.(interface {
			FloatCompare(op.FloatCondition, uint8, backend.Register, backend.Register, backend.Register)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		if ins.Imm > uint64(op.FloatUnorderedEqual) {
			return ErrInvalidInstruction
		}
		target.FloatCompare(op.FloatCondition(ins.Imm), ins.Bits, ins.A, ins.B, ins.C)
	case OpAndNot:
		target, ok := assembler.(interface {
			AndNot(uint8, backend.Register, backend.Register, backend.Register)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		target.AndNot(ins.Bits, ins.A, ins.B, ins.C)
	case OpCountZeros:
		target, ok := assembler.(interface {
			CountZeros(op.Opcode, uint8, backend.Register, backend.Register)
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
			ShiftBy(op.Opcode, uint8, backend.Register, backend.Register, backend.Register)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		target.ShiftBy(ins.Arithmetic, ins.Bits, ins.A, ins.B, ins.C)
	case OpBinaryImmediate:
		if ins.PreserveFlags {
			a, ok := assembler.(interface {
				BinaryImmediateNoFlags(op.Opcode, uint8, backend.Register, backend.Register, uint64)
			})
			if !ok {
				return ErrUnsupportedOperation
			}
			a.BinaryImmediateNoFlags(ins.Arithmetic, ins.Bits, ins.A, ins.B, ins.Imm)
			break
		}
		immediate, ok := assembler.(interface {
			BinaryImmediate(op.Opcode, uint8, backend.Register, backend.Register, uint64)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		immediate.BinaryImmediate(ins.Arithmetic, ins.Bits, ins.A, ins.B, ins.Imm)
	case OpBinaryCarry:
		// Compatibility opcodes canonicalize to the shared arithmetic visitors.
		ins.Op, ins.Bits, ins.CarryMode = OpWordCarry, 32, CarryFlags
		return checkedAssemblyError(Visit(ins, assembler), checked)
	case OpMulWide:
		// The implicit accumulator exists only on targets that name it.
		wide, ok := assembler.(interface{ MulWide(backend.Register) })
		if !ok {
			return ErrUnsupportedOperation
		}
		wide.MulWide(ins.A)
	case OpInc:
		if ins.PreserveFlags {
			if a, ok := assembler.(interface {
				IncrementNoFlags(backend.Register, backend.Register)
			}); ok {
				a.IncrementNoFlags(ins.A, ins.B)
				break
			}
			return ErrUnsupportedOperation
		}
		increment, ok := assembler.(interface {
			Increment(backend.Register, backend.Register)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		increment.Increment(ins.A, ins.B)
	case OpDec:
		if ins.PreserveFlags {
			if a, ok := assembler.(interface {
				DecrementNoFlags(backend.Register, backend.Register)
			}); ok {
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
		if v, ok := assembler.(interface{ ShiftInstruction(Instruction) }); ok {
			v.ShiftInstruction(ins)
		} else {
			assembler.Shift(ins.Arithmetic, ins.Bits, ins.A, ins.B, uint8(ins.Imm))
		}
	case OpScaled:
		assembler.Scaled(ins.A, ins.B, ins.C, ins.Imm, ins.Disp)
	case OpMulAdd:
		fused, ok := assembler.(interface {
			MulAdd(backend.Register, backend.Register, backend.Register, backend.Register)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		fused.MulAdd(ins.A, ins.B, ins.C, ins.D)
	case OpSelect:
		assembler.SelectValue(ins.A, ins.B, ins.C, ins.D)
	case OpSelectFlags:
		selection, ok := assembler.(interface {
			SelectFlags(backend.Register, backend.Register, backend.Register, op.BranchCondition)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		selection.SelectFlags(ins.A, ins.C, ins.D, ins.Condition)
	case OpBarrier:
		barrier, ok := assembler.(interface{ Barrier(Barrier) error })
		if !ok {
			return ErrUnsupportedOperation
		}
		return checkedAssemblyError(barrier.Barrier(ins.Barrier), checked)
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
		fp, ok := assembler.(interface{ SaveCFloat(backend.Register, int) })
		if !ok {
			return ErrUnsupportedOperation
		}
		fp.SaveCFloat(ins.A, ins.Slot)
	case OpRestoreCFloat:
		fp, ok := assembler.(interface{ RestoreCFloat(backend.Register, int) })
		if !ok {
			return ErrUnsupportedOperation
		}
		fp.RestoreCFloat(ins.A, ins.Slot)
	case OpLoadIncoming:
		incoming, ok := assembler.(interface{ IncomingWord(backend.Register, int) })
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
			JumpIfRange(op.BranchCondition, int, bool, bool)
		}); ok {
			forms.JumpIfRange(ins.Condition, int(ins.Imm), ins.Long, ins.Far)
		} else if forms, ok := assembler.(interface {
			JumpIfForm(op.BranchCondition, int, bool)
		}); ok {
			forms.JumpIfForm(ins.Condition, int(ins.Imm), ins.Long)
		} else {
			assembler.JumpIf(ins.Condition, int(ins.Imm))
		}
	case OpBranchZero:
		if a, ok := assembler.(interface {
			BranchZeroRange(backend.Register, uint8, bool, int, bool, bool)
		}); ok {
			a.BranchZeroRange(ins.A, ins.Bits, ins.Condition == op.BranchNotEqual, int(ins.Imm), ins.Long, ins.Far)
			break
		}
		branch, ok := assembler.(interface {
			BranchZero(backend.Register, uint8, bool, int, bool)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		branch.BranchZero(ins.A, ins.Bits, ins.Condition == op.BranchNotEqual, int(ins.Imm), ins.Long)
	case OpBranchBit:
		branch, ok := assembler.(interface {
			BranchBit(backend.Register, uint8, uint8, bool, int, bool)
		})
		if !ok {
			return ErrUnsupportedOperation
		}
		branch.BranchBit(ins.A, ins.Bits, uint8(ins.Disp), ins.Condition == op.BranchNotEqual, int(ins.Imm), ins.Long)
	case OpLoop:
		branch, ok := assembler.(interface{ Loop(int, bool) })
		if !ok {
			return ErrUnsupportedOperation
		}
		branch.Loop(int(ins.Imm), ins.Long)
	default:
		return ErrUnsupportedOperation
	}
	return checkedAssemblyError(nil, checked)
}

// checkedAssemblyError preserves an explicit visitor error, or observes a sticky
// encoder error after a successful dispatch, without a per-instruction defer.
func checkedAssemblyError(err error, checked interface{ Err() error }) error {
	if err != nil || checked == nil {
		return err
	}
	return checked.Err()
}
