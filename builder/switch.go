package builder

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/ir"
)

// Switch ends the block: selector (an unsigned scalar of at most 64 bits) below
// len(cases) continues at cases[selector], and any other value at otherwise.
// Labels may repeat. Like Jump, it leaves no open block; Bind the next label.
//
// Each case counts as one node against ir.MaxPredicateNodes, per Switch and
// in the program's total; above it Switch, or Finish or End for the total,
// returns an error wrapping ErrBackendLimit. An empty cases list is invalid:
// use Jump.
func (b *Builder) Switch(selector Value, cases []Label, otherwise Label) error {
	if !b.canAppend() || b.control == nil {
		return fmt.Errorf("Switch: current block is closed: %w", ErrInvalidFunction)
	}
	to, ok := b.localLabel(otherwise)
	if !ok || b.control.privateLabels[to] {
		return b.labelError("Switch", otherwise)
	}
	if len(cases) == 0 {
		return fmt.Errorf("Switch: no cases; use Jump: %w", ErrInvalidFunction)
	}
	if len(cases) > ir.MaxPredicateNodes {
		return newLimitError("predicate nodes", len(cases), ir.MaxPredicateNodes, "switch", "Switch: %d cases exceed the predicate-node limit (%d)", len(cases), ir.MaxPredicateNodes)
	}
	ids := make([]int, len(cases))
	for i, label := range cases {
		id, ok := b.localLabel(label)
		if !ok || b.control.privateLabels[id] {
			return b.labelError("Switch", label)
		}
		ids[i] = int(id)
	}
	values, err := b.materializeValues([]Value{selector})
	if err != nil {
		return err
	}
	selector = values[0]
	if !b.valid(selector) {
		return fmt.Errorf("Switch: selector is invalid in this block: %w", ErrInvalidFunction)
	}
	if t := selector.Type(); t.Kind != ir.ScalarKind || t.Lanes != 1 || t.LaneBits > 64 {
		return fmt.Errorf("Switch: selector must be a scalar of at most 64 bits: %w", ErrInvalidFunction)
	}
	if s := b.seg; s != nil {
		s.predicateNodes += len(cases)
		s.use(selector.id, -1)
	}
	_, err = b.endControl(controlEnd{term: builderSwitch, result: 0, to: to, other: -1, selector: selector.id, cases: ids})
	return err
}
