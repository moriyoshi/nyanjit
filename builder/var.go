package builder

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/ir"
)

// Var identifies mutable storage owned by one Builder scope.
type Var struct {
	builder *Builder
	id      int
}

func (b *Builder) scopeID() int { b.ensureRoot(); return b.scopes[len(b.scopes)-1] }
func (b *Builder) validVar(v Var) bool {
	if !b.canAppend() || v.builder != b || v.id < 0 || v.id >= len(b.variables) {
		return false
	}
	region := b.variables[v.id].Region
	return region >= 0 && region < len(b.scopeActive) && b.scopeActive[region]
}

// Var declares initialized mutable storage in the current scope's entry sequence.
func (b *Builder) Var(initial Value) (Var, error) {
	if !b.canAppend() {
		return Var{}, ErrInvalidFunction
	}
	scope := b.scopeID()
	if b.scopeTerminated[scope] {
		return Var{}, fmt.Errorf("Var: scope %d entry sequence is already terminated: %w", scope, ErrInvalidFunction)
	}
	value, err := b.materialize(initial)
	if err != nil {
		return Var{}, err
	}
	if b.checkedAddress(value) {
		return Var{}, fmt.Errorf("Var: indexed addresses cannot be stored: %w", ErrInvalidFunction)
	}
	if !value.Type().Valid() {
		return Var{}, ErrInvalidFunction
	}
	parts := value.components()
	ids := b.initialWords(len(parts))
	for i, p := range parts {
		ids[i] = p.id
	}
	id := len(b.variables)
	b.variables = append(b.variables, ir.Variable{Type: value.Type(), Scope: scope, Region: scope, Declaration: b.codeLen(), Initial: ids})
	v := Var{builder: b, id: id}
	if err := b.Set(v, value); err != nil {
		return Var{}, err
	}
	return v, nil
}

// Get returns a block-local snapshot of the variable's current value.
func (b *Builder) Get(v Var) (Value, error) {
	if !b.validVar(v) {
		return Value{}, fmt.Errorf("Get Var %d: foreign, invalid, or out of scope: %w", v.id, ErrInvalidFunction)
	}
	t := b.variables[v.id].Type
	if t == ir.Scalar(128) {
		a := b.append(instruction{Op: ir.OpGet, Type: ir.Scalar(64), Bits: 64, Variable: v.id})
		c := b.append(instruction{Op: ir.OpGet, Type: ir.Scalar(64), Bits: 64, Variable: v.id, Imm: 1})
		return compositeValue(a, c), nil
	}
	return b.appendPlain(ir.OpGet, t, t.LaneBits, v.id, 0, 0, 0, 0), nil
}

// Set replaces a variable's value without changing earlier Get snapshots.
func (b *Builder) Set(v Var, x Value) error {
	if !b.validVar(v) {
		return fmt.Errorf("Set Var %d: foreign, invalid, or out of scope: %w", v.id, ErrInvalidFunction)
	}
	if b.variables[v.id].Immutable {
		return fmt.Errorf("Set Var %d: capture is immutable: %w", v.id, ErrInvalidFunction)
	}
	value, err := b.materialize(x)
	if err != nil {
		return err
	}
	if b.checkedAddress(value) {
		return fmt.Errorf("Set: indexed addresses cannot be stored in Vars; take a fresh Index in the destination block: %w", ErrInvalidFunction)
	}
	if value.Type() != b.variables[v.id].Type {
		return fmt.Errorf("Set Var %d: type %v does not match %v: %w", v.id, value.Type(), b.variables[v.id].Type, ErrInvalidFunction)
	}
	for i, p := range value.components() {
		b.appendPlain(ir.OpSet, p.Type(), p.Type().LaneBits, v.id, p.id, 0, uint64(i), 1)
	}
	return nil
}

// captureScope finds the nearest entry that could see id. Both entry range
// endpoints increase along the stack, so lookup is logarithmic in depth.
func (b *Builder) captureScope(id int) (int, bool) {
	// Entry endpoints increase along the active stack. A value created after
	// the innermost entry cannot be captured at any enclosing entry either.
	if id < 0 || id >= b.codeLen() || len(b.scopes) < 2 || id >= b.captureEnds[b.scopes[len(b.scopes)-1]] || !captureOpcode(b.instructionOp(id)) {
		return 0, false
	}
	owner := int(b.values[id].scope)
	if owner < 0 || owner >= len(b.scopeActive) || !b.scopeActive[owner] {
		return 0, false
	}
	lo, hi := 1, len(b.scopes)
	for lo < hi {
		middle := int(uint(lo+hi) >> 1)
		if b.captureStarts[b.scopes[middle]] <= id {
			lo = middle + 1
		} else {
			hi = middle
		}
	}
	index := lo - 1
	return index, index > 0 && id < b.captureEnds[b.scopes[index]]
}

func (b *Builder) captured(id int) bool { _, ok := b.captureScope(id); return ok }
func (b *Builder) capture(id int) (Value, error) {
	if b.storageOwner != nil && b.storageOwner != b {
		b.detachCopy()
	}
	if b.seg != nil && b.seg.staged {
		// The capture belongs to the open block's segment.
		b.flushStaged()
	}
	index, ok := b.captureScope(id)
	if !ok {
		return Value{}, b.invalidValue(Value{builder: b, id: id})
	}
	scope := b.scopes[index]
	if b.captures == nil {
		b.captures = make(map[int]map[int]int)
	}
	bindings := b.captures[scope]
	if bindings == nil {
		bindings = make(map[int]int)
		b.captures[scope] = bindings
	}
	variable, exists := bindings[id]
	if !exists {
		variable = len(b.variables)
		bindings[id] = variable
		b.variables = append(b.variables, ir.Variable{Type: b.instructionType(id), Scope: b.scopes[index-1], Region: scope, Declaration: b.scopeStarts[scope], Initial: b.captureInitial(id), Immutable: true})
	}
	return b.Get(Var{builder: b, id: variable})
}
func captureOpcode(opcode op) bool {
	return opcode.ProducesValue()
}

// Initial slices become immutable IR data, with capacity restricted so they
// cannot overwrite a neighboring variable. Finish transfers the slab storage.
func (b *Builder) initialWords(count int) []int {
	if cap(b.variableInitials)-len(b.variableInitials) < count {
		b.variableInitials = make([]int, 0, max(count, min(256, max(8, 2*cap(b.variableInitials)))))
	}
	start := len(b.variableInitials)
	b.variableInitials = b.variableInitials[:start+count]
	return b.variableInitials[start : start+count : start+count]
}
func (b *Builder) captureInitial(id int) []int {
	words := b.initialWords(1)
	words[0] = id
	return words
}
