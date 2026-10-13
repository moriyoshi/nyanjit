package builder

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

// switchGenerator switches on a w-bit selector over cases, which index
// labels; the last label is the default. Every label returns a constant.
func switchGenerator(w uint8, labels int, cases []int) generator {
	return func(b *Builder) error {
		var c e
		s := c.v(b.ParamBits(w))
		list := make([]Label, labels)
		for i := range list {
			list[i] = b.NewLabel()
		}
		targets := make([]Label, len(cases))
		for i, k := range cases {
			targets[i] = list[k]
		}
		c.do(b.Switch(s, targets, list[labels-1]))
		for i, l := range list {
			c.do(b.Bind(l))
			c.do(b.Return(c.v(b.Const(uint64(i)))))
		}
		return c.err
	}
}

func TestSwitchBothModes(t *testing.T) {
	for _, w := range []uint8{8, 32, 64} {
		if !checkBothModes(t, "switch", switchGenerator(w, 4, []int{0, 1, 0, 2, 2})) {
			continue
		}
		r := runRecording(switchGenerator(w, 4, []int{0, 1, 0, 2, 2}))
		var step *ir.ControlStep
		for i := range r.program.Control().Steps {
			if s := &r.program.Control().Steps[i]; !s.IsLabel && s.Term == ir.TermSwitch {
				step = s
			}
		}
		if step == nil || len(step.Cases) != 5 || step.Cases[0] != step.Cases[2] || step.Result != 0 || len(step.Args) != 0 {
			t.Fatalf("width %d: switch step %+v", w, step)
		}
	}
	// A Switch from the empty fallthrough block of a Branch, and a Switch
	// inside a scope whose labels belong to it.
	checkBothModes(t, "after branch", func(b *Builder) error {
		var c e
		s := c.v(b.Param())
		one, two := b.NewLabel(), b.NewLabel()
		c.do(b.Branch(c.p(b.IsZero(s)), one))
		c.do(b.Switch(s, []Label{one, two, one}, two))
		c.do(b.Bind(one))
		c.do(b.Return(s))
		c.do(b.Bind(two))
		c.do(b.Return(c.v(b.Const(9))))
		return c.err
	})
}

func TestSwitchErrors(t *testing.T) {
	other := NewBuilder()
	foreign := other.NewLabel()
	cases := map[string]struct {
		gen  generator
		want error
	}{
		"no cases": {func(b *Builder) error {
			s, _ := b.Param()
			l := b.NewLabel()
			return b.Switch(s, nil, l)
		}, ErrInvalidFunction},
		"foreign case": {func(b *Builder) error {
			s, _ := b.Param()
			l := b.NewLabel()
			return b.Switch(s, []Label{foreign}, l)
		}, ErrInvalidFunction},
		"zero default": {func(b *Builder) error {
			s, _ := b.Param()
			l := b.NewLabel()
			return b.Switch(s, []Label{l}, Label{})
		}, ErrInvalidFunction},
		"vector selector": {func(b *Builder) error {
			s, _ := b.Param()
			v, _ := b.Splat(ir.Vec(2, 64), s)
			l := b.NewLabel()
			return b.Switch(v, []Label{l}, l)
		}, ErrInvalidFunction},
		"too many cases": {func(b *Builder) error {
			s, _ := b.Param()
			l := b.NewLabel()
			list := make([]Label, ir.MaxPredicateNodes+1)
			for i := range list {
				list[i] = l
			}
			return b.Switch(s, list, l)
		}, ErrBackendLimit},
		"closed block": {func(b *Builder) error {
			s, _ := b.Param()
			l := b.NewLabel()
			if err := b.Switch(s, []Label{l}, l); err != nil {
				return err
			}
			return b.Jump(l)
		}, ErrInvalidFunction},
	}
	for name, c := range cases {
		for _, segments := range []bool{false, true} {
			var r modeResult
			if segments {
				r, _ = runSegments(c.gen)
			} else {
				r = runRecording(c.gen)
			}
			if !errors.Is(r.callErr, c.want) {
				t.Errorf("%s (segments %v): %v, want %v", name, segments, r.callErr, c.want)
			}
		}
	}
	// The program's total counts the cases with the predicates.
	budget := func(b *Builder) error {
		var c e
		s := c.v(b.Param())
		l, next := b.NewLabel(), b.NewLabel()
		c.do(b.Branch(c.p(b.IsZero(s)), next))
		c.do(b.Bind(next))
		list := make([]Label, ir.MaxPredicateNodes)
		for i := range list {
			list[i] = l
		}
		c.do(b.Switch(s, list, l))
		c.do(b.Bind(l))
		c.do(b.Return(s))
		return c.err
	}
	for _, segments := range []bool{false, true} {
		var r modeResult
		if segments {
			r, _ = runSegments(budget)
		} else {
			r = runRecording(budget)
		}
		if r.callErr != nil || !errors.Is(r.endErr, ErrBackendLimit) {
			t.Errorf("program budget (segments %v): %v, %v", segments, r.callErr, r.endErr)
		}
	}
}
