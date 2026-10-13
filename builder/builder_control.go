package builder

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/ir"
)

// Label identifies a control-flow destination within one Builder.
type Label = ir.Label

const maxSpillSlots = 4096
const implicitEntryLabel Label = -2

type builderTerm uint8

const (
	builderJump builderTerm = iota
	builderBranch
	builderReturn
)

type builderControl struct {
	labels      []bool
	labelScopes []int
	params      []int
	paramBits   [][]uint8
	paramTypes  [][]Type
	steps       []builderControlStep
	active      bool
	hasTerm     bool
	start       int
	leaveStart  int
}

type builderControlStep struct {
	label       Label
	isLabel     bool
	term        builderTerm
	start, end  int
	leaveStart  int
	leaveEnd    int
	result      int
	resultParts []int
	arity       int
	values      []int
	args        []int
	otherArgs   []int
	predicate   *predicateNode
	to, other   Label
}

// NewLabel reserves a position in the current value scope. Control transfers
// can target only labels created in that same scope.
func (b *Builder) NewLabel() Label {
	if b == nil || b.finished || b.returned {
		return -1
	}
	b.ensureRoot()
	if b.control == nil {
		// The function entry is an implicit, zero-argument label. This lets
		// callers reserve destinations before or after emitting entry values.
		b.control = &builderControl{steps: []builderControlStep{{isLabel: true, label: implicitEntryLabel}}, active: true}
	}
	id := Label(len(b.control.labels))
	b.control.labels = append(b.control.labels, false)
	b.control.labelScopes = append(b.control.labelScopes, b.scopes[len(b.scopes)-1])
	b.control.params = append(b.control.params, 0)
	b.control.paramBits = append(b.control.paramBits, nil)
	b.control.paramTypes = append(b.control.paramTypes, nil)
	return id
}

func (b *Builder) validLabel(label Label) bool {
	return b != nil && b.control != nil && label >= 0 && int(label) < len(b.control.labels)
}

func (b *Builder) localLabel(label Label) bool {
	return b.validLabel(label) && len(b.scopes) != 0 && b.control.labelScopes[label] == b.scopes[len(b.scopes)-1]
}

// Bind marks the current position. The label must belong to the current scope.
// Several labels may mark the same position.
// A jump, branch, or return must end the current value sequence before Bind.
func (b *Builder) Bind(label Label) error {
	if !b.localLabel(label) {
		return fmt.Errorf("Bind label %d: label is unknown or belongs to another scope: %w", label, ErrInvalidFunction)
	}
	if b.control.labels[label] {
		return fmt.Errorf("Bind label %d: already bound: %w", label, ErrInvalidFunction)
	}
	if b.control.active && len(b.code) != b.control.start {
		return fmt.Errorf("Bind label %d: current block needs a terminator: %w", label, ErrInvalidFunction)
	}
	b.control.labels[label] = true
	b.control.steps = append(b.control.steps, builderControlStep{isLabel: true, label: label})
	b.control.active = true
	b.control.start = len(b.code)
	b.control.leaveStart = len(b.leaves)
	return nil
}

// BindParams binds a label and defines values supplied by incoming edges.
// Every JumpArgs or BranchArgs edge to this label must supply count values.
func (b *Builder) BindParams(label Label, count int) ([]Value, error) {
	if count < 0 || count > maxSpillSlots {
		return nil, fmt.Errorf("BindParams label %d: parameter count %d exceeds 0..%d: %w", label, count, maxSpillSlots, ErrBackendLimit)
	}
	widths := make([]uint8, count)
	for i := range widths {
		widths[i] = 64
	}
	return b.BindParamsBits(label, widths)
}

// BindParamsBits binds typed integer parameters supplied by incoming edges.
func (b *Builder) BindParamsBits(label Label, widths []uint8) ([]Value, error) {
	expanded := make([]uint8, 0, len(widths))
	for index, width := range widths {
		if width == 128 {
			expanded = append(expanded, 64, 64)
		} else if validScalarBits(width) {
			expanded = append(expanded, width)
		} else {
			return nil, fmt.Errorf("BindParamsBits label %d: parameter %d has unsupported width %d: %w", label, index, width, ErrInvalidFunction)
		}
	}
	if len(expanded) > maxSpillSlots {
		return nil, ErrBackendLimit
	}
	if b == nil || b.control == nil || b.control.active {
		return nil, fmt.Errorf("BindParamsBits label %d: current block must be terminated before binding parameters: %w", label, ErrInvalidFunction)
	}
	if err := b.Bind(label); err != nil {
		return nil, err
	}
	b.control.params[label] = len(expanded)
	b.control.paramBits[label] = expanded
	b.control.paramTypes[label] = make([]Type, len(expanded))
	for i, width := range expanded {
		b.control.paramTypes[label][i] = Scalar(width)
	}
	values := make([]Value, len(widths))
	index := 0
	for i, width := range widths {
		lo := b.append(instruction{Op: opLabelParam, Imm: uint64(index), EntryReg: -1, Bits: min(width, 64)})
		index++
		if width == 128 {
			hi := b.append(instruction{Op: opLabelParam, Imm: uint64(index), EntryReg: -1, Bits: 64})
			index++
			values[i] = compositeValue(lo, hi)
		} else {
			values[i] = lo
		}
	}
	return values, nil
}

func (b *Builder) endControl(term builderTerm, result int, predicate Predicate, to, other Label) {
	if b.scopeTerminated == nil {
		b.scopeTerminated = map[int]bool{}
	}
	b.scopeTerminated[b.scopeID()] = true
	arity := 0
	if !b.control.hasTerm {
		arity = b.arity
	}
	b.control.steps = append(b.control.steps, builderControlStep{
		term: term, start: b.control.start, end: len(b.code),
		leaveStart: b.control.leaveStart, leaveEnd: len(b.leaves),
		result: result, arity: arity, predicate: predicate.node, to: to, other: other,
	})
	b.control.active, b.control.hasTerm = false, true
}

// Jump ends the current value sequence and transfers control to label.
func (b *Builder) Jump(label Label) error {
	return b.JumpArgs(label)
}

// JumpArgs passes virtual values to the target label's BindParams.
func (b *Builder) JumpArgs(label Label, args ...Value) error {
	if !b.canAppend() || b.control == nil || !b.localLabel(label) {
		return fmt.Errorf("JumpArgs label %d: target is unknown, out of scope, or current block is closed: %w", label, ErrInvalidFunction)
	}
	var err error
	args, err = b.materializeValues(args)
	if err != nil {
		return err
	}
	ids := make([]int, 0, len(args))
	for _, value := range args {
		if value.composite != nil {
			if !b.validComposite(value) {
				return ErrInvalidFunction
			}
			for _, part := range value.components() {
				ids = append(ids, part.id)
			}
			continue
		}
		if !b.valid(value) {
			return ErrInvalidFunction
		}
		ids = append(ids, value.id)
	}
	b.endControl(builderJump, -1, Predicate{}, label, -1)
	b.control.steps[len(b.control.steps)-1].args = ids
	return nil
}

// Branch ends the current sequence. With one target and an empty predicate it
// is unconditional. With a nonempty predicate and one target, the false edge
// falls through to the next block. Two targets make both edges explicit.
func (b *Builder) Branch(predicate Predicate, ifTrue Label, ifFalse ...Label) error {
	if len(ifFalse) == 0 {
		if predicate.node != nil || predicate.builder != nil {
			if !b.canAppend() || !b.localLabel(ifTrue) {
				return fmt.Errorf("Branch label %d: target is unknown, out of scope, or current block is closed: %w", ifTrue, ErrInvalidFunction)
			}
			if values, err := b.predicateValues(predicate); err != nil || len(values) == 0 {
				return fmt.Errorf("Branch label %d: predicate is invalid in this block: %w", ifTrue, ErrInvalidFunction)
			}
			fallthroughLabel := b.NewLabel()
			if fallthroughLabel < 0 {
				return fmt.Errorf("Branch: cannot create fallthrough label: %w", ErrInvalidFunction)
			}
			if err := b.BranchArgs(predicate, ifTrue, nil, fallthroughLabel, nil); err != nil {
				return err
			}
			return b.Bind(fallthroughLabel)
		}
		return b.Jump(ifTrue)
	}
	if len(ifFalse) != 1 {
		return ErrInvalidFunction
	}
	return b.BranchArgs(predicate, ifTrue, nil, ifFalse[0], nil)
}

// BranchArgs sends separate virtual-value lists along the selected edge.
func (b *Builder) BranchArgs(predicate Predicate, ifTrue Label, trueArgs []Value, ifFalse Label, falseArgs []Value) error {
	if !b.canAppend() || b.control == nil || !b.localLabel(ifTrue) || !b.localLabel(ifFalse) {
		return fmt.Errorf("BranchArgs labels %d and %d: target is unknown, out of scope, or current block is closed: %w", ifTrue, ifFalse, ErrInvalidFunction)
	}
	var err error
	trueArgs, err = b.materializeValues(trueArgs)
	if err != nil {
		return err
	}
	falseArgs, err = b.materializeValues(falseArgs)
	if err != nil {
		return err
	}
	copyIDs := func(args []Value) ([]int, error) {
		ids := make([]int, 0, len(args))
		for _, value := range args {
			if value.composite != nil {
				if !b.validComposite(value) {
					return nil, ErrInvalidFunction
				}
				for _, part := range value.components() {
					ids = append(ids, part.id)
				}
				continue
			}
			if !b.valid(value) {
				return nil, ErrInvalidFunction
			}
			ids = append(ids, value.id)
		}
		return ids, nil
	}
	trueIDs, err := copyIDs(trueArgs)
	if err != nil {
		return err
	}
	falseIDs, err := copyIDs(falseArgs)
	if err != nil {
		return err
	}
	values, err := b.predicateValues(predicate)
	if err != nil || len(values) == 0 {
		return fmt.Errorf("BranchArgs labels %d and %d: predicate is invalid in this block: %w", ifTrue, ifFalse, ErrInvalidFunction)
	}
	b.endControl(builderBranch, values[0], predicate, ifTrue, ifFalse)
	step := &b.control.steps[len(b.control.steps)-1]
	step.values = values
	step.args, step.otherArgs = trueIDs, falseIDs
	return nil
}

// BindParamsTypes binds complete shapes, including mask and vector parameters.
func (b *Builder) BindParamsTypes(label Label, types ...Type) ([]Value, error) {
	expanded := []Type{}
	for _, t := range types {
		if !t.Valid() {
			return nil, ErrInvalidFunction
		}
		if t == Scalar(128) {
			expanded = append(expanded, Scalar(64), Scalar(64))
		} else {
			expanded = append(expanded, t)
		}
	}
	if b == nil || b.control == nil || b.control.active || len(expanded) > maxSpillSlots {
		return nil, ErrInvalidFunction
	}
	if err := b.Bind(label); err != nil {
		return nil, err
	}
	b.control.params[label] = len(expanded)
	b.control.paramTypes[label] = append([]Type(nil), expanded...)
	b.control.paramBits[label] = make([]uint8, len(expanded))
	flat := make([]Value, len(expanded))
	for i, t := range expanded {
		bits := t.LaneBits
		if t.Kind == MaskKind {
			bits = 64
		}
		b.control.paramBits[label][i] = bits
		flat[i] = b.append(instruction{Op: opLabelParam, Type: t, Bits: bits, Imm: uint64(i), EntryReg: -1})
	}
	out := make([]Value, len(types))
	index := 0
	for i, t := range types {
		if t == Scalar(128) {
			out[i] = compositeValue(flat[index], flat[index+1])
			index += 2
		} else {
			out[i] = flat[index]
			index++
		}
	}
	return out, nil
}
