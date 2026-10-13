package compile

import "github.com/moriyoshi/nyanjit/ir"

func (l *vectorLegalizer) initializeVariables() {
	l.variableIDs = make([][]int, len(l.source.Variables))
	for id, v := range l.source.Variables {
		count, bits := 1, v.Type.LaneBits
		if v.Type.Kind == ir.VectorKind || v.Type.Kind == ir.MaskKind {
			count = int(v.Type.Lanes)
			if v.Type.Kind == ir.MaskKind {
				bits = 8
			}
		}
		for lane := 0; lane < count; lane++ {
			copy := v
			if count > 1 {
				copy.Type = ir.Scalar(bits)
			}
			copy.Initial = nil
			l.variableIDs[id] = append(l.variableIDs[id], len(l.out.Variables))
			l.out.Variables = append(l.out.Variables, copy)
		}
	}
}
func (l *vectorLegalizer) finishVariables() {
	for id, v := range l.source.Variables {
		ids := l.variableIDs[id]
		for lane, dst := range ids {
			copy := &l.out.Variables[dst]
			copy.Declaration = l.boundary[v.Declaration]
			for _, initial := range v.Initial {
				values := l.values[initial].lanes
				if len(ids) > 1 {
					copy.Initial = append(copy.Initial, values[lane])
				} else {
					copy.Initial = append(copy.Initial, values...)
				}
			}
			if !v.Immutable && len(ids) > 1 {
				copy.Declaration += lane
			}
		}
	}
}
func (l *vectorLegalizer) variableOperation(id int, i ir.Instruction) (bool, error) {
	if i.Op == ir.OpScopeEnter || i.Op == ir.OpScopeLeave {
		copy := i.Clone()
		copy.Args = nil
		for _, x := range i.Args {
			copy.Args = append(copy.Args, l.values[x].lanes...)
		}
		x := l.emit(copy)
		l.values[id] = legalizedValue{lanes: []int{x}}
		return true, nil
	}
	if i.Op != ir.OpGet && i.Op != ir.OpSet {
		return false, nil
	}
	ids := l.variableIDs[i.Variable]
	out := legalizedValue{}
	for lane, dst := range ids {
		copy := i
		copy.Variable = dst
		if len(ids) > 1 {
			copy.Type = l.out.Variables[dst].Type
			copy.Bits = copy.Type.LaneBits
			copy.Imm = 0
		}
		if i.Op == ir.OpSet {
			values := l.values[i.A].lanes
			if len(ids) == 1 {
				copy.A = values[0]
			} else {
				copy.A = values[lane]
			}
		}
		out.lanes = append(out.lanes, l.emit(copy))
	}
	if i.Op == ir.OpSet {
		out.lanes = out.lanes[len(out.lanes)-1:]
	}
	l.values[id] = out
	return true, nil
}
