package builder

import "github.com/moriyoshi/nyanjit/ir"

// tightCopy transfers an exactly sized backing array into a sealed program.
// Merely clipping capacity would still keep the complete old allocation alive.
func tightCopy[T any](s []T) []T {
	if len(s) == 0 {
		if s == nil {
			return nil
		}
		return []T{}
	}
	if len(s) == cap(s) {
		return s
	}
	out := make([]T, len(s))
	copy(out, s)
	return out
}
func compactProgram(p *ir.Program, indices []int) {
	if len(indices) == 0 && len(p.ScopeExits) == 0 && len(p.Variables) == 0 && p.Control == nil {
		return
	}
	p.Instructions = tightCopy(p.Instructions)
	p.ScopeExits = tightCopy(p.ScopeExits)
	p.Variables = tightCopy(p.Variables)
	p.ResultParts = tightCopy(p.ResultParts)
	// Pack slack integer lists into one exact-size slab. Already exact lists
	// stay in place, except variable initials, which borrow a builder arena.
	countSlice := func(v []int) int {
		if len(v) != cap(v) {
			return len(v)
		}
		return 0
	}
	count := 0
	if len(indices) > 0 {
		for _, i := range indices {
			count += countSlice(p.Instructions[i].Args)
		}
	}
	for _, leave := range p.ScopeExits {
		count += countSlice(leave.Hidden) + countSlice(leave.Exports)
	}
	for _, v := range p.Variables {
		count += len(v.Initial)
	}
	if c := p.Control; c != nil {
		for _, step := range c.Steps {
			count += countSlice(step.Values) + countSlice(step.Args) + countSlice(step.OtherArgs) + countSlice(step.ResultParts)
		}
	}
	data := make([]int, count)
	compactInts := func(v []int, force bool) []int {
		if !force && len(v) == cap(v) {
			return v
		}
		if len(v) == 0 {
			if v == nil {
				return nil
			}
			return []int{}
		}
		n := len(v)
		copy(data, v)
		out := data[:n:n]
		data = data[n:]
		return out
	}
	if len(indices) > 0 {
		for _, i := range indices {
			ins := &p.Instructions[i]
			if e := ins.Effect; e != nil {
				e.ArgumentRegisters = tightCopy(e.ArgumentRegisters)
				e.ClobberedRegisters = tightCopy(e.ClobberedRegisters)
				e.ClobberedFPRegisters = tightCopy(e.ClobberedFPRegisters)
				e.ClobberedSpecialRegisters = tightCopy(e.ClobberedSpecialRegisters)
				e.ArgumentLocations = tightCopy(e.ArgumentLocations)
				for j := range e.ArgumentLocations {
					e.ArgumentLocations[j].Words = tightCopy(e.ArgumentLocations[j].Words)
				}
				e.ResultLocation.Words = tightCopy(e.ResultLocation.Words)
			}
			if len(ins.Args) != cap(ins.Args) {
				ins.Args = compactInts(ins.Args, false)
			}
			if len(ins.Data) != cap(ins.Data) {
				ins.Data = tightCopy(ins.Data)
			}
		}
	}
	for i := range p.ScopeExits {
		p.ScopeExits[i].Hidden = compactInts(p.ScopeExits[i].Hidden, false)
		p.ScopeExits[i].Exports = compactInts(p.ScopeExits[i].Exports, false)
	}
	for i := range p.Variables {
		p.Variables[i].Initial = compactInts(p.Variables[i].Initial, true)
	}
	if c := p.Control; c != nil {
		c.Steps = tightCopy(c.Steps)
		c.Labels = tightCopy(c.Labels)
		c.LabelScopes = tightCopy(c.LabelScopes)
		c.Params = tightCopy(c.Params)
		c.ParamBits = tightCopy(c.ParamBits)
		c.ParamTypes = tightCopy(c.ParamTypes)
		for i := range c.ParamBits {
			c.ParamBits[i] = tightCopy(c.ParamBits[i])
		}
		for i := range c.ParamTypes {
			c.ParamTypes[i] = tightCopy(c.ParamTypes[i])
		}
		for i := range c.Steps {
			s := &c.Steps[i]
			s.Values = compactInts(s.Values, false)
			s.Args = compactInts(s.Args, false)
			s.OtherArgs = compactInts(s.OtherArgs, false)
			s.ResultParts = compactInts(s.ResultParts, false)
		}
	}
}
