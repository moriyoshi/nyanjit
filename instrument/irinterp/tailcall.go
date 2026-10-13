package irinterp

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/ir"
)

// tailCall runs a TermTailCall as a C call through Options.Call followed by
// a return of its 64-bit result. ids are the step's Args: the target, then
// the arguments.
func (m *machine) tailCall(ids []int) error {
	if m.opts.Call == nil {
		return fmt.Errorf("tail call without Options.Call: %w", ErrUnsupported)
	}
	args := make([]uint64, len(ids)-1)
	unknownInput := false
	for n, id := range ids {
		unknownInput = unknownInput || m.vals[id].t != exact
		if n > 0 {
			args[n-1] = m.vals[id].x
		}
	}
	x, err := m.opts.Call(Call{Op: ir.OpCallC, Index: -1, Target: m.vals[ids[0]].x, Args: args})
	if err != nil {
		return err
	}
	m.result = Result{Lo: x, Bits: 64, Unspecified: unknownInput || m.control}
	return nil
}
