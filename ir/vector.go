package ir

import (
	"fmt"
	"slices"
)

// Inputs returns an independently mutable list of all virtual inputs.
func (i Instruction) Inputs() []int {
	inputs := i.InputsInto(nil)
	// InputsInto returns Args directly for borrowed variable-length operands.
	// Detect that ownership boundary instead of maintaining another opcode list.
	if cap(inputs) != 0 && cap(i.Args) != 0 && &inputs[:cap(inputs)][0] == &i.Args[:cap(i.Args)][0] {
		return slices.Clone(inputs)
	}
	return inputs
}

// InputsInto uses caller-owned storage for short operand lists. Returned Args
// belong to the immutable instruction and must not be modified.
func (i Instruction) InputsInto(dst []int) []int {
	return instructionInputsInto(i.Op, i.A, i.B, i.C, i.Args, dst)
}

// This small receiver wrapper can inline, so operand walkers read only the
// required fields instead of copying the entire instruction record.
func instructionInputsInto(opcode Opcode, a, b, c int, args, dst []int) []int {
	switch opcode {
	case OpScopeEnter, OpScopeLeave:
		return args
	case OpGet, OpParam, OpLabelParam, OpConst, OpVConst, OpVZero:
		return nil
	case OpAdd, OpSub, OpMul, OpAnd, OpOr, OpXor, OpAddScaled, OpFAdd, OpFSub, OpFMul, OpFDiv, OpMulHigh, OpMulHighSigned, OpFCompare, OpCarryAdd, OpBorrowSub, OpShiftLeftBy, OpShiftRightBy, OpShiftRightArithmeticBy, OpRotateLeftBy, OpRotateRightBy, OpStore64, OpStore8, OpStore16, OpStore32, OpVStore, OpVInsert, OpVInsertZero, OpVCompare, OpVCarrylessMul,
		OpDiv, OpDivSigned, OpRem, OpRemSigned:
		return append(dst, a, b)
	case OpSelect, OpFMA, OpFProductError, OpAddWithCarry, OpSubWithBorrow:
		return append(dst, a, b, c)
	case OpSyscall, OpCallC, OpCallGo:
		return args
	default:
		return append(dst, a)
	}
}

// IsVector reports a semantic vector operation, including scalar lane/mask results.
func (i Instruction) IsVector() bool {
	return i.Type.Kind == VectorKind || i.Type.Kind == MaskKind || i.Op >= OpVLoad && i.Op <= OpVInsertZero || i.Op == OpVCarrylessMul
}

func validateVectorInstruction(i Instruction, index int, ops []Instruction) error {
	var inputBuffer [4]int
	bad := func() error {
		return &ValidationError{Index: index, Op: i.Op, Rule: "vector operation has invalid shape or operands"}
	}
	if !i.ValueType().Valid() || !i.ByteOrder.Valid() || i.ByteOrder != ByteOrderNative && i.Op != OpVLoad && i.Op != OpVStore {
		return bad()
	}
	for _, id := range i.InputsInto(inputBuffer[:0]) {
		if id < 0 || id >= index {
			return bad()
		}
	}
	t := i.ValueType()
	// Bits describes the physical lane width, except comparisons whose result
	// is a logical mask and carries the input lane width instead. Mask label
	// parameters retain 64-bit legacy routing metadata, not one-bit lane storage.
	expectedBits := t.LaneBits
	if i.Op == OpLabelParam && t.Kind == MaskKind {
		expectedBits = 64
	}
	if i.Op != OpVCompare && i.Bits != expectedBits {
		return &ValidationError{Index: index, Op: i.Op, Rule: fmt.Sprintf("vector operation has Bits=%d, expected %d for type %+v", i.Bits, expectedBits, t)}
	}
	input := func(id int) Type { return ops[id].ValueType() }
	address := func(id int) bool {
		return input(id).Kind == ScalarKind && (input(id).LaneBits == 32 || input(id).LaneBits == 64)
	}
	switch i.Op {
	case OpLabelParam:
		if t.Kind != VectorKind && t.Kind != MaskKind {
			return bad()
		}
	case OpVLoad:
		if t.Kind != VectorKind || !address(i.A) {
			return bad()
		}
	case OpVStore:
		if t.Kind != VectorKind || input(i.B) != t || !address(i.A) {
			return bad()
		}
	case OpVSplat:
		if t.Kind != VectorKind || input(i.A) != Scalar(t.LaneBits) {
			return bad()
		}
	case OpVConst:
		if t.Kind != VectorKind || len(i.Data) != int(t.Bits()/8) {
			return bad()
		}
	case OpVZero:
		if t.Kind != VectorKind {
			return bad()
		}
	case OpVExtract:
		if input(i.A).Kind != VectorKind || t != Scalar(input(i.A).LaneBits) || i.Imm >= uint64(input(i.A).Lanes) {
			return bad()
		}
	case OpVInsert, OpVInsertZero:
		if t.Kind != VectorKind || input(i.A) != t || input(i.B) != Scalar(t.LaneBits) || i.Imm >= uint64(t.Lanes) {
			return bad()
		}
	case OpVCompare:
		if input(i.A).Kind != VectorKind || input(i.A) != input(i.B) || t != Mask(input(i.A).Lanes) || i.Bits != input(i.A).LaneBits || i.Imm > uint64(BranchAbove) {
			return bad()
		}
	case OpVMaskBits:
		if input(i.A).Kind != MaskKind || t.Kind != ScalarKind || t.LaneBits > 64 || uint16(t.LaneBits) < input(i.A).Lanes {
			return bad()
		}
	case OpVCarrylessMul:
		if t.Kind != VectorKind || t.LaneBits != 64 || t.Lanes%2 != 0 || input(i.A) != t || input(i.B) != t || i.Imm > 3 {
			return bad()
		}
	case OpAdd, OpSub, OpMul, OpAnd, OpOr, OpXor:
		if t.Kind != VectorKind || input(i.A) != t || input(i.B) != t {
			return bad()
		}
	case OpShiftLeft, OpShiftRight, OpShiftRightArithmetic, OpRotateLeft, OpRotateRight:
		if t.Kind != VectorKind || input(i.A) != t {
			return bad()
		}
	case OpShiftLeftBy, OpShiftRightBy, OpShiftRightArithmeticBy, OpRotateLeftBy, OpRotateRightBy:
		if t.Kind != VectorKind || input(i.A) != t || input(i.B) != t && (input(i.B).Kind != ScalarKind || input(i.B).LaneBits > 64) {
			return bad()
		}
	default:
		return bad()
	}
	return nil
}
