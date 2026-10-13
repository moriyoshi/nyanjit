package ir

import "fmt"

// Inputs names every virtual input, including vector and mask operands.
func (i Instruction) Inputs() []int {
	switch i.Op {
	case OpScopeEnter:
		return i.Args
	case OpGet, OpScopeLeave, OpParam, OpLabelParam, OpConst, OpVConst, OpVZero:
		return nil
	case OpAdd, OpSub, OpMul, OpAnd, OpOr, OpXor, OpAddScaled, OpFAdd, OpFSub, OpFMul, OpFDiv, OpMulHigh, OpCarryAdd, OpBorrowSub, OpShiftLeftBy, OpShiftRightBy, OpShiftRightArithmeticBy, OpRotateLeftBy, OpRotateRightBy, OpStore64, OpStore8, OpVStore, OpVInsert, OpVInsertZero, OpVCompare:
		return []int{i.A, i.B}
	case OpSelect, OpFMA, OpFProductError, OpAddWithCarry, OpSubWithBorrow:
		return []int{i.A, i.B, i.C}
	case OpSyscall, OpCallC, OpCallGo:
		return i.Args
	default:
		return []int{i.A}
	}
}

// IsVector reports a semantic vector operation, including scalar lane/mask results.
func (i Instruction) IsVector() bool {
	return i.Type.Kind == VectorKind || i.Type.Kind == MaskKind || i.Op >= OpVLoad && i.Op <= OpVInsertZero
}

func validateVectorInstruction(i Instruction, index int, ops []Instruction) error {
	bad := func() error {
		return fmt.Errorf("vector operation %d at value %d has invalid shape or operands: %w", i.Op, index, ErrInvalidProgram)
	}
	if !i.ValueType().Valid() || !i.ByteOrder.Valid() || i.ByteOrder != ByteOrderNative && i.Op != OpVLoad && i.Op != OpVStore {
		return bad()
	}
	for _, id := range i.Inputs() {
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
		return fmt.Errorf("vector operation %d at value %d has Bits=%d, expected %d for type %+v: %w", i.Op, index, i.Bits, expectedBits, t, ErrInvalidProgram)
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
