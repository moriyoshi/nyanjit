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
	for _, scope := range b.scopes {
		if scope == region {
			return true
		}
	}
	return false
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
	if !value.Type().Valid() {
		return Var{}, ErrInvalidFunction
	}
	parts := value.components()
	ids := make([]int, len(parts))
	for i, p := range parts {
		ids[i] = p.id
	}
	id := len(b.variables)
	b.variables = append(b.variables, ir.Variable{Type: value.Type(), Scope: scope, Region: scope, Declaration: len(b.code), Initial: ids})
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
	if t == Scalar(128) {
		a := b.append(instruction{Op: ir.OpGet, Type: Scalar(64), Bits: 64, Variable: v.id})
		c := b.append(instruction{Op: ir.OpGet, Type: Scalar(64), Bits: 64, Variable: v.id, Imm: 1})
		return compositeValue(a, c), nil
	}
	return b.append(instruction{Op: ir.OpGet, Type: t, Bits: t.LaneBits, Variable: v.id}), nil
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
	if value.Type() != b.variables[v.id].Type {
		return fmt.Errorf("Set Var %d: type %v does not match %v: %w", v.id, value.Type(), b.variables[v.id].Type, ErrInvalidFunction)
	}
	for i, p := range value.components() {
		b.append(instruction{Op: ir.OpSet, Type: p.Type(), Bits: p.Type().LaneBits, Variable: v.id, A: p.id, Imm: uint64(i)})
	}
	return nil
}

func (b *Builder) captured(id int) bool {
	for i := len(b.scopes) - 1; i >= 0; i-- {
		if _, ok := b.captures[b.scopes[i]][id]; ok {
			return true
		}
	}
	return false
}
func (b *Builder) capture(id int) (Value, error) {
	for i := len(b.scopes) - 1; i >= 0; i-- {
		scope := b.scopes[i]
		bindings := b.captures[scope]
		variable, ok := bindings[id]
		if !ok {
			continue
		}
		if variable < 0 {
			variable = len(b.variables)
			bindings[id] = variable
			b.variables = append(b.variables, ir.Variable{Type: b.code[id].ValueType(), Scope: b.scopes[i-1], Region: scope, Declaration: b.scopeStarts[scope], Initial: []int{id}, Immutable: true})
		}
		return b.Get(Var{builder: b, id: variable})
	}
	return Value{}, b.invalidValue(Value{builder: b, id: id})
}
func captureValue(ins instruction) bool {
	switch ins.Op {
	case ir.OpSet, ir.OpScopeEnter, ir.OpScopeLeave, ir.OpStore64, ir.OpStore8, ir.OpVStore:
		return false
	}
	return true
}
