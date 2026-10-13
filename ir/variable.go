package ir

// Variable is initialized storage whose lexical region is independent of blocks.
// Immutable captures are owned by Scope and usable within the entered Region.
type Variable struct {
	Type          Type
	Scope, Region int
	Declaration   int
	Initial       []int
	Immutable     bool
}

// variableScope is what validateVariables knows of one scope ID.
type variableScope struct {
	exit      ScopeExit
	present   bool
	firstTerm int // the first terminator inside the scope, or -1
	active    int // entries of the scope that have not left yet
}

// variableScopes maps scope IDs to variableScope. Builder IDs are dense,
// 0 … len(ScopeExits), and index a slice; other IDs use a map.
type variableScopes struct {
	dense  []variableScope
	sparse map[int]*variableScope
}

func (t *variableScopes) init(p *Program) {
	dense := true
	for i := range p.ScopeExits {
		if id := p.ScopeExits[i].ID; id < 0 || id > len(p.ScopeExits) {
			dense = false
		}
	}
	if dense {
		t.dense = make([]variableScope, len(p.ScopeExits)+1)
	} else {
		t.sparse = make(map[int]*variableScope, len(p.ScopeExits)+1)
	}
	t.put(ScopeExit{ID: 0, Enter: 0, Position: len(p.Instructions)})
	for _, s := range p.ScopeExits {
		t.put(s)
	}
}

// put records s as its ID's scope; a later duplicate replaces it.
func (t *variableScopes) put(s ScopeExit) {
	if t.sparse != nil {
		t.sparse[s.ID] = &variableScope{exit: s, present: true, firstTerm: -1}
		return
	}
	t.dense[s.ID] = variableScope{exit: s, present: true, firstTerm: -1}
}

// at returns id's scope, or nil for an unknown ID.
func (t *variableScopes) at(id int) *variableScope {
	if t.sparse != nil {
		return t.sparse[id]
	}
	if id < 0 || id >= len(t.dense) || !t.dense[id].present {
		return nil
	}
	return &t.dense[id]
}

// exit returns id's ScopeExit, or the zero value for an unknown ID.
func (t *variableScopes) exit(id int) ScopeExit {
	if s := t.at(id); s != nil {
		return s.exit
	}
	return ScopeExit{}
}

func validateVariables(p *Program, regions []int) error {
	var inputBuffer [4]int
	var scopes variableScopes
	scopes.init(p)
	captures := make(map[int][]int)
	for v := range p.Variables {
		if p.Variables[v].Immutable {
			captures[p.Variables[v].Region] = append(captures[p.Variables[v].Region], p.Variables[v].Initial...)
		}
	}
	if p.Control != nil {
		for s := range p.Control.Steps {
			step := &p.Control.Steps[s]
			if step.IsLabel || step.End < 0 || step.End >= len(regions) {
				continue
			}
			id := regions[step.End]
			region := scopes.exit(id)
			if step.End <= region.Enter {
				continue
			}
			if id != 0 && step.Term == TermJump && step.End == region.Enter+1 && region.Enter >= 0 && region.Enter < len(p.Instructions) && p.Instructions[region.Enter].Op == OpScopeEnter && int(p.Instructions[region.Enter].Imm) == id {
				continue
			}
			// Regions name recorded scopes, so the entry exists.
			if scope := scopes.at(id); scope != nil && (scope.firstTerm < 0 || step.End < scope.firstTerm) {
				scope.firstTerm = step.End
			}
		}
	}
	inside := func(region, pos int) bool {
		s := scopes.at(region)
		return s != nil && pos >= s.exit.Enter && pos < s.exit.Position
	}
	for id := range p.Variables {
		v := &p.Variables[id]
		if !v.Type.Valid() || v.Declaration < 0 || v.Declaration >= len(p.Instructions) || !inside(v.Region, v.Declaration) || len(v.Initial) == 0 {
			return invalidRule("Var %d has invalid declaration or region", id)
		}
		region := scopes.exit(v.Region)
		if v.Immutable {
			if v.Scope != region.Parent || v.Region == 0 || v.Declaration != region.Enter {
				return invalidRule("Var %d: an immutable capture must belong to its region's parent scope and be declared at the scope entry", id)
			}
		} else if v.Scope != v.Region {
			return invalidRule("Var %d: Scope %d differs from Region %d", id, v.Scope, v.Region)
		}
		expected := []Type{v.Type}
		if v.Type == (Type{Kind: ScalarKind, Lanes: 1, LaneBits: 128}) {
			expected = []Type{{Kind: ScalarKind, Lanes: 1, LaneBits: 64}, {Kind: ScalarKind, Lanes: 1, LaneBits: 64}}
		}
		if len(expected) != len(v.Initial) {
			return invalidRule("Var %d: %d initial values for type %+v", id, len(v.Initial), v.Type)
		}
		for n, x := range v.Initial {
			if x < 0 || x >= v.Declaration || x >= len(p.Instructions) || !instructionHasType(&p.Instructions[x], expected[n]) || !variableValue(p.Instructions[x]) {
				return invalidRule("Var %d: initial value %d is not a preceding value of the Var's type", id, x)
			}
		}
		if !v.Immutable {
			for n, x := range v.Initial {
				at := v.Declaration + n
				if at >= len(p.Instructions) {
					return invalidRule("Var %d: declaration runs past the program", id)
				}
				i := p.Instructions[at]
				if i.Op != OpSet || i.Variable != id || i.A != x || i.Imm != uint64(n) {
					return invalidAt(p.Instructions, at, "declaration of Var %d must Set part %d to initial value %d", id, n, x)
				}
			}
			if scope := scopes.at(v.Region); scope != nil && scope.firstTerm >= 0 && scope.firstTerm <= v.Declaration {
				return invalidAt(p.Instructions, v.Declaration, "Var %d must be declared in its scope entry sequence", id)
			}
		}
	}
	stack := make([]int, 1, len(p.ScopeExits)+1)
	scopes.at(0).active = 1
	for pos := range p.Instructions {
		i := &p.Instructions[pos]
		for _, x := range instructionInputsInto(i.Op, i.A, i.B, i.C, i.Args, inputBuffer[:0]) {
			if x < 0 || x >= pos || !variableValue(p.Instructions[x]) {
				return invalidAt(p.Instructions, pos, "uses non-value %d", x)
			}
		}
		switch i.Op {
		case OpScopeEnter:
			expected := captures[int(i.Imm)]
			if len(i.Args) != len(expected) {
				return invalidAt(p.Instructions, pos, "scope entry captures %d values; its immutable Vars need %d", len(i.Args), len(expected))
			}
			for n, x := range expected {
				if i.Args[n] != x {
					return invalidAt(p.Instructions, pos, "scope entry capture %d is %d; its immutable Var needs %d", n, i.Args[n], x)
				}
			}
			entry := scopes.at(int(i.Imm))
			if entry == nil || entry.exit.Parent != stack[len(stack)-1] || entry.exit.Enter != pos {
				return invalidAt(p.Instructions, pos, "scope %d entry does not match its ScopeExit or the enclosing scope", i.Imm)
			}
			stack = append(stack, entry.exit.ID)
			entry.active++
		case OpScopeLeave:
			if len(stack) <= 1 || stack[len(stack)-1] != int(i.Imm) || scopes.exit(int(i.Imm)).Position != pos+1 {
				return invalidAt(p.Instructions, pos, "leave of scope %d does not close the innermost scope at its exit position", i.Imm)
			}
			scopes.at(stack[len(stack)-1]).active--
			stack = stack[:len(stack)-1]
		case OpGet, OpSet:
			if i.Variable < 0 || i.Variable >= len(p.Variables) {
				return invalidAt(p.Instructions, pos, "Var %d out of range", i.Variable)
			}
			v := &p.Variables[i.Variable]
			if scope := scopes.at(v.Region); scope == nil || scope.active == 0 {
				return invalidAt(p.Instructions, pos, "Var %d accessed while its scope is not active", i.Variable)
			}
			if !inside(v.Region, pos) || pos < v.Declaration {
				return invalidAt(p.Instructions, pos, "Var %d is out of scope", i.Variable)
			}
			typ := v.Type
			if typ == (Type{Kind: ScalarKind, Lanes: 1, LaneBits: 128}) {
				typ = Type{Kind: ScalarKind, Lanes: 1, LaneBits: 64}
				if i.Imm > 1 {
					return invalidAt(p.Instructions, pos, "part %d of 128-bit Var %d", i.Imm, i.Variable)
				}
			} else if i.Imm != 0 {
				return invalidAt(p.Instructions, pos, "part %d of single-part Var %d", i.Imm, i.Variable)
			}
			if !instructionHasType(i, typ) || i.Bits != typ.LaneBits || i.ByteOrder != ByteOrderNative || i.Contract || i.Effect != nil || i.Symbol != "" {
				return invalidAt(p.Instructions, pos, "access shape disagrees with Var %d's type, or carries unused fields", i.Variable)
			}
			if i.Op == OpSet {
				if pos == v.Declaration+int(i.Imm) && stack[len(stack)-1] != v.Scope {
					return invalidAt(p.Instructions, pos, "declaration of Var %d outside its scope", i.Variable)
				}
				if v.Immutable || i.A < 0 || i.A >= pos || !instructionHasType(&p.Instructions[i.A], typ) {
					return invalidAt(p.Instructions, pos, "Set of Var %d needs a mutable Var and a preceding value of its type", i.Variable)
				}
			}
		}
	}
	if len(stack) != 1 {
		return invalidRule("scope entered without a matching leave")
	}
	return nil
}

func variableRegions(p *Program) []int {
	return scopeRegions(len(p.Instructions), len(p.ScopeExits), func(i int) (int, int, int) { s := p.ScopeExits[i]; return s.ID, s.Enter + 1, s.Position })
}

func variableValue(i Instruction) bool {
	return i.Op.ProducesValue()
}

// instructionHasType compares against an already validated expected shape.
// With no explicit metadata, only the legacy scalar width can match it;
// the valid expected shape proves that width without another type validation.
func instructionHasType(i *Instruction, expected Type) bool {
	if i.Type != (Type{}) {
		return i.Type == expected
	}
	bits := i.Bits
	if bits == 0 {
		bits = 64
	}
	return expected.Kind == ScalarKind && expected.Lanes == 1 && expected.LaneBits == bits
}
