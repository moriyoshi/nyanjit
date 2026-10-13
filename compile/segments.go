package compile

import "github.com/moriyoshi/nyanjit/ir"

// programSegments hands a validated program to yield one ir.Segment per
// block, in layout order. The segments borrow the program's storage, and seg
// is reused between calls.
//
// Two liberties keep the split exact for any valid program: the whole
// Variables table rides on the first segment, so no capture is declared after
// its scope's OpScopeEnter and Args need no patching; and the labels bound
// before the first segment (the entry label) are listed on it, so a jump back
// to the entry keeps its label number. Stream accepts both.
func programSegments(p *ir.Program, yield func(*ir.Segment) error) error {
	var seg ir.Segment
	if p.Control == nil {
		seg = ir.Segment{
			Code: p.Instructions, Variables: p.Variables, ScopeExits: p.ScopeExits,
			ParameterTypes: p.ParameterTypes,
			Step: ir.ControlStep{
				Term: ir.TermReturn, End: len(p.Instructions), LeaveEnd: len(p.ScopeExits),
				Result: p.Result, ResultParts: p.ResultParts, Arity: p.Arity,
			},
		}
		return yield(&seg)
	}
	var labels []ir.Label
	first := true
	for i := range p.Control.Steps {
		step := &p.Control.Steps[i]
		if step.IsLabel {
			labels = append(labels, ir.Label(step.Label))
			continue
		}
		seg = ir.Segment{
			Labels: labels, Base: step.Start, Code: p.Instructions[step.Start:step.End],
			ScopeExits: p.ScopeExits[step.LeaveStart:step.LeaveEnd], Step: *step,
		}
		if first {
			seg.Variables, seg.ParameterTypes = p.Variables, p.ParameterTypes
			first = false
		}
		if err := yield(&seg); err != nil {
			return err
		}
		labels = labels[:0]
	}
	return nil
}
