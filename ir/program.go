package ir

import (
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/op"
)

var ErrInvalidProgram = errors.New("ir: invalid program")

func instructionWidth(ins *Instruction) uint8 {
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

// Program is a mutable target-independent construction/inspection form.
// Seal detaches and validates it for reuse. Compiler raw adapters seal afresh
// on each call; Builder.Finish returns an immutable SealedProgram instead.
type Program struct {
	// ParameterTypes records host Go aggregate entry types. Nil entries are raw
	// scalar parameters. Reflection metadata is not a JSON wire format.
	ParameterTypes []reflect.Type `json:"-"`
	Variables      []Variable
	Instructions   []Instruction
	ScopeExits     []ScopeExit
	Control        *Control
	Arity          int
	// Result must name a value. Control programs may use -1 (no global result).
	Result int
	// ResultParts contains the low and high 64-bit value IDs for a 128-bit
	// result. When empty, Result is the sole result value.
	ResultParts []int
}

// Validate checks the current Program contents, including labeled control.
// An invalid program returns a *ValidationError, which wraps ErrInvalidProgram
// and names the offending instruction where there is one. Predicates
// whose expanded size exceeds MaxPredicateNodes, per predicate or in total,
// return an error wrapping ErrPredicateLimit instead.
func (p *Program) Validate() error {
	if p == nil {
		return invalidRule("nil program")
	}
	if len(p.ParameterTypes) != 0 && len(p.ParameterTypes) != p.Arity {
		return invalidRule("ParameterTypes has %d entries for arity %d", len(p.ParameterTypes), p.Arity)
	}
	for _, typ := range p.ParameterTypes {
		if typ != nil && typ.Kind() != reflect.Struct && typ.Kind() != reflect.Array && typ.Kind() != reflect.Slice && typ.Kind() != reflect.Pointer {
			return invalidRule("parameter type %s is not a struct, array, slice or pointer", typ)
		}
	}
	if p.Control != nil {
		c := p.Control
		if len(c.Labels) != len(c.LabelScopes) || len(c.Params) != len(c.Labels) || len(c.ParamBits) != len(c.Labels) || len(c.ParamTypes) != 0 && len(c.ParamTypes) != len(c.Labels) {
			return invalidRule("Control label tables (Labels, LabelScopes, Params, ParamBits, ParamTypes) differ in length")
		}
		for i := range c.Labels {
			if !c.Labels[i] || c.Params[i] < 0 || c.Params[i] != len(c.ParamBits[i]) || len(c.ParamTypes) != 0 && len(c.ParamTypes[i]) != c.Params[i] {
				return invalidRule("label %d: undeclared, or Params disagrees with ParamBits/ParamTypes", i)
			}
			for _, bits := range c.ParamBits[i] {
				if bits != 8 && bits != 16 && bits != 32 && bits != 64 {
					return invalidRule("label %d: parameter width %d is not 8, 16, 32 or 64", i, bits)
				}
			}
			if len(c.ParamTypes) != 0 {
				for j, typ := range c.ParamTypes[i] {
					bits := typ.LaneBits
					if typ.Kind == MaskKind {
						bits = 64
					}
					if !typ.Valid() || c.ParamBits[i][j] != bits {
						return invalidRule("label %d parameter %d: ParamBits disagrees with ParamTypes", i, j)
					}
				}
			}
		}
		total := 0
		for index := range p.Control.Steps {
			step := &p.Control.Steps[index]
			if rule := controlStepFieldRule(step); rule != "" {
				return invalidRule("step %d: %s", index, rule)
			}
			work, err := predicateWork(step.Predicate)
			if err != nil {
				return prefixRule(err, "step %d", index)
			}
			total += work
			if total > MaxPredicateNodes {
				return fmt.Errorf("step %d: program predicate budget %d exceeded: %w", index, MaxPredicateNodes, ErrPredicateLimit)
			}
		}
	}
	return validateProgram(p)
}

// Clone makes an independent snapshot of the Program and its nested slices.
func (p *Program) Clone() *Program {
	if p == nil {
		return nil
	}
	copyProgram := &Program{
		ParameterTypes: append([]reflect.Type(nil), p.ParameterTypes...),
		Variables:      make([]Variable, len(p.Variables)),
		Instructions:   make([]Instruction, len(p.Instructions)),
		ScopeExits:     make([]ScopeExit, len(p.ScopeExits)),
		Control:        cloneControl(p.Control), Arity: p.Arity, Result: p.Result,
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
	var inputBuffer [4]int
	if spec.Arity < 0 || len(spec.Instructions) == 0 ||
		spec.Control == nil && (spec.Result < 0 || spec.Result >= len(spec.Instructions)) {
		return invalidRule("program needs a nonnegative arity, instructions, and a Result in range")
	}
	if spec.Control == nil || spec.Result != -1 {
		if spec.Result < 0 || spec.Result >= len(spec.Instructions) || !variableValue(spec.Instructions[spec.Result]) {
			return invalidRule("Result %d must produce a value", spec.Result)
		}
	}
	if len(spec.ResultParts) != 0 {
		if spec.Control != nil || len(spec.ResultParts) != 2 || spec.ResultParts[0] != spec.Result ||
			spec.ResultParts[0] < 0 || spec.ResultParts[0] >= len(spec.Instructions) ||
			spec.ResultParts[1] < 0 || spec.ResultParts[1] >= len(spec.Instructions) ||
			spec.ResultParts[0] == spec.ResultParts[1] {
			return invalidRule("ResultParts must be two distinct in-range values starting with Result, outside control flow")
		}
		for _, id := range spec.ResultParts {
			if !variableValue(spec.Instructions[id]) {
				return invalidAt(spec.Instructions, id, "ResultParts operand must produce a value")
			}
			if spec.Instructions[id].ValueType() != Scalar(64) {
				return invalidAt(spec.Instructions, id, "ResultParts operand must be a 64-bit scalar")
			}
		}
	}
	if spec.Control == nil && spec.Instructions[spec.Result].ValueType().Kind != ScalarKind {
		return invalidAt(spec.Instructions, spec.Result, "program Result must be a scalar")
	}
	scoped := len(spec.Variables) > 0 || len(spec.ScopeExits) > 0
	var regions []int
	if scoped {
		for _, scope := range spec.ScopeExits {
			if scope.Enter < 0 || scope.Position <= scope.Enter || scope.Position > len(spec.Instructions) {
				return invalidRule("scope %d has invalid instruction bounds", scope.ID)
			}
		}
		regions = variableRegions(spec)
		if err := validateVariables(spec, regions); err != nil {
			return err
		}
	}
	parameters := 0
	for index := range spec.Instructions {
		ins := &spec.Instructions[index]
		if ins.Op >= opcodeEnd {
			return atInstruction(invalidInstructionField(ins.Op, "opcode"), spec.Instructions, index)
		}
		fields := instructionFieldRules[ins.Op]
		// Plain arithmetic carries no optional metadata. Prove that all of it
		// is absent in one combined test, and keep its operand checks in this
		// walk. Anything carrying metadata takes the full audit field checks.
		if ins.EntryReg|ins.Variable|len(ins.Data)|len(ins.Symbol)|len(ins.Args) != 0 ||
			ins.ByteOrder != ByteOrderNative || ins.Effect != nil || ins.Contract || ins.Aligned || fields&fieldScope != 0 {
			if err := validateInstructionFields(ins, fields); err != nil {
				return atInstruction(err, spec.Instructions, index)
			}
		} else if ins.A != 0 && fields&fieldA == 0 || ins.B != 0 && fields&fieldB == 0 ||
			ins.C != 0 && fields&fieldC == 0 || ins.Imm != 0 && fields&fieldImm == 0 {
			return atInstruction(invalidInstructionField(ins.Op, "operand/immediate"), spec.Instructions, index)
		}
		if fields&checkLabelParameter != 0 && spec.Control == nil {
			return invalidAt(spec.Instructions, index, "label parameter outside control flow")
		}
		if fields&checkParameter != 0 {
			parameters++
		}
		if fields&checkConstant != 0 && ins.Imm&^integerWidthMask(instructionWidth(ins)) != 0 {
			return invalidAt(spec.Instructions, index, "constant exceeds width")
		}
		if fields&checkLoad64 != 0 && instructionWidth(ins) != 64 || fields&checkLoad8 != 0 && instructionWidth(ins) != 64 && instructionWidth(ins) != 8 {
			return invalidAt(spec.Instructions, index, "inconsistent load width")
		}
		if fields&checkVariable != 0 {
			if !scoped {
				return invalidAt(spec.Instructions, index, "variable access in a program without variables or scopes")
			}
			continue
		}
		vector := ins.IsVector()
		// Check references and scalar operand kinds together. Scoped reference
		// validity was checked by validateVariables; it still needs the kind
		// check. Avoid rediscovering every instruction's inputs a second time.
		for _, id := range ruleInputsInto(fields, ins.A, ins.B, ins.C, ins.Args, inputBuffer[:0]) {
			if !scoped && (id < 0 || id >= index || !variableValue(spec.Instructions[id])) {
				return invalidAt(spec.Instructions, index, "uses non-value %d", id)
			}
			if !vector && id >= 0 && id < index && spec.Instructions[id].Type.Kind != InvalidKind && spec.Instructions[id].Type.Kind != ScalarKind {
				return invalidAt(spec.Instructions, index, "scalar operation cannot consume vector/mask value %d", id)
			}
		}
		if vector {
			if err := validateVectorInstruction(*ins, index, spec.Instructions); err != nil {
				return err
			}
			continue
		}
		if ins.Type != (Type{}) && ins.Type != (Type{Kind: ScalarKind, Lanes: 1, LaneBits: instructionWidth(ins)}) {
			return invalidAt(spec.Instructions, index, "Type disagrees with the scalar width")
		}
		if ins.Bits != 0 && ins.Bits != 8 && ins.Bits != 16 && ins.Bits != 32 && ins.Bits != 64 {
			return invalidAt(spec.Instructions, index, "scalar width %d is not 8, 16, 32 or 64", ins.Bits)
		}
		if ins.Op == OpFSqrt && (ins.Bits != 32 && ins.Bits != 64 || ins.A < 0 || ins.A >= index ||
			instructionWidth(&spec.Instructions[ins.A]) != ins.Bits) {
			return invalidAt(spec.Instructions, index, "FSqrt needs one %d-bit input", ins.Bits)
		}
		if ins.Op == OpFConvert && (ins.Bits != 32 && ins.Bits != 64 || ins.A < 0 || ins.A >= index ||
			instructionWidth(&spec.Instructions[ins.A]) != 96-ins.Bits) {
			return invalidAt(spec.Instructions, index, "FConvert needs a %d-bit input", 96-ins.Bits)
		}
		switch ins.Op {
		case OpFFromSigned, OpFFromUnsigned, OpFToSigned, OpFToUnsigned:
			if ins.Bits != 32 && ins.Bits != 64 || ins.A < 0 || ins.A >= index ||
				instructionWidth(&spec.Instructions[ins.A]) != 32 && instructionWidth(&spec.Instructions[ins.A]) != 64 {
				return invalidAt(spec.Instructions, index, "%v needs Bits 32 or 64 and a 32- or 64-bit input", ins.Op)
			}
			if (ins.Op == OpFToSigned || ins.Op == OpFToUnsigned) && (ins.Imm > 0xff || !ConversionSemantics(ins.Imm).Valid()) {
				return invalidAt(spec.Instructions, index, "conversion semantics %d is not defined", ins.Imm)
			}
		case OpDiv, OpDivSigned, OpRem, OpRemSigned:
			if ins.Imm > 0xff || !DivisionSemantics(ins.Imm).Valid() {
				return invalidAt(spec.Instructions, index, "division semantics %d is not defined", ins.Imm)
			}
		}
		if fields&fieldOrder != 0 {
			if ins.A < 0 || ins.A >= index {
				return invalidAt(spec.Instructions, index, "memory operation needs a preceding address")
			}
			addressBits := instructionWidth(&spec.Instructions[ins.A])
			if addressBits != 32 && addressBits != 64 {
				return invalidAt(spec.Instructions, index, "memory operation needs a 32- or 64-bit address")
			}
		}
		if ins.Op == OpStore64 && (ins.B < 0 || ins.B >= index || instructionWidth(&spec.Instructions[ins.B]) != 64) {
			return invalidAt(spec.Instructions, index, "Store64 needs a 64-bit value")
		}
		if bits := ins.NarrowMemoryBits(); bits != 0 {
			if ins.Bits != bits || ins.A < 0 || ins.A >= index {
				return invalidAt(spec.Instructions, index, "narrow memory operation needs Bits %d and a preceding address", bits)
			}
			if ins.Op == OpStore16 || ins.Op == OpStore32 {
				if ins.B < 0 || ins.B >= index {
					return invalidAt(spec.Instructions, index, "narrow store needs a preceding value")
				}
				dataBits := instructionWidth(&spec.Instructions[ins.B])
				if dataBits != bits && dataBits != 64 {
					return invalidAt(spec.Instructions, index, "narrow store value has %d bits; want %d or 64", dataBits, bits)
				}
			}
		} else if ins.Aligned {
			return invalidAt(spec.Instructions, index, "Aligned on a non-narrow memory operation")
		}
		if ins.Contract && ins.Op != OpFAdd {
			return invalidAt(spec.Instructions, index, "Contract on an operation other than FAdd")
		}
		if (ins.Op == OpFAdd || ins.Op == OpFSub || ins.Op == OpFMul || ins.Op == OpFDiv || ins.Op == OpFMA || ins.Op == OpFProductError) &&
			(ins.Bits != 32 && ins.Bits != 64 || ins.A < 0 || ins.B < 0 || ins.A >= index || ins.B >= index ||
				instructionWidth(&spec.Instructions[ins.A]) != ins.Bits || instructionWidth(&spec.Instructions[ins.B]) != ins.Bits) {
			return invalidAt(spec.Instructions, index, "floating operation needs two %d-bit inputs", ins.Bits)
		}
		if ins.Op == OpFCompare && (ins.Bits != 32 && ins.Bits != 64 || ins.A < 0 || ins.B < 0 || ins.A >= index || ins.B >= index ||
			instructionWidth(&spec.Instructions[ins.A]) != ins.Bits || instructionWidth(&spec.Instructions[ins.B]) != ins.Bits) {
			return invalidAt(spec.Instructions, index, "float comparison needs two %d-bit inputs", ins.Bits)
		}
		if ins.Op == OpFCompare && ins.Imm > uint64(FloatUnorderedEqual) {
			return invalidAt(spec.Instructions, index, "float condition %d is not defined", ins.Imm)
		}
		if ins.Contract && spec.Instructions[ins.A].Op != OpFMul && spec.Instructions[ins.B].Op != OpFMul {
			return invalidAt(spec.Instructions, index, "FAdd permits contraction without an FMul input")
		}
		if (ins.Op == OpFMA || ins.Op == OpFProductError) && (ins.C < 0 || ins.C >= index || instructionWidth(&spec.Instructions[ins.C]) != ins.Bits) {
			return invalidAt(spec.Instructions, index, "FMA needs a third %d-bit input", ins.Bits)
		}
		if ins.Op == OpFProductError && ins.Bits != 64 {
			return invalidAt(spec.Instructions, index, "FProductError needs 64-bit operands")
		}
		width := ins.Bits
		if width == 0 {
			width = 64
		}
		if (ins.Op == OpShiftLeft || ins.Op == OpShiftRight || ins.Op == OpShiftRightArithmetic ||
			ins.Op == OpRotateLeft || ins.Op == OpRotateRight) && ins.Imm >= uint64(width) {
			return invalidAt(spec.Instructions, index, "shift or rotate count %d is not below the width %d", ins.Imm, width)
		}
		if ins.Op == OpSignExtend && ins.Imm != 8 && ins.Imm != 16 && ins.Imm != 32 {
			return invalidAt(spec.Instructions, index, "SignExtend source width %d is not 8, 16 or 32", ins.Imm)
		}
		if ins.Op == OpSignExtend && (ins.Imm > uint64(width) || ins.Imm > uint64(instructionWidth(&spec.Instructions[ins.A]))) {
			return invalidAt(spec.Instructions, index, "SignExtend source width %d exceeds the result or input width", ins.Imm)
		}
		if ins.Op == OpAddWithCarry || ins.Op == OpSubWithBorrow || ins.Op == OpCarryAdd || ins.Op == OpBorrowSub {
			if ins.A < 0 || ins.B < 0 || ins.A >= index || ins.B >= index || instructionWidth(&spec.Instructions[ins.A]) != width || instructionWidth(&spec.Instructions[ins.B]) != width ||
				(ins.Op == OpAddWithCarry || ins.Op == OpSubWithBorrow) && (ins.C < 0 || ins.C >= index || instructionWidth(&spec.Instructions[ins.C]) != width) {
				return invalidAt(spec.Instructions, index, "word carry operation needs matching %d-bit inputs", width)
			}
		}
		if ins.Op == OpLeadingZeros || ins.Op == OpTrailingZeros {
			if ins.A < 0 || ins.A >= index || instructionWidth(&spec.Instructions[ins.A]) != width || width > 64 {
				return invalidAt(spec.Instructions, index, "bit count needs a matching scalar width")
			}
		}
		if (ins.Op == OpMulHigh || ins.Op == OpMulHighSigned) && (ins.A < 0 || ins.B < 0 || ins.A >= index || ins.B >= index ||
			instructionWidth(&spec.Instructions[ins.A]) != width || instructionWidth(&spec.Instructions[ins.B]) != width) {
			return invalidAt(spec.Instructions, index, "high multiply needs two %d-bit inputs", width)
		}
		if (ins.Op == OpDiv || ins.Op == OpDivSigned || ins.Op == OpRem || ins.Op == OpRemSigned) && (ins.A < 0 || ins.B < 0 || ins.A >= index || ins.B >= index ||
			instructionWidth(&spec.Instructions[ins.A]) != width || instructionWidth(&spec.Instructions[ins.B]) != width) {
			return invalidAt(spec.Instructions, index, "division needs two %d-bit inputs", width)
		}
		if _, by := op.ShiftByOperation(ins.Op); by {
			if ins.A < 0 || ins.B < 0 || ins.A >= index || ins.B >= index ||
				instructionWidth(&spec.Instructions[ins.A]) != width || instructionWidth(&spec.Instructions[ins.B]) > 64 {
				return invalidAt(spec.Instructions, index, "variable shift needs a %d-bit input and a scalar unsigned count", width)
			}
		}
		validInput := func(id int) bool { return id >= 0 && id < index }
		switch ins.Op {
		case OpCarryAdd, OpBorrowSub, OpMulHigh, OpMulHighSigned, OpFCompare, OpDiv, OpDivSigned, OpRem, OpRemSigned, OpShiftLeftBy, OpShiftRightBy, OpShiftRightArithmeticBy, OpRotateLeftBy, OpRotateRightBy, OpAdd, OpSub, OpMul, OpAnd, OpOr, OpXor, OpAddScaled, OpFAdd, OpFSub, OpFMul, OpFDiv, OpStore64, OpStore8, OpStore16, OpStore32:
			if !validInput(ins.A) || !validInput(ins.B) {
				return invalidAt(spec.Instructions, index, "needs two preceding inputs")
			}
		case OpSelect:
			if !validInput(ins.A) || !validInput(ins.B) || !validInput(ins.C) {
				return invalidAt(spec.Instructions, index, "Select needs three preceding inputs")
			}
		case OpAddWithCarry, OpSubWithBorrow, OpFMA, OpFProductError:
			if !validInput(ins.A) || !validInput(ins.B) || !validInput(ins.C) {
				return invalidAt(spec.Instructions, index, "needs three preceding inputs")
			}
		case OpSet, OpLeadingZeros, OpTrailingZeros, OpFConvert, OpFSqrt, OpFFromSigned, OpFFromUnsigned, OpFToSigned, OpFToUnsigned, OpLoad64, OpLoad8, OpLoad16, OpLoad32, OpShiftRight, OpCastUnsigned, OpSignExtend,
			OpShiftLeft, OpShiftRightArithmetic, OpRotateLeft, OpRotateRight:
			if !validInput(ins.A) {
				return invalidAt(spec.Instructions, index, "needs a preceding input")
			}
		case OpSyscall, OpCallC, OpCallGo:
			if len(ins.Args) == 0 {
				return invalidAt(spec.Instructions, index, "call needs at least one argument")
			}
			for _, id := range ins.Args {
				if !validInput(id) {
					return invalidAt(spec.Instructions, index, "call argument %d is not a preceding value", id)
				}
			}
			if err := validateCallEffect(ins.Op, ins.Effect); err != nil {
				return atInstruction(err, spec.Instructions, index)
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
			if inputWidth := instructionWidth(&spec.Instructions[id]); inputWidth != width {
				return invalidAt(spec.Instructions, index, "scalar operation needs %d-bit operands; value %d has %d bits", width, id, inputWidth)
			}
		}
	}
	scopeByID := make(map[int]ScopeExit, len(spec.ScopeExits))
	lastExit := 0
	for _, exit := range spec.ScopeExits {
		if exit.Enter < 0 || exit.Enter > exit.Position || exit.Position > len(spec.Instructions) ||
			exit.ID <= 0 || exit.Parent < 0 || exit.Parent >= exit.ID ||
			exit.Position < lastExit {
			return invalidRule("scope %d: invalid ID, parent, bounds or exit order", exit.ID)
		}
		if _, duplicate := scopeByID[exit.ID]; duplicate {
			return invalidRule("scope %d: duplicate ID", exit.ID)
		}
		scopeByID[exit.ID] = exit
		lastExit = exit.Position
		for fieldIndex, ids := range [2][]int{exit.Hidden, exit.Exports} {
			field := [2]string{"Hidden", "Exports"}[fieldIndex]
			for _, id := range ids {
				if id < 0 || id >= exit.Position {
					return invalidRule("scope %d: %s operand %d outside scope", exit.ID, field, id)
				}
				ins := spec.Instructions[id]
				if !variableValue(ins) {
					return invalidRule("scope %d: %s operand %d cannot export/hide %s", exit.ID, field, id, ins.Op)
				}
			}
		}
	}
	for _, exit := range spec.ScopeExits {
		if exit.Parent == 0 {
			continue
		}
		parent, ok := scopeByID[exit.Parent]
		if !ok || exit.Enter < parent.Enter || exit.Position > parent.Position {
			return invalidRule("scope %d: not nested in parent scope %d", exit.ID, exit.Parent)
		}
	}
	if parameters != spec.Arity {
		return invalidRule("parameter count %d differs from arity %d", parameters, spec.Arity)
	}

	if control := spec.Control; control != nil {
		if len(control.Labels) != len(control.LabelScopes) || len(control.Labels) != len(control.Params) ||
			len(control.Labels) != len(control.ParamBits) || len(control.Steps) == 0 {
			return invalidRule("Control label tables differ in length, or there are no steps")
		}
		seenLabels := make([]bool, len(control.Labels))
		// blockOf maps each label to the block it is bound to; labels bound
		// before the same segment alias one block.
		blockOf := make([]int, len(control.Labels))
		position, leavePosition := 0, 0
		var labelBits []uint8
		var labelTypes []Type
		typedLabels := len(control.ParamTypes) != 0
		active := false
		entrySegment := true
		// Uses of values from earlier blocks are checked against dominance
		// once the whole graph is known.
		var outer []outerUse
		block := -1
		for stepIndex, step := range control.Steps {
			if step.IsLabel {
				if step.Label < 0 || step.Label >= len(control.Labels) || seenLabels[step.Label] {
					return invalidRule("step %d: label %d is out of range or bound twice", stepIndex, step.Label)
				}
				seenLabels[step.Label] = true
				// The block's OpLabelParam reads one parameter list, so every
				// label of the block must declare it.
				if active && (!slices.Equal(labelBits, control.ParamBits[step.Label]) ||
					typedLabels && !slices.Equal(labelTypes, control.ParamTypes[step.Label])) {
					return invalidRule("step %d: label %d is bound to the same block as an earlier label with different parameters", stepIndex, step.Label)
				}
				blockOf[step.Label] = block + 1
				labelBits = control.ParamBits[step.Label]
				if typedLabels {
					labelTypes = control.ParamTypes[step.Label]
				}
				active = true
				continue
			}
			if !active || step.Term > TermReturn || step.Start != position || step.Start > step.End || step.End > len(spec.Instructions) ||
				step.LeaveStart != leavePosition || step.LeaveStart > step.LeaveEnd || step.LeaveEnd > len(spec.ScopeExits) {
				return invalidRule("step %d: segment does not follow a label, or its terminator, instruction range or leave range is out of order", stepIndex)
			}
			active = false
			position, leavePosition = step.End, step.LeaveEnd
			block++
			// available accepts a value defined earlier in this block, or in an
			// earlier block, which must dominate this one. at is the using
			// instruction, or -1 for a step operand.
			available := func(id, at int) bool {
				if id >= step.Start && id < step.End {
					return true
				}
				if id < 0 || id >= step.Start {
					return false
				}
				outer = append(outer, outerUse{value: id, block: block, at: at, step: stepIndex})
				return true
			}
			stepOperand := func(id int) bool { return available(id, -1) }
			segmentParameters := 0
			for index := step.Start; index < step.End; index++ {
				ins := &spec.Instructions[index]
				if ins.Op == OpParam {
					if !entrySegment {
						return invalidAt(spec.Instructions, index, "step %d: OpParam outside entry segment", stepIndex)
					}
					segmentParameters++
				}
				if ins.Op == OpLabelParam {
					if ins.Imm >= uint64(len(labelBits)) {
						return invalidAt(spec.Instructions, index, "label parameter index %d out of range", ins.Imm)
					}
					if typedLabels {
						if ins.ValueType() != labelTypes[ins.Imm] {
							return invalidAt(spec.Instructions, index, "label parameter Type disagrees with the label's ParamTypes")
						}
					} else if ins.ValueType().Kind != ScalarKind || instructionWidth(ins) != labelBits[ins.Imm] {
						return invalidAt(spec.Instructions, index, "label parameter must be a scalar of the label's ParamBits width")
					}
				}
				// Every semantic input, including scope handoff Args, comes from
				// this segment or from a dominating block. The same dense operand
				// rule serves ordinary reference validation, without a second
				// opcode list.
				inputs := ruleInputsInto(instructionFieldRules[ins.Op], ins.A, ins.B, ins.C, ins.Args, inputBuffer[:0])
				for _, id := range inputs {
					if !available(id, index) {
						return invalidAt(spec.Instructions, index, "input %d is not defined before block %d..%d or in it", id, step.Start, step.End)
					}
				}
			}
			// Scope handoffs are materialized by this segment's selection. Hidden
			// metadata may span blocks; exported operands must be available here.
			for _, exit := range spec.ScopeExits[step.LeaveStart:step.LeaveEnd] {
				for _, id := range exit.Exports {
					if !stepOperand(id) {
						return invalidRule("step %d: scope %d Exports operand %d is not defined before the segment's end", stepIndex, exit.ID, id)
					}
				}
			}
			if step.Arity != segmentParameters {
				return invalidRule("step %d: arity %d differs from segment parameter count %d", stepIndex, step.Arity, segmentParameters)
			}
			entrySegment = false
			if step.Term != TermReturn && (step.To < 0 || step.To >= len(control.Labels)) {
				return invalidRule("step %d: jump target %d out of range", stepIndex, step.To)
			}
			if step.Term == TermBranch {
				if step.Predicate == nil {
					return invalidRule("step %d: predicate node 0: missing predicate", stepIndex)
				}
				if err := validatePredicate(step.Predicate, stepOperand, step.Values, spec.Instructions); err != nil {
					return prefixRule(err, "step %d", stepIndex)
				}
				if step.Other < 0 || step.Other >= len(control.Labels) {
					return invalidRule("step %d: false branch target %d out of range", stepIndex, step.Other)
				}
			}
			if step.Term != TermJump {
				if !stepOperand(step.Result) || !variableValue(spec.Instructions[step.Result]) {
					return invalidRule("step %d: Result %d must be a value defined before the segment's end", stepIndex, step.Result)
				}
			}
			if len(step.ResultParts) != 0 {
				if step.Term != TermReturn || len(step.ResultParts) != 2 || step.ResultParts[0] != step.Result ||
					step.ResultParts[0] == step.ResultParts[1] {
					return invalidRule("step %d: ResultParts must be two distinct values starting with Result on a return", stepIndex)
				}
				for _, id := range step.ResultParts {
					if !stepOperand(id) || !variableValue(spec.Instructions[id]) || spec.Instructions[id].ValueType() != Scalar(64) {
						return invalidRule("step %d: ResultParts operand %d must be a 64-bit scalar defined before the segment's end", stepIndex, id)
					}
				}
			}
			for fieldIndex, ids := range [3][]int{step.Values, step.Args, step.OtherArgs} {
				field := [3]string{"Values", "Args", "OtherArgs"}[fieldIndex]
				for _, id := range ids {
					if !stepOperand(id) {
						return invalidRule("step %d: %s operand %d is not defined before the segment's end", stepIndex, field, id)
					}
					if !variableValue(spec.Instructions[id]) {
						return invalidRule("step %d: %s operand %d references non-value opcode %s", stepIndex, field, id, spec.Instructions[id].Op)
					}
				}
			}
		}
		if active || position != len(spec.Instructions) || leavePosition != len(spec.ScopeExits) {
			return invalidRule("control ends inside a label, or segments or leaves do not cover the program")
		}
		for _, seen := range seenLabels {
			if !seen {
				return invalidRule("a declared label is never bound")
			}
		}
		// incoming counts edges into each block: a block is entered through
		// any of its labels.
		incoming := make([]int, block+1)
		if len(control.ParamTypes) != 0 && len(control.ParamTypes) != len(control.Labels) {
			return invalidRule("ParamTypes has %d entries for %d labels", len(control.ParamTypes), len(control.Labels))
		}
		wideReturn, scalarReturn := false, false
		for stepIndex, step := range control.Steps {
			if step.IsLabel {
				continue
			}
			if step.Term == TermReturn {
				if spec.Instructions[step.Result].ValueType().Kind != ScalarKind {
					return invalidAt(spec.Instructions, step.Result, "step %d: returned value must be a scalar", stepIndex)
				}
				if len(step.ResultParts) != 0 {
					wideReturn = true
				} else {
					scalarReturn = true
				}
				if wideReturn && scalarReturn {
					return invalidRule("step %d: every return must be 128-bit (ResultParts) or none may be", stepIndex)
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
				if len(spec.Variables) > 0 && control.LabelScopes[edge.label] != regions[step.End] {
					return invalidRule("step %d: branch to label %d leaves its scope", stepIndex, edge.label)
				}
				if len(edge.args) != control.Params[edge.label] {
					return invalidRule("step %d: edge to label %d passes %d arguments for %d parameters", stepIndex, edge.label, len(edge.args), control.Params[edge.label])
				}
				for i, id := range edge.args {
					expected := Scalar(control.ParamBits[edge.label][i])
					if len(control.ParamTypes) != 0 {
						if len(control.ParamTypes[edge.label]) != control.Params[edge.label] {
							return invalidRule("label %d: ParamTypes disagrees with Params", edge.label)
						}
						expected = control.ParamTypes[edge.label][i]
					}
					if spec.Instructions[id].ValueType() != expected {
						return invalidRule("step %d: edge to label %d parameter %d has incompatible Type", stepIndex, edge.label, i)
					}
				}
			}
			if step.IsLabel || step.Term == TermReturn {
				continue
			}
			incoming[blockOf[step.To]]++
			if step.Term == TermBranch {
				incoming[blockOf[step.Other]]++
			}
		}
		for label, count := range control.Params {
			if count > 0 && label == control.Steps[0].Label {
				return invalidRule("entry label %d cannot take %d incoming parameters", label, count)
			}
			if count > 0 && incoming[blockOf[label]] == 0 {
				return invalidRule("label %d has %d parameters but no incoming edge", label, count)
			}
		}
		if len(outer) != 0 {
			graph := newBlockGraph(control)
			dom := newDominance(graph)
			for _, use := range outer {
				if dom.blockDominates(graph.blockOf(use.value), use.block) {
					continue
				}
				if use.at >= 0 {
					return invalidAt(spec.Instructions, use.at, "input %d is defined in a block that does not dominate this one", use.value)
				}
				return invalidRule("step %d: operand %d is defined in a block that does not dominate this one", use.step, use.value)
			}
		}
	}
	return nil
}

// outerUse is a use of a value defined in an earlier block.
type outerUse struct{ value, block, at, step int }

// validatePredicate checks operand materialization as well as types. Small
// value lists avoid a map; larger predicates use constant-time membership.
func validatePredicate(root *PredicateExpr, validValue func(int) bool, values []int, ops []Instruction) error {
	var members map[int]bool
	if len(values) > 8 {
		members = make(map[int]bool, len(values))
		for _, id := range values {
			members[id] = true
		}
	}
	listed := func(id int) bool {
		if members != nil {
			return members[id]
		}
		for _, value := range values {
			if value == id {
				return true
			}
		}
		return false
	}
	var seen map[*PredicateExpr]bool
	if root == nil || root.Kind != PredicateCompare {
		seen = make(map[*PredicateExpr]bool)
	}
	work := []*PredicateExpr{root}
	index := 0
	for len(work) != 0 {
		node := work[len(work)-1]
		work = work[:len(work)-1]
		if seen != nil {
			if seen[node] {
				continue
			}
			seen[node] = true
		}
		nodeIndex := index
		index++
		invalid := func(rule string) error {
			return invalidRule("predicate node %d: %s", nodeIndex, rule)
		}
		if node == nil {
			return invalid("missing child")
		}
		switch node.Kind {
		case PredicateCompare:
			if node.Condition > BranchAbove {
				return invalid("unknown comparison condition")
			}
			operands := [3]int{node.Value, node.Other, node.Anchor}
			for i, id := range operands {
				if i == 1 && !node.WithValue || i == 2 && !node.HasAnchor {
					continue
				}
				if !validValue(id) {
					return invalid(fmt.Sprintf("operand %d outside step", id))
				}
				if !listed(id) {
					return invalid(fmt.Sprintf("operand %d missing from step.Values", id))
				}
				if !variableValue(ops[id]) {
					return invalid(fmt.Sprintf("operand %d references non-value opcode %s", id, ops[id].Op))
				}
			}
			left := ops[node.Value].ValueType()
			if left.Kind != ScalarKind || left.LaneBits > 64 || node.WithValue && ops[node.Other].ValueType() != left || node.HasAnchor && ops[node.Anchor].ValueType().Kind != ScalarKind {
				return invalid("incompatible operand types")
			}
			// Equality and unsigned conditions agree on every target only
			// for immediates in the zero-extended operand's range.
			unsigned := node.Condition == BranchEqual || node.Condition == BranchNotEqual || node.Condition >= BranchBelow
			if !node.WithValue && unsigned && left.LaneBits < 64 && uint64(node.Immediate) >= uint64(1)<<left.LaneBits {
				return invalid(fmt.Sprintf("immediate %d is outside the %d-bit operand's range", node.Immediate, left.LaneBits))
			}
		case PredicateNot:
			work = append(work, node.Left)
		case PredicateAnd, PredicateOr:
			work = append(work, node.Right, node.Left)
		default:
			return invalid("unknown predicate kind")
		}
	}
	return nil
}

// validateCallEffect checks the parts of a custom call effect that no target
// could accept. Lowering still matches the remaining fields against the
// selected target's calling convention.
func validateCallEffect(op Opcode, effect *abi.CallEffect) error {
	if effect == nil {
		return nil
	}
	invalid := func(rule string) error {
		return invalidRule("%s effect %s", op, rule)
	}
	switch {
	case op == OpSyscall:
		return invalid("is only supported on function calls")
	case effect.Kind != abi.FunctionCall:
		return invalid("must describe a function call")
	case op == OpCallC && effect.ABI != abi.C, op == OpCallGo && effect.ABI != abi.Go:
		return invalid("names a different ABI")
	case effect.Architecture == "":
		return invalid("has no architecture")
	case effect.NumberRegister != "":
		return invalid("has a system call number register")
	case effect.ResultRegister == "" && len(effect.ResultLocation.Words) == 0:
		return invalid("describes no result")
	case effect.ResultRegister == "":
		return nil
	}
	for _, name := range effect.ClobberedRegisters {
		if name == effect.ResultRegister {
			return nil
		}
	}
	return invalid("does not clobber its result register")
}
