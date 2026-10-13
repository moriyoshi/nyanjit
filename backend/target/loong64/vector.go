package loong64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// LSX vrN aliases fN. Homes share f8-f23 with scalar FP. Only the low
// 64 bits of f24-f31 are C-preserved, so none are whole-vector homes.
// vr0-vr2 are reserved for expansion and are never allocation homes.
func vectorPlan(i machine.Instruction, stack abi.StackPractice, homes int, sizeOnly bool) ([]concrete, []backend.Register, error) {
	valid := true
	gp, vec := func(r backend.Register) bool { return r < 32 }, backend.IsVectorRegister
	switch i.Op {
	case machine.OpVectorMove:
		valid = vec(i.A) && vec(i.B)
	case machine.OpVectorSpill, machine.OpVectorReload:
		valid = vec(i.A) && i.Slot >= 0
	case machine.OpVector:
		switch i.Arithmetic {
		case op.OpVLoad:
			valid = vec(i.A) && gp(i.B) && (!i.Indexed || gp(i.C) && i.Disp == 0)
		case op.OpVStore:
			valid = gp(i.A) && vec(i.B) && (!i.Indexed || gp(i.C) && i.Disp == 0)
		case op.OpVSplat:
			valid = vec(i.A) && gp(i.B)
		case op.OpVExtract, op.OpVMaskBits:
			valid = gp(i.A) && vec(i.B)
		case op.OpVZero:
			valid = vec(i.A)
		case op.OpVInsert, op.OpVInsertZero:
			valid = vec(i.A) && vec(i.B) && gp(i.C)
		case op.OpShiftLeft, op.OpShiftRight, op.OpShiftRightArithmetic, op.OpRotateLeft, op.OpRotateRight:
			valid = vec(i.A) && vec(i.B)
		case op.OpShiftLeftBy, op.OpShiftRightBy, op.OpShiftRightArithmeticBy, op.OpRotateLeftBy, op.OpRotateRightBy:
			valid = vec(i.A) && vec(i.B) && (vec(i.C) || gp(i.C))
		default:
			valid = vec(i.A) && vec(i.B) && vec(i.C)
		}
	default:
		valid = false
	}
	if !valid {
		return nil, nil, machine.ErrUnsupportedOperation
	}
	e := NewByteAssembler(stack, homes)
	e.sizeOnly = sizeOnly
	var writes []backend.Register
	write := func(d uint32) {
		r := backend.VectorRegister(uint8(d))
		for _, old := range writes {
			if old == r {
				return
			}
		}
		writes = append(writes, r)
	}
	rr := func(op uint32, name string, d, a uint32) {
		e.emit(op|a<<5|d, "%s vr%d, vr%d", name, d, a)
		write(d)
	}
	rrr := func(op uint32, name string, d, a, b uint32) {
		e.emit(op|b<<10|a<<5|d, "%s vr%d, vr%d, vr%d", name, d, a, b)
		write(d)
	}
	copyVector := func(d, a uint32) {
		if d != a {
			rrr(0xe24d<<15, "vor.v", d, a, a)
		}
	}
	vr := func(r backend.Register) uint32 { return uint32(backend.VectorNumber(r)) }
	a, b, c := vr(i.A), vr(i.B), vr(i.C)
	if i.Op == machine.OpVectorMove {
		copyVector(a, b)
		return e.plan, writes, nil
	}
	memory := func(load bool, v uint32, base backend.Register, offset int64, index backend.Register, indexed bool) {
		if indexed {
			op, name := uint32(0x38400000), "vldx"
			if !load {
				op, name = 0x38440000, "vstx"
			}
			e.emit(op|uint32(index)<<10|uint32(base)<<5|v, "%s vr%d, r%d, r%d", name, v, base, index)
		} else {
			base, offset = e.memoryBase(base, offset)
			op, name := uint32(0x2c000000), "vld"
			if !load {
				op, name = 0x2c400000, "vst"
			}
			e.emit(op|(uint32(offset)&4095)<<10|uint32(base)<<5|v, "%s vr%d, %d(r%d)", name, v, offset, base)
		}
		if load {
			write(v)
		}
	}
	if i.Op == machine.OpVectorSpill || i.Op == machine.OpVectorReload {
		if i.Slot < 0 {
			return nil, nil, machine.ErrUnsupportedOperation
		}
		base, off := e.spillAddress(i.Slot)
		memory(i.Op == machine.OpVectorReload, a, base, int64(off), 0, false)
		return e.plan, writes, nil
	}
	size, suffix := uint32(0), "b"
	switch i.Bits {
	case 8:
	case 16:
		size, suffix = 1, "h"
	case 32:
		size, suffix = 2, "w"
	case 64:
		size, suffix = 3, "d"
	default:
		return nil, nil, machine.ErrUnsupportedOperation
	}
	splat := func(d uint32, r backend.Register) {
		e.emit((0x1ca7c0+size)<<10|uint32(r)<<5|d, "vreplgr2vr.%s vr%d, r%d", suffix, d, r)
		write(d)
	}
	shiftImmediate := func(operation op.Opcode, d, src uint32, count uint64) {
		base, name := uint32(0x1ccb<<18), "vslli."
		switch operation {
		case op.OpShiftRight:
			base, name = 0x1ccc<<18, "vsrli."
		case op.OpShiftRightArithmetic:
			base, name = 0x1ccd<<18, "vsrai."
		case op.OpRotateRight:
			base, name = 0x1ca8<<18, "vrotri."
		}
		e.emit(base|uint32(i.Bits)<<10|uint32(count)<<10|src<<5|d, "%s%s vr%d, vr%d, %d", name, suffix, d, src, count)
		write(d)
	}
	switch i.Arithmetic {
	case op.OpVLoad:
		memory(true, a, i.B, int64(i.Disp), i.C, i.Indexed)
	case op.OpVStore:
		memory(false, b, i.A, int64(i.Disp), i.C, i.Indexed)
	case op.OpVZero:
		rrr(0xe24e<<15, "vxor.v", a, a, a)
	case op.OpVSplat:
		splat(a, i.B)
	case op.OpVExtract:
		if i.Imm >= 128/uint64(i.Bits) {
			return nil, nil, machine.ErrUnsupportedOperation
		}
		op := []uint32{0x01cbce << 14, 0x03979e << 13, 0x072f3e << 12, 0x0e5e7e << 11}[size]
		e.emit(op|uint32(i.Imm)<<10|b<<5|uint32(i.A), "vpickve2gr.%su r%d, vr%d, %d", suffix, i.A, b, i.Imm)
	case op.OpVInsert, op.OpVInsertZero:
		if i.Imm >= 128/uint64(i.Bits) {
			return nil, nil, machine.ErrUnsupportedOperation
		}
		copyVector(a, b)
		op := []uint32{0x01cbae << 14, 0x03975e << 13, 0x072ebe << 12, 0x0e5d7e << 11}[size]
		e.emit(op|uint32(i.Imm)<<10|uint32(i.C)<<5|a, "vinsgr2vr.%s vr%d, r%d, %d", suffix, a, i.C, i.Imm)
		write(a)
	case op.OpAdd, op.OpSub, op.OpMul:
		operation, name := uint32(0xe014), "vadd."
		if i.Arithmetic == op.OpSub {
			operation, name = 0xe018, "vsub."
		}
		if i.Arithmetic == op.OpMul {
			operation, name = 0xe108, "vmul."
		}
		rrr((operation+size)<<15, name+suffix, a, b, c)
	case op.OpAnd, op.OpOr, op.OpXor:
		operation, name := uint32(0xe24c), "vand.v"
		if i.Arithmetic == op.OpOr {
			operation, name = 0xe24d, "vor.v"
		}
		if i.Arithmetic == op.OpXor {
			operation, name = 0xe24e, "vxor.v"
		}
		rrr(operation<<15, name, a, b, c)
	case op.OpVCompare:
		if i.Bits != 8 || i.Imm != uint64(op.BranchEqual) {
			return nil, nil, machine.ErrUnsupportedOperation
		}
		rrr(0xe000<<15, "vseq.b", a, b, c)
	case op.OpVMaskBits:
		rr(0x1ca710<<10, "vmskltz.b", 0, b)
		e.emit(0x072f3e<<12|uint32(i.A), "vpickve2gr.wu r%d, vr0, 0", i.A)
	case op.OpShiftLeft, op.OpShiftRight, op.OpShiftRightArithmetic, op.OpRotateLeft, op.OpRotateRight:
		count, operation := i.Imm, i.Arithmetic
		if operation == op.OpRotateLeft || operation == op.OpRotateRight {
			count %= uint64(i.Bits)
			if operation == op.OpRotateLeft {
				count = (uint64(i.Bits) - count) % uint64(i.Bits)
			}
			operation = op.OpRotateRight
		} else if count >= uint64(i.Bits) {
			if operation == op.OpShiftRightArithmetic {
				count = uint64(i.Bits - 1)
			} else {
				rrr(0xe24e<<15, "vxor.v", a, a, a)
				break
			}
		}
		if count == 0 {
			copyVector(a, b)
		} else {
			shiftImmediate(operation, a, b, count)
		}
	case op.OpShiftLeftBy, op.OpShiftRightBy, op.OpShiftRightArithmeticBy, op.OpRotateLeftBy, op.OpRotateRightBy:
		operation, _ := op.ShiftByOperation(i.Arithmetic)
		rotate := operation == op.OpRotateLeft || operation == op.OpRotateRight
		count := c
		if !backend.IsVectorRegister(i.C) {
			splat(0, i.C)
			count = 0
		}
		if rotate {
			if operation == op.OpRotateLeft {
				rr((0x1ca70c+size)<<10, "vneg."+suffix, 0, count)
				count = 0
			}
			rrr((0xe1dc+size)<<15, "vrotr."+suffix, a, b, count)
			break
		}
		// Hardware masks counts modulo lane width. Retain the IR's full
		// unsigned count and mask out overshifts (or substitute the sign).
		if backend.IsVectorRegister(i.C) {
			e.Constant(19, uint64(i.Bits))
			splat(1, 19)
			rrr((0xe010+size)<<15, "vslt."+suffix+"u", 1, c, 1)
		} else {
			e.ri(0x02400000, "sltui", 19, i.C, int64(i.Bits))
			e.rrr(0x00118000, "sub.d", 19, 0, 19)
			splat(1, 19)
		}
		if operation == op.OpShiftRightArithmetic {
			shiftImmediate(operation, 2, b, uint64(i.Bits-1))
		}
		base, name := uint32(0xe1d0), "vsll."
		if operation == op.OpShiftRight {
			base, name = 0xe1d4, "vsrl."
		}
		if operation == op.OpShiftRightArithmetic {
			base, name = 0xe1d8, "vsra."
		}
		rrr((base+size)<<15, name+suffix, a, b, count)
		rrr(0xe24c<<15, "vand.v", a, a, 1)
		if operation == op.OpShiftRightArithmetic {
			// VANDN negates its first source: (~valid) & sign.
			rrr(0xe250<<15, "vandn.v", 2, 1, 2)
			rrr(0xe24d<<15, "vor.v", a, a, 2)
		}
	default:
		return nil, nil, machine.ErrUnsupportedOperation
	}
	return e.plan, writes, nil
}

// Vector emits the same instruction plan used by text, sizing and clobbers.
func (e *ByteAssembler) Vector(i machine.Instruction) error {
	if !e.require((validInstructionRegisters(i))) {
		return e.err
	}

	plan, _, err := vectorPlan(i, e.stack, e.goHomes, e.sizeOnly)
	if err != nil {
		return err
	}
	e.plan = append(e.plan, plan...)
	return nil
}

// VectorWrites returns the FP/vector bank writes of the actual LSX expansion.
func VectorWrites(i machine.Instruction) []backend.Register {
	_, writes, _ := vectorPlan(i, abi.StackNoUse, 0, true)
	return writes
}
