package ir

import (
	"errors"
	"testing"
)

// ScopeLeave Args are semantic inputs: current-segment operands and values
// from a dominating segment are valid.
func TestScopeLeaveArgsSegment(t *testing.T) {
	base := &Program{
		Instructions: []Instruction{{Op: OpConst}, {Op: OpScopeEnter, Imm: 1}, {Op: OpConst}, {Op: OpConst}, {Op: OpScopeLeave, Imm: 1}, {Op: OpConst}},
		ScopeExits:   []ScopeExit{{ID: 1, Enter: 1, Position: 5}}, Result: -1,
		Control: &Control{Labels: []bool{true, true}, LabelScopes: []int{0, 1}, Params: []int{0, 0}, ParamBits: [][]uint8{nil, nil}, Steps: []ControlStep{
			{IsLabel: true, Label: 0}, {Start: 0, End: 3, Term: TermJump, To: 1},
			{IsLabel: true, Label: 1}, {Start: 3, End: 6, LeaveEnd: 1, Term: TermReturn, Result: 5},
		}},
	}
	for _, tc := range []struct {
		name  string
		args  []int
		valid bool
	}{
		{"empty", nil, true}, {"current", []int{3}, true}, {"duplicate-current", []int{3, 3}, true},
		{"previous-segment", []int{2}, true}, {"root-previous-segment", []int{0}, true},
		{"mixed", []int{3, 2}, true}, {"self", []int{4}, false}, {"future", []int{5}, false},
		{"negative", []int{-1}, false}, {"non-value", []int{1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base.Clone()
			p.Instructions[4].Args = tc.args
			err := p.Validate()
			if (err == nil) != tc.valid {
				t.Fatalf("Validate=%v; valid=%v", err, tc.valid)
			}
			_, sealedErr := Seal(p)
			if (sealedErr == nil) != tc.valid {
				t.Fatalf("Seal=%v; valid=%v", sealedErr, tc.valid)
			}
			if !tc.valid && (!errors.Is(err, ErrInvalidProgram) || !errors.Is(sealedErr, ErrInvalidProgram)) {
				t.Fatalf("validation errors must wrap ErrInvalidProgram: %v / %v", err, sealedErr)
			}
		})
	}
}
