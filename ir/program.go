package ir

import (
	"errors"
	"fmt"
)

var ErrInvalidProgram = errors.New("ir: invalid program")

func instructionWidth(ins Instruction) uint8 {
	if ins.Bits == 0 {
		return 64
	}
	return ins.Bits
}

// ScopeExit records a nested Builder scope and the values that cease to be
// visible at its exit. ID zero is reserved for the root scope.
type ScopeExit struct {
	ID       int
	Parent   int
	Enter    int
	Position int
	Hidden   []int
	Exports  []int
}

// Program is a target-independent function with public, mutable fields.
// Validate before compilation when fields may have changed.
type Program struct {
	Variables    []Variable
	Instructions []Instruction
	ScopeExits   []ScopeExit
	Control      *Control
	Arity        int
	Result       int
	// ResultParts contains the low and high 64-bit value IDs for a 128-bit
	// result. When empty, Result is the sole result value.
	ResultParts []int
}

// Validate checks the current Program contents, including labeled control.
func (p *Program) Validate() error {
	if p == nil {
		return ErrInvalidProgram
	}
	return validateProgram(p)
}

// Clone makes an independent snapshot of the Program and its nested slices.
func (p *Program) Clone() *Program {
	if p == nil {
		return nil
	}
	copyProgram := &Program{
		Variables:    make([]Variable, len(p.Variables)),
		Instructions: make([]Instruction, len(p.Instructions)),
		ScopeExits:   make([]ScopeExit, len(p.ScopeExits)),
		Control:      cloneControl(p.Control), Arity: p.Arity, Result: p.Result,
		ResultParts: append([]int(nil), p.ResultParts...),
	}
	for i, ins := range p.Instructions {
		copyProgram.Instructions[i] = ins.Clone()
	}
	for i, v := range p.Variables {
		v.Initial = append([]int(nil), v.Initial...)
		copyProgram.Variables[i] = v
	}
	for i, exit := range p.ScopeExits {
		copyProgram.ScopeExits[i] = ScopeExit{ID: exit.ID, Parent: exit.Parent, Enter: exit.Enter, Position: exit.Position,
			Hidden: append([]int(nil), exit.Hidden...), Exports: append([]int(nil), exit.Exports...)}
	}
	return copyProgram
}

func validateProgram(spec *Program) error {
	if spec.Arity < 0 || len(spec.Instructions) == 0 ||
		spec.Control == nil && (spec.Result < 0 || spec.Result >= len(spec.Instructions)) {
		return ErrInvalidProgram
	}
	if len(spec.ResultParts) != 0 {
		if spec.Control != nil || len(spec.ResultParts) != 2 || spec.ResultParts[0] != spec.Result ||
			spec.ResultParts[0] < 0 || spec.ResultParts[0] >= len(spec.Instructions) ||
			spec.ResultParts[1] < 0 || spec.ResultParts[1] >= len(spec.Instructions) ||
			spec.ResultParts[0] == spec.ResultParts[1] {
			return ErrInvalidProgram
		}
		for _, id := range spec.ResultParts {
			bits := spec.Instructions[id].Bits
			if bits != 0 && bits != 64 {
				return ErrInvalidProgram
			}
		}
	}
	if spec.Control == nil && spec.Instructions[spec.Result].ValueType().Kind != ScalarKind {
		return ErrInvalidProgram
	}
	if err := validateVariables(spec); err != nil {
		return err
	}
	for index, ins := range spec.Instructions {
		if ins.Op == OpGet || ins.Op == OpSet || ins.Op == OpScopeEnter || ins.Op == OpScopeLeave {
			continue
		}
		if ins.IsVector() {
			if err := validateVectorInstruction(ins, index, spec.Instructions); err != nil {
				return err
			}
			continue
		}
		if ins.Type != (Type{}) && ins.Type != Scalar(instructionWidth(ins)) {
			return ErrInvalidProgram
		}
		if ins.Type != (Type{}) && !ins.Type.Valid() {
			return ErrInvalidProgram
		}
		for _, id := range ins.Inputs() {
			if id >= 0 && id < index && spec.Instructions[id].ValueType().Kind != ScalarKind {
				return fmt.Errorf("scalar operation %d at value %d cannot consume vector/mask value %d: %w", ins.Op, index, id, ErrInvalidProgram)
			}
		}
		if !ins.ByteOrder.Valid() || ins.ByteOrder != ByteOrderNative &&
			ins.Op != OpLoad64 && ins.Op != OpStore64 && ins.Op != OpLoad8 && ins.Op != OpStore8 {
			return fmt.Errorf("value %d has invalid memory byte order %d for operation %d: %w", index, ins.ByteOrder, ins.Op, ErrInvalidProgram)
		}
		if ins.Op > OpScopeLeave || ins.Bits != 0 && ins.Bits != 8 && ins.Bits != 16 && ins.Bits != 32 && ins.Bits != 64 {
			return ErrInvalidProgram
		}
		if ins.Op == OpFSqrt && (ins.Bits != 32 && ins.Bits != 64 || ins.A < 0 || ins.A >= index ||
			instructionWidth(spec.Instructions[ins.A]) != ins.Bits) {
			return fmt.Errorf("FSqrt at value %d needs one %d-bit input: %w", index, ins.Bits, ErrInvalidProgram)
		}
		if ins.Op == OpFConvert && (ins.Bits != 32 && ins.Bits != 64 || ins.A < 0 || ins.A >= index ||
			instructionWidth(spec.Instructions[ins.A]) != 96-ins.Bits) {
			return fmt.Errorf("FConvert at value %d needs a %d-bit input: %w", index, 96-ins.Bits, ErrInvalidProgram)
		}
		if ins.Contract && ins.Op != OpFAdd {
			return ErrInvalidProgram
		}
		if (ins.Op == OpFAdd || ins.Op == OpFSub || ins.Op == OpFMul || ins.Op == OpFDiv || ins.Op == OpFMA || ins.Op == OpFProductError) &&
			(ins.Bits != 32 && ins.Bits != 64 || ins.A < 0 || ins.B < 0 || ins.A >= index || ins.B >= index ||
				instructionWidth(spec.Instructions[ins.A]) != ins.Bits || instructionWidth(spec.Instructions[ins.B]) != ins.Bits) {
			return fmt.Errorf("floating operation %d at value %d needs two %d-bit inputs: %w", ins.Op, index, ins.Bits, ErrInvalidProgram)
		}
		if ins.Contract && spec.Instructions[ins.A].Op != OpFMul && spec.Instructions[ins.B].Op != OpFMul {
			return fmt.Errorf("FAdd at value %d permits contraction without an FMul input: %w", index, ErrInvalidProgram)
		}
		if (ins.Op == OpFMA || ins.Op == OpFProductError) && (ins.C < 0 || ins.C >= index || instructionWidth(spec.Instructions[ins.C]) != ins.Bits) {
			return fmt.Errorf("FMA at value %d needs a third %d-bit input: %w", index, ins.Bits, ErrInvalidProgram)
		}
		if ins.Op == OpFProductError && ins.Bits != 64 {
			return ErrInvalidProgram
		}
		width := ins.Bits
		if width == 0 {
			width = 64
		}
		if (ins.Op == OpShiftLeft || ins.Op == OpShiftRight || ins.Op == OpShiftRightArithmetic ||
			ins.Op == OpRotateLeft || ins.Op == OpRotateRight) && ins.Imm >= uint64(width) {
			return ErrInvalidProgram
		}
		if ins.Op == OpSignExtend && ins.Imm != 8 && ins.Imm != 16 && ins.Imm != 32 {
			return ErrInvalidProgram
		}
		if ins.Op == OpAddWithCarry || ins.Op == OpSubWithBorrow || ins.Op == OpCarryAdd || ins.Op == OpBorrowSub {
			if ins.A < 0 || ins.B < 0 || ins.A >= index || ins.B >= index || instructionWidth(spec.Instructions[ins.A]) != width || instructionWidth(spec.Instructions[ins.B]) != width ||
				(ins.Op == OpAddWithCarry || ins.Op == OpSubWithBorrow) && (ins.C < 0 || ins.C >= index || instructionWidth(spec.Instructions[ins.C]) != width) {
				return fmt.Errorf("word carry operation at value %d needs matching %d-bit inputs: %w", index, width, ErrInvalidProgram)
			}
		}
		if ins.Op == OpLeadingZeros || ins.Op == OpTrailingZeros {
			if ins.A < 0 || ins.A >= index || instructionWidth(spec.Instructions[ins.A]) != width || width > 64 {
				return fmt.Errorf("bit count at value %d needs a matching scalar width: %w", index, ErrInvalidProgram)
			}
		}
		if ins.Op == OpMulHigh && (ins.A < 0 || ins.B < 0 || ins.A >= index || ins.B >= index ||
			instructionWidth(spec.Instructions[ins.A]) != width || instructionWidth(spec.Instructions[ins.B]) != width) {
			return fmt.Errorf("high multiply at value %d needs two %d-bit inputs: %w", index, width, ErrInvalidProgram)
		}
		if _, by := ShiftByOperation(ins.Op); by {
			if ins.A < 0 || ins.B < 0 || ins.A >= index || ins.B >= index ||
				instructionWidth(spec.Instructions[ins.A]) != width || instructionWidth(spec.Instructions[ins.B]) > 64 {
				return fmt.Errorf("variable shift at value %d needs a %d-bit input and a scalar unsigned count: %w", index, width, ErrInvalidProgram)
			}
		}
		validInput := func(id int) bool { return id >= 0 && id < index }
		switch ins.Op {
		case OpCarryAdd, OpBorrowSub, OpMulHigh, OpShiftLeftBy, OpShiftRightBy, OpShiftRightArithmeticBy, OpRotateLeftBy, OpRotateRightBy, OpAdd, OpSub, OpMul, OpAnd, OpOr, OpXor, OpAddScaled, OpFAdd, OpFSub, OpFMul, OpFDiv, OpStore64, OpStore8:
			if !validInput(ins.A) || !validInput(ins.B) {
				return ErrInvalidProgram
			}
		case OpSelect:
			if !validInput(ins.A) || !validInput(ins.B) || !validInput(ins.C) {
				return ErrInvalidProgram
			}
		case OpAddWithCarry, OpSubWithBorrow, OpFMA, OpFProductError:
			if !validInput(ins.A) || !validInput(ins.B) || !validInput(ins.C) {
				return ErrInvalidProgram
			}
		case OpSet, OpLeadingZeros, OpTrailingZeros, OpFConvert, OpFSqrt, OpLoad64, OpLoad8, OpShiftRight, OpCastUnsigned, OpSignExtend,
			OpShiftLeft, OpShiftRightArithmetic, OpRotateLeft, OpRotateRight:
			if !validInput(ins.A) {
				return ErrInvalidProgram
			}
		case OpSyscall, OpCallC, OpCallGo:
			if len(ins.Args) == 0 {
				return ErrInvalidProgram
			}
			for _, id := range ins.Args {
				if !validInput(id) {
					return ErrInvalidProgram
				}
			}
		}
		// Width-changing casts, memory addresses, call arguments, Select's
		// condition and variable shift counts have separate operand contracts.
		// Ordinary integer arithmetic and immediate shifts do not implicitly
		// truncate an operand of a different width.
		var sameWidth []int
		switch ins.Op {
		case OpAdd, OpSub, OpMul, OpAnd, OpOr, OpXor, OpAddScaled:
			sameWidth = []int{ins.A, ins.B}
		case OpShiftLeft, OpShiftRight, OpShiftRightArithmetic, OpRotateLeft, OpRotateRight:
			sameWidth = []int{ins.A}
		case OpSelect:
			sameWidth = []int{ins.B, ins.C}
		}
		for _, id := range sameWidth {
			if inputWidth := instructionWidth(spec.Instructions[id]); inputWidth != width {
				return fmt.Errorf("scalar operation %d at value %d needs %d-bit operands; value %d has %d bits: %w", ins.Op, index, width, id, inputWidth, ErrInvalidProgram)
			}
		}
	}
	scopeByID := make(map[int]ScopeExit, len(spec.ScopeExits))
	lastExit := 0
	for _, exit := range spec.ScopeExits {
		if exit.Enter < 0 || exit.Enter > exit.Position || exit.Position > len(spec.Instructions) ||
			exit.ID <= 0 || exit.Parent < 0 || exit.Parent >= exit.ID ||
			exit.Position < lastExit {
			return ErrInvalidProgram
		}
		if _, duplicate := scopeByID[exit.ID]; duplicate {
			return ErrInvalidProgram
		}
		scopeByID[exit.ID] = exit
		lastExit = exit.Position
		for _, id := range exit.Hidden {
			if id < 0 || id >= exit.Position {
				return ErrInvalidProgram
			}
		}
		for _, id := range exit.Exports {
			if id < 0 || id >= exit.Position {
				return ErrInvalidProgram
			}
		}
	}
	for _, exit := range spec.ScopeExits {
		if exit.Parent == 0 {
			continue
		}
		parent, ok := scopeByID[exit.Parent]
		if !ok || exit.Enter < parent.Enter || exit.Position > parent.Position {
			return ErrInvalidProgram
		}
	}
	if control := spec.Control; control != nil {
		if len(control.Labels) != len(control.LabelScopes) || len(control.Labels) != len(control.Params) ||
			len(control.Labels) != len(control.ParamBits) || len(control.Steps) == 0 {
			return ErrInvalidProgram
		}
		for label, bound := range control.Labels {
			if !bound || control.Params[label] != len(control.ParamBits[label]) {
				return ErrInvalidProgram
			}
			for _, bits := range control.ParamBits[label] {
				if bits != 8 && bits != 16 && bits != 32 && bits != 64 {
					return ErrInvalidProgram
				}
			}
		}
		seenLabels := make([]bool, len(control.Labels))
		position, leavePosition := 0, 0
		currentLabel := -1
		active := false
		for _, step := range control.Steps {
			if step.IsLabel {
				currentLabel = step.Label
				if step.Label < 0 || step.Label >= len(control.Labels) || seenLabels[step.Label] {
					return ErrInvalidProgram
				}
				seenLabels[step.Label] = true
				active = true
				continue
			}
			if !active || step.Term > TermReturn || step.Start != position || step.Start > step.End || step.End > len(spec.Instructions) ||
				step.LeaveStart != leavePosition || step.LeaveStart > step.LeaveEnd || step.LeaveEnd > len(spec.ScopeExits) {
				return ErrInvalidProgram
			}
			active = false
			position, leavePosition = step.End, step.LeaveEnd
			inSegment := func(id int) bool { return id >= step.Start && id < step.End }
			for index := step.Start; index < step.End; index++ {
				ins := spec.Instructions[index]
				if ins.Op == OpLabelParam && len(control.ParamTypes) != 0 {
					if currentLabel < 0 || ins.Imm >= uint64(control.Params[currentLabel]) || len(control.ParamTypes[currentLabel]) != control.Params[currentLabel] || ins.ValueType() != control.ParamTypes[currentLabel][ins.Imm] {
						return ErrInvalidProgram
					}
				}
				inputs := []int(nil)
				if ins.IsVector() {
					inputs = ins.Inputs()
				}
				switch ins.Op {
				case OpCarryAdd, OpBorrowSub, OpMulHigh, OpShiftLeftBy, OpShiftRightBy, OpShiftRightArithmeticBy, OpRotateLeftBy, OpRotateRightBy, OpAdd, OpSub, OpMul, OpAnd, OpOr, OpXor, OpAddScaled, OpFAdd, OpFSub, OpFMul, OpFDiv, OpStore64, OpStore8:
					inputs = []int{ins.A, ins.B}
				case OpSelect:
					inputs = []int{ins.A, ins.B, ins.C}
				case OpAddWithCarry, OpSubWithBorrow, OpFMA, OpFProductError:
					inputs = []int{ins.A, ins.B, ins.C}
				case OpSet, OpLeadingZeros, OpTrailingZeros, OpFConvert, OpFSqrt, OpLoad64, OpLoad8, OpShiftRight, OpCastUnsigned, OpSignExtend,
					OpShiftLeft, OpShiftRightArithmetic, OpRotateLeft, OpRotateRight:
					inputs = []int{ins.A}
				case OpScopeEnter, OpSyscall, OpCallC, OpCallGo:
					inputs = ins.Args
				}
				for _, id := range inputs {
					if !inSegment(id) {
						return fmt.Errorf("instruction %d input %d outside block %d..%d: %w", index, id, step.Start, step.End, ErrInvalidProgram)
					}
				}
			}
			if step.Term != TermReturn && (step.To < 0 || step.To >= len(control.Labels)) {
				return ErrInvalidProgram
			}
			if step.Term == TermBranch && (step.Other < 0 || step.Other >= len(control.Labels) || step.Predicate == nil ||
				!validPredicate(step.Predicate, inSegment, spec.Instructions)) {
				return ErrInvalidProgram
			}
			if step.Term == TermReturn && !inSegment(step.Result) {
				return ErrInvalidProgram
			}
			if len(step.ResultParts) != 0 {
				if step.Term != TermReturn || len(step.ResultParts) != 2 || step.ResultParts[0] != step.Result ||
					step.ResultParts[0] == step.ResultParts[1] {
					return ErrInvalidProgram
				}
				for _, id := range step.ResultParts {
					if !inSegment(id) || spec.Instructions[id].Bits != 0 && spec.Instructions[id].Bits != 64 {
						return ErrInvalidProgram
					}
				}
			}
			for _, ids := range [][]int{step.Values, step.Args, step.OtherArgs} {
				for _, id := range ids {
					if !inSegment(id) {
						return ErrInvalidProgram
					}
				}
			}
		}
		if active || position != len(spec.Instructions) || leavePosition != len(spec.ScopeExits) {
			return ErrInvalidProgram
		}
		for _, seen := range seenLabels {
			if !seen {
				return ErrInvalidProgram
			}
		}
		incoming := make([]int, len(control.Labels))
		if len(control.ParamTypes) != 0 && len(control.ParamTypes) != len(control.Labels) {
			return ErrInvalidProgram
		}
		for _, step := range control.Steps {
			if step.IsLabel {
				continue
			}
			if step.Term == TermReturn {
				if spec.Instructions[step.Result].ValueType().Kind != ScalarKind {
					return ErrInvalidProgram
				}
				continue
			}
			edges := []struct {
				label int
				args  []int
			}{{step.To, step.Args}}
			if step.Term == TermBranch {
				edges = append(edges, struct {
					label int
					args  []int
				}{step.Other, step.OtherArgs})
			}
			for _, edge := range edges {
				if len(spec.Variables) > 0 && control.LabelScopes[edge.label] != variableRegionAt(spec, step.End) {
					return fmt.Errorf("branch to label %d leaves its scope: %w", edge.label, ErrInvalidProgram)
				}
				if len(edge.args) != control.Params[edge.label] {
					return ErrInvalidProgram
				}
				for i, id := range edge.args {
					expected := Scalar(control.ParamBits[edge.label][i])
					if len(control.ParamTypes) != 0 {
						if len(control.ParamTypes[edge.label]) != control.Params[edge.label] {
							return ErrInvalidProgram
						}
						expected = control.ParamTypes[edge.label][i]
					}
					if spec.Instructions[id].ValueType() != expected {
						return fmt.Errorf("edge to label %d parameter %d has incompatible Type: %w", edge.label, i, ErrInvalidProgram)
					}
				}
			}
			if step.IsLabel || step.Term == TermReturn {
				continue
			}
			incoming[step.To]++
			if step.Term == TermBranch {
				incoming[step.Other]++
			}
		}
		for label, count := range control.Params {
			if count > 0 && label == control.Steps[0].Label {
				return fmt.Errorf("entry label %d cannot take %d incoming parameters: %w", label, count, ErrInvalidProgram)
			}
			if count > 0 && incoming[label] == 0 {
				return fmt.Errorf("label %d has %d parameters but no incoming edge: %w", label, count, ErrInvalidProgram)
			}
		}
	}
	return nil
}

func validPredicate(root *PredicateExpr, validValue func(int) bool, ops []Instruction) bool {
	state := make(map[*PredicateExpr]uint8)
	var visit func(*PredicateExpr) bool
	visit = func(node *PredicateExpr) bool {
		if node == nil || state[node] == 1 {
			return false
		}
		if state[node] == 2 {
			return true
		}
		state[node] = 1
		switch node.Kind {
		case PredicateCompare:
			if node.Condition > BranchAbove || !validValue(node.Value) || node.WithValue && !validValue(node.Other) ||
				node.HasAnchor && !validValue(node.Anchor) {
				return false
			}
			left := ops[node.Value].ValueType()
			if left.Kind != ScalarKind || left.LaneBits > 64 ||
				node.WithValue && ops[node.Other].ValueType() != left ||
				node.HasAnchor && ops[node.Anchor].ValueType().Kind != ScalarKind {
				return false
			}
		case PredicateNot:
			if !visit(node.Left) {
				return false
			}
		case PredicateAnd, PredicateOr:
			if !visit(node.Left) || !visit(node.Right) {
				return false
			}
		default:
			return false
		}
		state[node] = 2
		return true
	}
	return visit(root)
}
