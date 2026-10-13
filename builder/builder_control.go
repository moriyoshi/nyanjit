package builder

import (
	"fmt"
	"slices"
	"sync/atomic"

	"github.com/moriyoshi/nyanjit/ir"
)

// Label is a control-flow destination of one Builder. The zero Label is
// invalid. Labels carry their Builder's identity, so a Label of another
// Builder, or of an earlier run of a generator, is rejected.
type Label struct {
	// builder is the owning Builder's generation, never zero for a Label
	// NewLabel returned.
	builder uint64
	index   uint32
}

// builderGenerations numbers Builders so that Labels can name their owner
// without a pointer.
var builderGenerations atomic.Uint64

func newGeneration() uint64 { return builderGenerations.Add(1) }

const maxSpillSlots = 4096
const implicitEntryLabel ir.Label = -2

type builderTerm uint8

const (
	builderJump builderTerm = iota
	builderBranch
	builderReturn
)

type builderControl struct {
	privateLabels map[ir.Label]bool // Index continuations have exactly one checked incoming edge.
	labels        []bool
	labelScopes   []int
	params        []int
	paramBits     [][]uint8
	paramTypes    [][]ir.Type
	steps         []builderControlStep
	active        bool
	hasTerm       bool
	start         int
	leaveStart    int
	// pendingFalse is the label a one-target Branch bound for its false
	// edge, while that block is still empty, or -1. A Jump from the empty
	// block retargets the Branch's false edge instead of adding a block.
	pendingFalse ir.Label
}

type builderControlStep struct {
	scope       int
	label       ir.Label
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
	to, other   ir.Label
}

// fallthroughLabel asks endControl to create the false edge's label of a
// one-target Branch, after any scope-entry split has created its own.
const fallthroughLabel ir.Label = -3

// NewLabel reserves a position in the current value scope. Control transfers
// can target only labels created in that same scope. After Finish or a
// root Return it returns the zero Label, which every method rejects.
func (b *Builder) NewLabel() Label {
	id := b.newLabel()
	if id < 0 {
		return Label{}
	}
	return Label{builder: b.generation, index: uint32(id)}
}

func (b *Builder) newLabel() ir.Label {
	if b == nil || b.finished || b.returned {
		return -1
	}
	b.ensureRoot()
	if b.control == nil {
		// The function entry is an implicit, zero-argument label. This lets
		// callers reserve destinations before or after emitting entry values.
		if s := b.seg; s != nil {
			// A segment's consumer finds label parameters in the code, so
			// only the label tables the Builder checks are kept.
			b.control = &builderControl{labels: s.spareLabels, labelScopes: s.spareScopes, active: true, pendingFalse: -1}
			s.spareLabels, s.spareScopes = nil, nil
		} else {
			b.control = &builderControl{
				labels: make([]bool, 0, 4), labelScopes: make([]int, 0, 4), params: make([]int, 0, 4),
				paramBits: make([][]uint8, 0, 4), paramTypes: make([][]ir.Type, 0, 4),
				active: true, pendingFalse: -1,
			}
			b.control.steps = make([]builderControlStep, 1, 8)
			b.control.steps[0] = builderControlStep{isLabel: true, label: implicitEntryLabel}
		}
	}
	return b.addLabel(b.scopes[len(b.scopes)-1], false)
}

// addLabel appends a label of scope to the label tables.
func (b *Builder) addLabel(scope int, bound bool) ir.Label {
	c := b.control
	id := ir.Label(len(c.labels))
	c.labels = append(c.labels, bound)
	c.labelScopes = append(c.labelScopes, scope)
	if b.seg == nil {
		c.params = append(c.params, 0)
		c.paramBits = append(c.paramBits, nil)
		c.paramTypes = append(c.paramTypes, nil)
	}
	return id
}

// dropLastLabel removes the last label, which no edge names.
func (b *Builder) dropLastLabel() {
	c := b.control
	n := len(c.labels) - 1
	c.labels, c.labelScopes = c.labels[:n], c.labelScopes[:n]
	if b.seg == nil {
		c.params, c.paramBits, c.paramTypes = c.params[:n], c.paramBits[:n], c.paramTypes[:n]
	}
}

// labelIndex maps a Label of this Builder to its IR label.
func (b *Builder) labelIndex(label Label) (ir.Label, bool) {
	if b == nil || b.control == nil || label.builder == 0 || label.builder != b.generation || uint64(label.index) >= uint64(len(b.control.labels)) {
		return -1, false
	}
	return ir.Label(label.index), true
}

// localLabel maps a public Label that the current scope may target.
func (b *Builder) localLabel(label Label) (ir.Label, bool) {
	id, ok := b.labelIndex(label)
	if !ok || !b.localIndex(id) {
		return -1, false
	}
	return id, true
}

func (b *Builder) localIndex(id ir.Label) bool {
	return id >= 0 && int(id) < len(b.control.labels) && len(b.scopes) != 0 && b.control.labelScopes[id] == b.scopes[len(b.scopes)-1]
}

func (b *Builder) labelError(method string, label Label) error {
	switch {
	case label.builder == 0:
		return fmt.Errorf("%s: the zero Label is invalid: %w", method, ErrInvalidFunction)
	case b == nil || label.builder != b.generation:
		return fmt.Errorf("%s: label belongs to another Builder: %w", method, ErrInvalidFunction)
	}
	return fmt.Errorf("%s label %d: label is unknown or belongs to another scope: %w", method, label.index, ErrInvalidFunction)
}

// Bind marks the current position. The label must belong to the current scope.
// Several labels may mark the same position.
// If the current value sequence is still open and nonempty, Bind terminates
// it with an implicit edge to the label.
func (b *Builder) Bind(label Label) error {
	if b != nil {
		b.detachCopy()
	}
	if b != nil && b.seg != nil && b.seg.failed {
		return fmt.Errorf("Bind: segment emission failed: %w", ErrInvalidFunction)
	}
	id, ok := b.localLabel(label)
	if !ok || b.control.privateLabels[id] {
		return b.labelError("Bind", label)
	}
	return b.bind(id, false)
}

// bind starts a block at label id. A one-target Branch binds its fallthrough
// label with falseEdge set: in segment mode, the Branch's block then stays
// staged until the next operation shows whether a Jump retargets its false
// edge.
func (b *Builder) bind(id ir.Label, falseEdge bool) error {
	if b.control.labels[id] {
		return fmt.Errorf("Bind label %d: already bound: %w", id, ErrInvalidFunction)
	}
	var err error
	if b.seg != nil && b.seg.staged && !falseEdge {
		err = b.flushStaged()
	}
	if b.control.active && b.codeLen() != b.control.start {
		// An immediately preceding Enter supplies the scope entry edge.
		// Otherwise this is an ordinary implicit fallthrough edge.
		entry := len(b.scopes) > 1 && b.codeLen() == b.scopeStarts[b.scopeID()]+1
		terminated := b.scopeTerminated[b.scopeID()]
		if _, endErr := b.endControl(controlEnd{term: builderJump, result: -1, to: id, other: -1}); err == nil {
			err = endErr
		}
		if entry {
			b.scopeTerminated[b.scopeID()] = terminated
		}
	}
	b.control.labels[id] = true
	if s := b.seg; s != nil {
		if falseEdge && s.staged {
			s.stagedLabel = id
		} else {
			s.labels = append(s.labels, id)
		}
		s.block = s.emitted
	} else {
		b.control.steps = append(b.control.steps, builderControlStep{isLabel: true, label: id})
	}
	b.control.active = true
	b.control.pendingFalse = -1
	b.control.start = b.codeLen()
	b.validationEpoch++
	b.blockSerial++
	b.control.leaveStart = b.leaveCount()
	return err
}

// bindParamsBits binds a private label whose single incoming edge supplies
// typed integer values. Only IndexedAddress uses it: a hardened bounds check
// clamps the index it passes on that edge.
func (b *Builder) bindParamsBits(id ir.Label, widths []uint8) ([]Value, error) {
	if b == nil || b.control == nil || b.control.active {
		return nil, fmt.Errorf("IndexedAddress: current block must be terminated before binding its continuation: %w", ErrInvalidFunction)
	}
	if err := b.bind(id, false); err != nil {
		return nil, err
	}
	if b.seg == nil {
		b.control.params[id] = len(widths)
		b.control.paramBits[id] = widths
		b.control.paramTypes[id] = make([]ir.Type, len(widths))
		for i, width := range widths {
			b.control.paramTypes[id][i] = ir.Scalar(width)
		}
	}
	values := make([]Value, len(widths))
	for i, width := range widths {
		values[i] = b.append(instruction{Op: opLabelParam, Imm: uint64(i), EntryReg: -1, Bits: width})
	}
	return values, nil
}

// controlEnd describes the terminator that ends the open block.
type controlEnd struct {
	term        builderTerm
	result      int
	resultParts []int
	predicate   *predicateNode
	values      []int
	args        []int
	otherArgs   []int
	to, other   ir.Label
	// stage leaves a one-target Branch's block staged in segment mode; see
	// bind.
	stage bool
}

// splitPoint is a position where the open block ends at a scope's entry.
type splitPoint struct{ position, scope int }

// scopeEntrySplits returns where the open block, which a jump or branch
// ends, must end at a scope entry. Every active nested scope whose Enter
// lies in the block, with code after it, has its Enter's edge materialized:
// the block ends with a jump just after the OpScopeEnter, and the scope's
// code starts a block of its own. Values cross the split by dominance, so
// nothing is threaded. A scope whose Enter is the block's last instruction
// needs no split: the block's own edge is the entry edge. Straight-line
// scopes, which no jump or branch ends, keep their enclosing values in one
// block.
func (b *Builder) scopeEntrySplits(dst []splitPoint) []splitPoint {
	for _, scope := range b.scopes[1:] {
		enter := b.scopeStarts[scope]
		if enter >= b.control.start && enter+1 < b.codeLen() {
			dst = append(dst, splitPoint{enter + 1, scope})
		}
	}
	if len(dst) != 0 && b.paramEnd > dst[0].position {
		if b.splitErr == nil {
			b.splitErr = fmt.Errorf("parameters must precede Enter when its scope contains a branch: %w", ir.ErrInvalidProgram)
		}
		return dst[:0]
	}
	return dst
}

// endControl ends the open block with e and returns the false edge's label.
// In segment mode it hands the block to emit, and returns emit's error.
func (b *Builder) endControl(e controlEnd) (ir.Label, error) {
	if b.storageOwner != nil && b.storageOwner != b {
		b.detachCopy()
	}
	var err error
	s := b.seg
	if s != nil && s.staged {
		err = b.flushStaged()
	}
	c := b.control
	b.scopeTerminated[b.scopeID()] = true
	arity := 0
	if !c.hasTerm {
		arity = b.arity
	}
	start, leaveStart := c.start, c.leaveStart
	var splitBuffer [4]splitPoint
	for _, split := range b.scopeEntrySplits(splitBuffer[:0]) {
		label := b.addLabel(split.scope, true)
		leaveEnd := leaveStart
		if s != nil {
			n := 0
			for n < len(s.leaves) && s.leaves[n].Position <= split.position {
				n++
			}
			if emitErr := b.emitSegment(split.position, s.enterVars[split.scope], n,
				ir.ControlStep{Term: ir.TermJump, To: int(label), Other: -1, Result: -1, Arity: arity}, false); err == nil {
				err = emitErr
			}
			s.labels = append(s.labels, label)
			leaveEnd = s.leaveBase
		} else {
			for leaveEnd < len(b.leaves) && b.leaves[leaveEnd].Position <= split.position {
				leaveEnd++
			}
			c.steps = append(c.steps,
				builderControlStep{term: builderJump, start: start, end: split.position, leaveStart: leaveStart, leaveEnd: leaveEnd, result: -1, arity: arity, to: label, other: -1},
				builderControlStep{isLabel: true, label: label})
		}
		start, leaveStart, arity = split.position, leaveEnd, 0
	}
	if e.other == fallthroughLabel {
		e.other = b.newLabel()
	}
	if s != nil {
		step := ir.ControlStep{Term: ir.ControlTerm(e.term), To: int(e.to), Other: int(e.other), Result: e.result, Arity: arity}
		step.ResultParts = s.copyInts(e.resultParts)
		step.Values = s.copyInts(e.values)
		step.Args = s.copyInts(e.args)
		step.OtherArgs = s.copyInts(e.otherArgs)
		step.Predicate = s.freezePredicate(e.predicate)
		if e.stage {
			s.stage(b, step)
		} else if emitErr := b.emitSegment(b.codeLen(), len(b.variables), len(s.leaves), step, true); err == nil {
			err = emitErr
		}
	} else {
		c.steps = append(c.steps, builderControlStep{
			scope: b.scopeID(),
			term:  e.term, start: start, end: b.codeLen(),
			leaveStart: leaveStart, leaveEnd: len(b.leaves),
			result: e.result, resultParts: e.resultParts, arity: arity, predicate: e.predicate,
			values: slices.Clone(e.values), args: e.args, otherArgs: e.otherArgs, to: e.to, other: e.other,
		})
	}
	c.active, c.hasTerm, c.pendingFalse = false, true, -1
	return e.other, err
}

// Jump ends the current value sequence and transfers control to label.
// Directly after a Branch, Jump gives that Branch its false edge, so
// Branch(p, a) followed by Jump(c) is one two-way branch.
func (b *Builder) Jump(label Label) error {
	if !b.canAppend() || b.control == nil {
		return fmt.Errorf("Jump: current block is closed: %w", ErrInvalidFunction)
	}
	id, ok := b.localLabel(label)
	if !ok || b.control.privateLabels[id] {
		return b.labelError("Jump", label)
	}
	if merged, err := b.mergeFallthrough(id); merged {
		return err
	}
	_, err := b.endControl(controlEnd{term: builderJump, result: -1, to: id, other: -1})
	return err
}

// mergeFallthrough retargets the false edge of the Branch that ended the
// previous block when the open block is that edge's empty fallthrough block.
// It removes the fallthrough label, which no other edge can name.
func (b *Builder) mergeFallthrough(to ir.Label) (bool, error) {
	c := b.control
	ft := c.pendingFalse
	if ft < 0 || int(ft) != len(c.labels)-1 || b.codeLen() != c.start || b.leaveCount() != c.leaveStart {
		return false, nil
	}
	if s := b.seg; s != nil {
		if !s.staged || s.stagedLabel != ft || s.step.Other != int(ft) {
			return false, nil
		}
		s.step.Other = int(to)
		s.stagedLabel = -1
		b.dropLastLabel()
		c.active, c.pendingFalse = false, -1
		return true, b.flushStaged()
	}
	n := len(c.steps)
	if n < 2 {
		return false, nil
	}
	branch, bound := &c.steps[n-2], c.steps[n-1]
	if !bound.isLabel || bound.label != ft || branch.isLabel || branch.term != builderBranch || branch.other != ft {
		return false, nil
	}
	branch.other = to
	c.steps = c.steps[:n-1]
	b.dropLastLabel()
	c.active, c.pendingFalse = false, -1
	return true, nil
}

// Branch continues at target when predicate holds. Otherwise execution
// continues after the call, in a new block. For a two-way branch, follow it
// with Jump. An empty Predicate always holds: Branch then ends the block as
// Jump does. Predicates have no depth limit. Expanded trees (counting shared
// subtrees at every use) above ir.MaxPredicateNodes return an error wrapping
// ErrBackendLimit.
func (b *Builder) Branch(predicate Predicate, target Label) error {
	if predicate.node == nil && predicate.builder == nil {
		return b.Jump(target)
	}
	if !b.canAppend() || b.control == nil {
		return fmt.Errorf("Branch: current block is closed: %w", ErrInvalidFunction)
	}
	to, ok := b.localLabel(target)
	if !ok || b.control.privateLabels[to] {
		return b.labelError("Branch", target)
	}
	fallthroughTo, err := b.branch(predicate, to, nil, fallthroughLabel, nil, false)
	if fallthroughTo < 0 {
		return err
	}
	if bindErr := b.bind(fallthroughTo, true); err == nil {
		err = bindErr
	}
	b.control.pendingFalse = fallthroughTo
	return err
}

// branch ends the block with a two-way edge and returns the false edge's
// label, or -1 when the block did not end. Only IndexedAddress passes edge
// values, to its private continuation. ifFalse may be fallthroughLabel.
func (b *Builder) branch(predicate Predicate, ifTrue ir.Label, trueArgs []Value, ifFalse ir.Label, falseArgs []Value, checkedIndex bool) (ir.Label, error) {
	if !b.canAppend() || b.control == nil || !b.localIndex(ifTrue) || ifFalse != fallthroughLabel && !b.localIndex(ifFalse) {
		return -1, fmt.Errorf("Branch labels %d and %d: target is unknown, out of scope, or current block is closed: %w", ifTrue, ifFalse, ErrInvalidFunction)
	}
	var err error
	trueArgs, err = b.materializeValues(trueArgs)
	if err != nil {
		return -1, err
	}
	falseArgs, err = b.materializeValues(falseArgs)
	if err != nil {
		return -1, err
	}
	copyIDs := func(args []Value) ([]int, error) {
		if len(args) == 0 {
			return nil, nil
		}
		ids := make([]int, 0, len(args))
		for _, value := range args {
			if !checkedIndex && b.checkedAddress(value) {
				return nil, fmt.Errorf("Branch: indexed addresses cannot cross blocks: %w", ErrInvalidFunction)
			}
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
		return -1, err
	}
	falseIDs, err := copyIDs(falseArgs)
	if err != nil {
		return -1, err
	}
	values, nodes, err := b.predicateValues(predicate)
	if err != nil {
		return -1, err
	}
	if len(values) == 0 {
		if ifFalse == fallthroughLabel {
			return -1, fmt.Errorf("Branch label %d: predicate is invalid in this block: %w", ifTrue, ErrInvalidFunction)
		}
		return -1, fmt.Errorf("Branch labels %d and %d: predicate is invalid in this block: %w", ifTrue, ifFalse, ErrInvalidFunction)
	}
	if s := b.seg; s != nil {
		s.predicateNodes += nodes
		for _, ids := range [][]int{values, trueIDs, falseIDs} {
			for _, id := range ids {
				s.use(id, -1)
			}
		}
	}
	return b.endControl(controlEnd{term: builderBranch, result: values[0], predicate: predicate.node, values: values,
		args: trueIDs, otherArgs: falseIDs, to: ifTrue, other: ifFalse, stage: ifFalse == fallthroughLabel})
}
