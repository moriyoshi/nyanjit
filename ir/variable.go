package ir

import "fmt"

// Variable is initialized storage whose lexical region is independent of blocks.
// Immutable captures are owned by Scope and usable within the entered Region.
type Variable struct {
	Type          Type
	Scope, Region int
	Declaration   int
	Initial       []int
	Immutable     bool
}

func validateVariables(p *Program) error {
	scopes := map[int]ScopeExit{0: {ID: 0, Enter: 0, Position: len(p.Instructions)}}
	for _, s := range p.ScopeExits {
		scopes[s.ID] = s
	}
	inside := func(region, pos int) bool { s, ok := scopes[region]; return ok && pos >= s.Enter && pos < s.Position }
	for id, v := range p.Variables {
		if !v.Type.Valid() || !inside(v.Region, v.Declaration) || len(v.Initial) == 0 {
			return fmt.Errorf("Var %d has invalid declaration or region: %w", id, ErrInvalidProgram)
		}
		region := scopes[v.Region]
		if v.Immutable {
			if v.Scope != region.Parent || v.Region == 0 || v.Declaration != region.Enter {
				return ErrInvalidProgram
			}
		} else if v.Scope != v.Region {
			return ErrInvalidProgram
		}
		expected := []Type{v.Type}
		if v.Type == Scalar(128) {
			expected = []Type{Scalar(64), Scalar(64)}
		}
		if len(expected) != len(v.Initial) {
			return ErrInvalidProgram
		}
		for n, x := range v.Initial {
			if x < 0 || x >= v.Declaration || p.Instructions[x].ValueType() != expected[n] || !variableValue(p.Instructions[x]) {
				return ErrInvalidProgram
			}
		}
		if !v.Immutable {
			for n, x := range v.Initial {
				at := v.Declaration + n
				if at >= len(p.Instructions) {
					return ErrInvalidProgram
				}
				i := p.Instructions[at]
				if i.Op != OpSet || i.Variable != id || i.A != x || i.Imm != uint64(n) {
					return ErrInvalidProgram
				}
			}
			if p.Control != nil {
				for _, step := range p.Control.Steps {
					if !step.IsLabel && step.End > region.Enter && step.End <= v.Declaration && variableRegionAt(p, step.End) == v.Region {
						return fmt.Errorf("Var %d must be declared in its scope entry sequence: %w", id, ErrInvalidProgram)
					}
				}
			}
		}
	}
	for pos, i := range p.Instructions {
		for _, x := range i.Inputs() {
			if x < 0 || x >= pos || !variableValue(p.Instructions[x]) {
				return fmt.Errorf("instruction %d uses non-value %d: %w", pos, x, ErrInvalidProgram)
			}
		}
	}
	stack := []int{0}
	for pos, i := range p.Instructions {
		switch i.Op {
		case OpScopeEnter:
			expected := []int{}
			for _, v := range p.Variables {
				if v.Immutable && v.Region == int(i.Imm) {
					expected = append(expected, v.Initial...)
				}
			}
			if len(i.Args) != len(expected) {
				return ErrInvalidProgram
			}
			for n, x := range expected {
				if i.Args[n] != x {
					return ErrInvalidProgram
				}
			}
			s, ok := scopes[int(i.Imm)]
			if !ok || s.Parent != stack[len(stack)-1] || s.Enter != pos {
				return ErrInvalidProgram
			}
			stack = append(stack, s.ID)
		case OpScopeLeave:
			if len(stack) <= 1 || stack[len(stack)-1] != int(i.Imm) || scopes[int(i.Imm)].Position != pos+1 {
				return ErrInvalidProgram
			}
			stack = stack[:len(stack)-1]
		case OpGet, OpSet:
			if i.Variable < 0 || i.Variable >= len(p.Variables) {
				return ErrInvalidProgram
			}
			v := p.Variables[i.Variable]
			active := false
			for _, scope := range stack {
				active = active || scope == v.Region
			}
			if !active {
				return ErrInvalidProgram
			}
			if !inside(v.Region, pos) || pos < v.Declaration {
				return fmt.Errorf("Var %d at instruction %d is out of scope: %w", i.Variable, pos, ErrInvalidProgram)
			}
			typ := v.Type
			if typ == Scalar(128) {
				typ = Scalar(64)
				if i.Imm > 1 {
					return ErrInvalidProgram
				}
			} else if i.Imm != 0 {
				return ErrInvalidProgram
			}
			if i.ValueType() != typ || i.Bits != typ.LaneBits || i.ByteOrder != ByteOrderNative || i.Contract || i.Effect != nil || i.Symbol != "" {
				return ErrInvalidProgram
			}
			if i.Op == OpSet {
				if pos == v.Declaration+int(i.Imm) && stack[len(stack)-1] != v.Scope {
					return ErrInvalidProgram
				}
				if v.Immutable || i.A < 0 || i.A >= pos || p.Instructions[i.A].ValueType() != typ {
					return ErrInvalidProgram
				}
			}
		}
	}
	if len(stack) != 1 {
		return ErrInvalidProgram
	}
	return nil
}

func variableRegionAt(p *Program, pos int) int {
	region, start := 0, -1
	for _, s := range p.ScopeExits {
		if pos > s.Enter && pos < s.Position && s.Enter >= start {
			region, start = s.ID, s.Enter
		}
	}
	return region
}

func variableValue(i Instruction) bool {
	switch i.Op {
	case OpSet, OpScopeEnter, OpScopeLeave, OpStore64, OpStore8, OpVStore:
		return false
	}
	return true
}
