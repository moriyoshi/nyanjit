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

func validateVariables(p *Program, regions []int) error {
	var inputBuffer [4]int
	scopes := make(map[int]ScopeExit, len(p.ScopeExits)+1)
	scopes[0] = ScopeExit{ID: 0, Enter: 0, Position: len(p.Instructions)}
	for _, s := range p.ScopeExits {
		scopes[s.ID] = s
	}
	captures := make(map[int][]int)
	for _, v := range p.Variables {
		if v.Immutable {
			captures[v.Region] = append(captures[v.Region], v.Initial...)
		}
	}
	firstTerm := make(map[int]int)
	if p.Control != nil {
		for _, step := range p.Control.Steps {
			if step.IsLabel || step.End < 0 || step.End >= len(regions) {
				continue
			}
			id := regions[step.End]
			region := scopes[id]
			if step.End <= region.Enter {
				continue
			}
			if id != 0 && step.Term == TermJump && step.End == region.Enter+1 && region.Enter >= 0 && region.Enter < len(p.Instructions) && p.Instructions[region.Enter].Op == OpScopeEnter && int(p.Instructions[region.Enter].Imm) == id {
				continue
			}
			if old, ok := firstTerm[id]; !ok || step.End < old {
				firstTerm[id] = step.End
			}
		}
	}
	inside := func(region, pos int) bool { s, ok := scopes[region]; return ok && pos >= s.Enter && pos < s.Position }
	for id, v := range p.Variables {
		if !v.Type.Valid() || v.Declaration < 0 || v.Declaration >= len(p.Instructions) || !inside(v.Region, v.Declaration) || len(v.Initial) == 0 {
			return invalidRule("Var %d has invalid declaration or region", id)
		}
		region := scopes[v.Region]
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
			if end, ok := firstTerm[v.Region]; ok && end <= v.Declaration {
				return invalidAt(p.Instructions, v.Declaration, "Var %d must be declared in its scope entry sequence", id)
			}
		}
	}
	stack := make([]int, 1, len(p.ScopeExits)+1)
	active := make(map[int]int, len(p.ScopeExits)+1)
	active[0] = 1
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
			s, ok := scopes[int(i.Imm)]
			if !ok || s.Parent != stack[len(stack)-1] || s.Enter != pos {
				return invalidAt(p.Instructions, pos, "scope %d entry does not match its ScopeExit or the enclosing scope", i.Imm)
			}
			stack = append(stack, s.ID)
			active[s.ID]++
		case OpScopeLeave:
			if len(stack) <= 1 || stack[len(stack)-1] != int(i.Imm) || scopes[int(i.Imm)].Position != pos+1 {
				return invalidAt(p.Instructions, pos, "leave of scope %d does not close the innermost scope at its exit position", i.Imm)
			}
			active[stack[len(stack)-1]]--
			stack = stack[:len(stack)-1]
		case OpGet, OpSet:
			if i.Variable < 0 || i.Variable >= len(p.Variables) {
				return invalidAt(p.Instructions, pos, "Var %d out of range", i.Variable)
			}
			v := p.Variables[i.Variable]
			if active[v.Region] == 0 {
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
