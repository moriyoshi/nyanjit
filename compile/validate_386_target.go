package compile

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// validate386Program keeps unsupported paired operations out of the scalar
// 386 assembler. The encoder must never silently truncate a 64-bit value to
// one 32-bit instruction.
func (*i386Target) validateProgram(program machineProgram) error {
	var inputBuffer [4]int
	words := func(id int) uint8 {
		if id < 0 || id >= len(program.Values) {
			return 0
		}
		if program.Values[id].Words == 0 {
			return 1
		}
		return program.Values[id].Words
	}
	limit := func(ins machineInstruction) error {
		return fmt.Errorf("386 selected operation %d at value %d has unsupported register words (dst=%d, A=%d, B=%d; bits=%d): %w", ins.Op, ins.Dst, words(ins.Dst), words(ins.A), words(ins.B), ins.Bits, ErrBackendLimit)
	}
	for _, ins := range program.Instructions {
		switch ins.Op {
		case mVector:
			continue // Typed native SIMD was checked by IR validation and legalization.
		case machine.SelectedHomeGet, machine.SelectedHomeSet, machine.SelectedHomeBoundary:
			continue
		case mConst:
			if words(ins.Dst) != 1 && words(ins.Dst) != 2 {
				return limit(ins)
			}
		case mSymbol:
			if words(ins.Dst) != 1 && words(ins.Dst) != 2 {
				return limit(ins)
			}
		case mMove:
			if words(ins.Dst) != words(ins.A) && !(words(ins.Dst) == 2 && words(ins.A) == 1) {
				return limit(ins)
			}
		case mInc, mDec:
			if words(ins.Dst) != 1 || words(ins.A) != 1 || ins.Bits != 0 && ins.Bits != 32 {
				return limit(ins)
			}
		case mBinary:
			if words(ins.Dst) == 2 {
				if ins.Bits != 64 || words(ins.A) != 2 || words(ins.B) != 2 ||
					(ins.Arithmetic != opAdd && ins.Arithmetic != opSub &&
						ins.Arithmetic != opMul && ins.Arithmetic != opAnd && ins.Arithmetic != opOr && ins.Arithmetic != opXor) {
					return limit(ins)
				}
			} else if words(ins.Dst) != 1 || words(ins.A) != 1 || words(ins.B) != 1 || ins.Bits > 32 {
				return limit(ins)
			}
		case mBinaryImmediate:
			if words(ins.Dst) != 1 || words(ins.A) != 1 || ins.Bits != 32 ||
				(ins.Arithmetic != opAdd && ins.Arithmetic != opSub && ins.Arithmetic != opAnd &&
					ins.Arithmetic != opOr && ins.Arithmetic != opXor) {
				return limit(ins)
			}
		case mNarrow:
			if words(ins.Dst) != 1 || (words(ins.A) != 1 && words(ins.A) != 2) || ins.Imm > 32 {
				return limit(ins)
			}
		case mSignExtend:
			if words(ins.Dst) != 2 || (words(ins.A) != 1 && words(ins.A) != 2) ||
				(ins.Imm != 8 && ins.Imm != 16 && ins.Imm != 32) {
				return limit(ins)
			}
		case mMulHigh, mMulHighSigned, mCarryBit, mWordCarry:
			if words(ins.Dst) != 1 || ins.Bits != 32 {
				return limit(ins)
			}
			for _, id := range ins.InputsInto(inputBuffer[:0]) {
				if words(id) != 1 {
					return limit(ins)
				}
			}
		case mShiftBy:
			if words(ins.Dst) != words(ins.A) || words(ins.Dst) > 2 || words(ins.B) > 2 {
				return limit(ins)
			}
		case mShift:
			if words(ins.Dst) != words(ins.A) ||
				(words(ins.Dst) == 2 && ins.Bits != 64) ||
				(words(ins.Dst) == 1 && (ins.Bits > 32 ||
					ins.Bits < 32 && (ins.Arithmetic == ir.OpRotateLeft || ins.Arithmetic == ir.OpRotateRight))) {
				return limit(ins)
			}
		case mScaled:
			if (words(ins.Dst) != 1 && words(ins.Dst) != 2) || words(ins.A) != words(ins.Dst) || words(ins.B) != words(ins.Dst) ||
				ins.Disp != 0 || (ins.Imm != 1 && ins.Imm != 2 && ins.Imm != 4 && ins.Imm != 8) {
				return limit(ins)
			}
		case mSelect:
			if words(ins.Dst) != words(ins.B) || words(ins.Dst) != words(ins.C) ||
				(words(ins.Dst) != 1 && words(ins.Dst) != 2) ||
				(words(ins.A) != 1 && words(ins.A) != 2) {
				return limit(ins)
			}
		case mLoad16, mLoad32:
			if words(ins.Dst) != 1 || (words(ins.A) != 1 && words(ins.A) != 2) {
				return limit(ins)
			}
		case mStore16, mStore32:
			if (words(ins.A) != 1 && words(ins.A) != 2) || (words(ins.B) != 1 && words(ins.B) != 2) {
				return limit(ins)
			}
		case mLoad8:
			if (words(ins.Dst) != 1 && words(ins.Dst) != 2) || (words(ins.A) != 1 && words(ins.A) != 2) {
				return limit(ins)
			}
		case mStore8:
			if (words(ins.A) != 1 && words(ins.A) != 2) || (words(ins.B) != 1 && words(ins.B) != 2) {
				return limit(ins)
			}
		case mLoad64:
			if words(ins.Dst) != 2 || (words(ins.A) != 1 && words(ins.A) != 2) {
				return limit(ins)
			}
		case mStore64:
			if (words(ins.A) != 1 && words(ins.A) != 2) || words(ins.B) != 2 {
				return limit(ins)
			}
		case machine.SelectedFloatMove:
			if ins.Bits != 32 && ins.Bits != 64 || words(ins.Dst) != ins.Bits/32 || words(ins.A) != ins.Bits/32 {
				return limit(ins)
			}
		case machine.SelectedFloatBits:
			if ins.Bits != 32 && ins.Bits != 64 || words(ins.Dst) != ins.Bits/32 || words(ins.A) != ins.Bits/32 {
				return limit(ins)
			}
		case mFloatUnary, mFloatBinary:
			if ins.Bits != 32 && ins.Bits != 64 || words(ins.Dst) != ins.Bits/32 {
				return limit(ins)
			}
			inputBits := ins.Bits
			if ins.Op == mFloatUnary && ins.Arithmetic == ir.OpFConvert {
				inputBits = 96 - ins.Bits
			}
			if words(ins.A) != inputBits/32 || ins.Op == mFloatBinary && words(ins.B) != ins.Bits/32 {
				return limit(ins)
			}
		case mDivide:
			// A 64-bit division is a pair expansion; narrower ones are words.
			n := uint8(1)
			if ins.Bits == 64 {
				n = 2
			}
			if ins.Bits > 64 || words(ins.Dst) != n || words(ins.A) != n || words(ins.B) != n {
				return limit(ins)
			}
		case mIntToFloat:
			if ins.Bits != 32 && ins.Bits != 64 || ins.Imm != 32 && ins.Imm != 64 || words(ins.Dst) != ins.Bits/32 || words(ins.A) != uint8(ins.Imm/32) {
				return limit(ins)
			}
		case mFloatToInt:
			if ins.Bits != 32 && ins.Bits != 64 || ins.Disp != 32 && ins.Disp != 64 || words(ins.Dst) != ins.Bits/32 || words(ins.A) != uint8(ins.Disp/32) {
				return limit(ins)
			}
		case mFloatCompare:
			if ins.Bits != 32 && ins.Bits != 64 || words(ins.Dst) != 1 || words(ins.A) != ins.Bits/32 || words(ins.B) != ins.Bits/32 {
				return limit(ins)
			}
		case mCallC, mCallGo:
			maxInputs := 9
			if ins.Op == mCallGo {
				maxInputs = 10
			}
			if words(ins.Dst) != 2 || len(ins.Args) < 1 || len(ins.Args) > maxInputs || words(ins.Args[0]) != 1 {
				return limit(ins)
			}
			if ins.Op == mCallGo {
				bits := make([]uint8, len(ins.Args)-1)
				for i, id := range ins.Args[1:] {
					if id < 0 || id >= len(program.Values) {
						return limit(ins)
					}
					bits[i] = program.Values[id].Bits
				}
				if err := validate386GoSubwords(bits); err != nil {
					return err
				}
			}
			for _, id := range ins.Args[1:] {
				if words(id) != 2 && words(id) != 1 {
					return limit(ins)
				}
			}
		default:
			return limit(ins)
		}
	}
	return nil
}
