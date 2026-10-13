package ir

import "sort"

// analyzeProgram computes fresh analyses of validated IR for trusted callers.
func analyzeProgram(p *Program, requireUses bool) *facts {
	if p == nil {
		return &facts{}
	}
	f := sourceMetadata(p)
	// Home analyses have no consumers when the program has no variables.
	if len(p.Variables) == 0 && !requireUses {
		return f
	}
	countScopes := len(p.ScopeExits) + 1
	f.Parents = make(map[int]int, countScopes)
	f.Parents[0] = -1
	f.Bounds = make(map[int][2]int, countScopes)
	f.Bounds[0] = [2]int{0, len(p.Instructions)}
	f.Local = make(map[int][]int, min(countScopes, len(p.Variables)))
	f.Regions = scopeRegions(len(p.Instructions), len(p.ScopeExits), func(i int) (int, int, int) { s := p.ScopeExits[i]; return s.ID, s.Enter + 1, s.Position })
	f.Scopes = make([]int, countScopes)
	for i, s := range p.ScopeExits {
		f.Parents[s.ID] = s.Parent
		f.Bounds[s.ID] = [2]int{s.Enter + 1, s.Position}
		f.Scopes[i+1] = s.ID
	}
	sort.Ints(f.Scopes)
	f.Last = make([]int, len(p.Instructions))
	if countScopes == 1 {
		if len(p.Variables) > 0 {
			ids := make([]int, len(p.Variables))
			for id := range ids {
				ids[id] = id
			}
			f.Local[0] = ids
		}
	} else {
		localCounts := f.Last[:len(f.Scopes)]
		for _, v := range p.Variables {
			localCounts[f.scopeIndex(v.Region)]++
		}
		localData := make([]int, len(p.Variables))
		localOffset := 0
		for i, scope := range f.Scopes {
			count := localCounts[i]
			if count > 0 {
				f.Local[scope] = localData[localOffset : localOffset : localOffset+count]
				localOffset += count
			}
		}
		for id, v := range p.Variables {
			f.Local[v.Region] = append(f.Local[v.Region], id)
		}
	}
	clear(f.Last)
	f.ParamIndex = make([]int, len(p.Instructions))
	// Precount variable references into the not-yet-initialized owned analyses.
	// Fill each adjacency list from a single slab rather than growing per Var.
	useCounts, setCounts := f.Last[:len(p.Variables)], f.ParamIndex[:len(p.Variables)]
	for id := range p.Instructions {
		i := &p.Instructions[id]
		if i.Op == OpGet || i.Op == OpSet {
			useCounts[i.Variable]++
			if i.Op == OpSet && id >= p.Variables[i.Variable].Declaration+len(p.Variables[i.Variable].Initial) {
				setCounts[i.Variable]++
			}
		}
	}
	f.VariableUses = adjacencyStorage(useCounts)
	f.VariableSets = adjacencyStorage(setCounts)
	var operands [4]int
	eachUse := func(visit func(int, int)) {
		// Keep this walk small enough to inline both it and its visitors.
		for pos, i := range p.Instructions {
			for _, id := range i.InputsInto(operands[:0]) {
				visit(id, pos)
			}
		}
		if p.Control != nil {
			for _, step := range p.Control.Steps {
				if !step.IsLabel {
					for _, list := range [][]int{step.Values, step.Args, step.OtherArgs} {
						for _, id := range list {
							visit(id, step.End)
						}
					}
				}
			}
		}
	}
	for id := range p.Instructions {
		i := &p.Instructions[id]
		f.Last[id] = id
		if i.Op == OpGet || i.Op == OpSet {
			f.VariableUses[i.Variable] = append(f.VariableUses[i.Variable], id)
			if i.Op == OpSet && id >= p.Variables[i.Variable].Declaration+len(p.Variables[i.Variable].Initial) {
				f.VariableSets[i.Variable] = append(f.VariableSets[i.Variable], id)
			}
		}
	}
	// The set-count scratch is dead after filling variable lists. Reuse its
	// array for value-use counts before installing final parameter indices.
	counts := f.ParamIndex
	clear(counts)
	eachUse(func(id, pos int) { counts[id]++; f.Last[id] = max(f.Last[id], pos) })
	f.Uses = adjacencyStorage(counts)
	parameters := 0
	for id := range p.Instructions {
		f.ParamIndex[id] = -1
		if p.Instructions[id].Op == OpParam {
			f.ParamIndex[id] = parameters
			parameters++
		}
	}

	eachUse(func(id, pos int) { f.Uses[id] = append(f.Uses[id], pos) })
	f.Floating = sourceFloatingHomes(p)
	return f
}

func sourceMetadata(p *Program) *facts {
	f := &facts{Loop: sourceHasLoop(p), LabelFloating: sourceFloatingLabelParams(p)}
	for index := range p.Instructions {
		i := &p.Instructions[index]
		if i.Op != OpParam {
			continue
		}
		bits := i.Bits
		if bits == 0 {
			bits = 64
		}
		if i.Type.Kind == VectorKind {
			bits = 0
		}
		f.EntryParamBits = append(f.EntryParamBits, bits)
	}
	if len(f.EntryParamBits) != p.Arity {
		f.EntryParamBits = nil
	}
	return f
}

func sourceFloatingHomes(p *Program) []bool {
	var inputBuffer [4]int
	homes := make([]bool, len(p.Variables))
	if !hasFloatingArithmetic(p) {
		return homes
	}
	values := make([]bool, len(p.Instructions))
	gets := make([][]int, len(p.Variables))
	targets := make([][]int, len(p.Instructions))
	queue := make([]int, 0, len(p.Instructions))
	markValue := func(id int) {
		if !values[id] {
			values[id] = true
			queue = append(queue, id)
		}
	}
	markHome := func(id int) {
		v := p.Variables[id]
		if !homes[id] && v.Type.Kind == ScalarKind && (v.Type.LaneBits == 32 || v.Type.LaneBits == 64) {
			homes[id] = true
			queue = append(queue, len(values)+id)
		}
	}
	for id := range p.Instructions {
		i := &p.Instructions[id]
		switch i.Op {
		case OpFAdd, OpFSub, OpFMul, OpFDiv, OpFMA, OpFSqrt, OpFConvert, OpFProductError, OpFFromSigned, OpFFromUnsigned:
			markValue(id)
		case OpGet:
			gets[i.Variable] = append(gets[i.Variable], id)
		case OpSet:
			targets[i.A] = append(targets[i.A], i.Variable)
		}
	}
	for id, v := range p.Variables {
		if v.Immutable {
			targets[v.Initial[0]] = append(targets[v.Initial[0]], id)
		}
	}
	for cursor := 0; cursor < len(queue); cursor++ {
		id := queue[cursor]
		if id >= len(values) {
			for _, get := range gets[id-len(values)] {
				markValue(get)
			}
			continue
		}
		for _, x := range p.Instructions[id].InputsInto(inputBuffer[:0]) {
			if p.Instructions[x].Op == OpGet {
				markHome(p.Instructions[x].Variable)
			}
		}
		for _, variable := range targets[id] {
			markHome(variable)
		}
	}
	for index := range p.Instructions {
		i := &p.Instructions[index]
		switch i.Op {
		case OpAdd, OpSub, OpMul, OpAnd, OpOr, OpXor, OpShiftLeft, OpShiftRight, OpShiftRightArithmetic, OpShiftLeftBy, OpShiftRightBy, OpShiftRightArithmeticBy, OpRotateLeft, OpRotateRight, OpRotateLeftBy, OpRotateRightBy:
			for _, x := range i.InputsInto(inputBuffer[:0]) {
				if p.Instructions[x].Op == OpGet {
					homes[p.Instructions[x].Variable] = false
				}
			}
		}
	}
	return homes
}

func sourceFloatingLabelParams(p *Program) [][]bool {
	var inputBuffer [4]int
	if p.Control == nil {
		return nil
	}
	if !hasFloatingArithmetic(p) {
		labels := make([][]bool, len(p.Control.Params))
		total := 0
		for _, count := range p.Control.Params {
			total += count
		}
		data := make([]bool, total)
		offset := 0
		for label, count := range p.Control.Params {
			labels[label] = data[offset : offset+count : offset+count]
			offset += count
		}
		return labels
	}
	values := make([]bool, len(p.Instructions))
	ids := make([][]int, len(p.Control.Params))
	labels := make([][]bool, len(ids))
	for label, count := range p.Control.Params {
		ids[label] = make([]int, count)
		labels[label] = make([]bool, count)
		for n := range ids[label] {
			ids[label][n] = -1
		}
	}
	currentLabel := -1
	for _, step := range p.Control.Steps {
		if step.IsLabel {
			currentLabel = step.Label
			continue
		}
		for pos := step.Start; pos < step.End; pos++ {
			i := p.Instructions[pos]
			if i.Op == OpLabelParam && currentLabel >= 0 && int(i.Imm) < len(ids[currentLabel]) {
				ids[currentLabel][i.Imm] = pos
			}
		}
	}
	for id := range p.Instructions {
		i := &p.Instructions[id]
		switch i.Op {
		case OpFAdd, OpFSub, OpFMul, OpFDiv, OpFMA, OpFSqrt, OpFConvert, OpFProductError, OpFFromSigned, OpFFromUnsigned:
			values[id] = true
		}
	}
	successors := make([][]int, len(values))
	for id := range p.Instructions {
		i := &p.Instructions[id]
		for _, x := range i.InputsInto(inputBuffer[:0]) {
			if p.Instructions[x].Op == OpLabelParam {
				successors[id] = append(successors[id], x)
			}
		}
	}
	for _, step := range p.Control.Steps {
		if step.IsLabel {
			continue
		}
		for _, edge := range []struct {
			label int
			args  []int
		}{{step.To, step.Args}, {step.Other, step.OtherArgs}} {
			if edge.label < 0 || edge.label >= len(ids) {
				continue
			}
			for n, source := range edge.args {
				if n < len(ids[edge.label]) && ids[edge.label][n] >= 0 {
					successors[source] = append(successors[source], ids[edge.label][n])
				}
			}
		}
	}
	queue := make([]int, 0, len(values))
	for id, floating := range values {
		if floating {
			queue = append(queue, id)
		}
	}
	for cursor := 0; cursor < len(queue); cursor++ {
		for _, id := range successors[queue[cursor]] {
			if !values[id] && (instructionWidth(&p.Instructions[id]) == 32 || instructionWidth(&p.Instructions[id]) == 64) {
				values[id] = true
				queue = append(queue, id)
			}
		}
	}
	for label, list := range ids {
		for n, id := range list {
			labels[label][n] = id >= 0 && values[id]
		}
	}
	return labels
}

func sourceHasLoop(p *Program) bool {
	if p == nil || p.Control == nil {
		return false
	}
	positions := make([]int, len(p.Control.Labels))
	for pos, s := range p.Control.Steps {
		if s.IsLabel {
			positions[s.Label] = pos
		}
	}
	for pos, s := range p.Control.Steps {
		if !s.IsLabel && (s.Term == TermJump || s.Term == TermBranch) && positions[s.To] <= pos {
			return true
		}
		if !s.IsLabel && s.Term == TermBranch && positions[s.Other] <= pos {
			return true
		}
	}
	return false
}

func hasFloatingArithmetic(p *Program) bool {
	for index := range p.Instructions {
		i := &p.Instructions[index]
		switch i.Op {
		case OpFAdd, OpFSub, OpFMul, OpFDiv, OpFMA, OpFSqrt, OpFConvert, OpFProductError, OpFFromSigned, OpFFromUnsigned, OpFToSigned, OpFToUnsigned, OpFCompare:
			return true
		}
	}
	return false
}

// Pack complete adjacency lists into small exactly filled slabs. This avoids
// page-rounded slack on large pointer-free allocations without allocating one
// object per value. Oversized individual lists get their own exact allocation.
func adjacencyStorage(counts []int) [][]int {
	out := make([][]int, len(counts))
	for start := 0; start < len(counts); {
		end, total := start, 0
		for end < len(counts) && (total+counts[end] <= 1024 || end == start) {
			total += counts[end]
			end++
		}
		if total > 0 {
			data := make([]int, total)
			offset := 0
			for i := start; i < end; i++ {
				n := counts[i]
				out[i] = data[offset : offset : offset+n]
				offset += n
			}
		}
		start = end
	}
	return out
}
