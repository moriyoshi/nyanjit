package builder

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

// TestSegmentErrorsMatchFinish builds invalid functions in both modes. A
// method error must be the same in both, and every error Finish reports must
// be reported by End.
func TestSegmentErrorsMatchFinish(t *testing.T) {
	other := NewBuilder()
	foreign := other.NewLabel()
	type when uint8
	const (
		atCall when = iota // a Builder method fails
		atEnd              // Finish and End fail
	)
	cases := []struct {
		name       string
		when       when
		validation bool // the error carries an *ir.ValidationError
		limit      error
		gen        generator
	}{
		{"unbound label", atEnd, false, nil, func(b *Builder) error {
			var c e
			x := c.v(b.Param())
			never := b.NewLabel()
			_ = never
			c.do(b.Return(x))
			return c.err
		}},
		{"unbound label after branch", atEnd, false, nil, func(b *Builder) error {
			var c e
			x := c.v(b.Param())
			a, never := b.NewLabel(), b.NewLabel()
			_ = never
			c.do(b.Branch(c.p(b.IsZero(x)), a))
			c.do(b.Return(x))
			c.do(b.Bind(a))
			c.do(b.Return(x))
			return c.err
		}},
		{"open scope", atEnd, false, nil, func(b *Builder) error {
			var c e
			c.v(b.Param())
			c.do(b.Enter())
			return c.err
		}},
		{"open scope with control", atEnd, false, nil, func(b *Builder) error {
			var c e
			c.v(b.Param())
			c.do(b.Enter())
			l := b.NewLabel()
			c.do(b.Jump(l))
			c.do(b.Bind(l))
			return c.err
		}},
		{"missing return", atEnd, false, nil, func(b *Builder) error {
			var c e
			x := c.v(b.Param())
			c.v(b.Add(x, x))
			return c.err
		}},
		{"empty function", atEnd, false, nil, func(b *Builder) error { return nil }},
		{"open block", atEnd, false, nil, func(b *Builder) error {
			var c e
			x := c.v(b.Param())
			l := b.NewLabel()
			c.do(b.Jump(l))
			c.do(b.Bind(l))
			c.v(b.Add(x, x))
			return c.err
		}},
		{"open fallthrough block", atEnd, false, nil, func(b *Builder) error {
			var c e
			x := c.v(b.Param())
			l := b.NewLabel()
			c.do(b.Branch(c.p(b.IsZero(x)), l))
			c.do(b.Return(x))
			c.do(b.Bind(l))
			// The fallthrough block of this Branch stays open.
			c.do(b.Branch(c.p(b.IsZero(x)), l))
			return c.err
		}},
		{"non-dominating operand", atEnd, true, nil, func(b *Builder) error {
			var c e
			x := c.v(b.Param())
			a, done := b.NewLabel(), b.NewLabel()
			c.do(b.Branch(c.p(b.IsZero(x)), a))
			y := c.v(b.Add(x, x))
			c.do(b.Jump(done))
			c.do(b.Bind(a))
			c.do(b.Jump(done))
			c.do(b.Bind(done))
			c.do(b.Return(c.v(b.Add(y, x))))
			return c.err
		}},
		{"non-dominating return", atEnd, true, nil, func(b *Builder) error {
			var c e
			x := c.v(b.Param())
			a, done := b.NewLabel(), b.NewLabel()
			c.do(b.Branch(c.p(b.IsZero(x)), a))
			y := c.v(b.Add(x, x))
			c.do(b.Jump(done))
			c.do(b.Bind(a))
			c.do(b.Jump(done))
			c.do(b.Bind(done))
			c.do(b.Return(y))
			return c.err
		}},
		{"non-dominating predicate", atEnd, true, nil, func(b *Builder) error {
			var c e
			x := c.v(b.Param())
			a, done, out := b.NewLabel(), b.NewLabel(), b.NewLabel()
			c.do(b.Branch(c.p(b.IsZero(x)), a))
			y := c.v(b.Add(x, x))
			c.do(b.Jump(done))
			c.do(b.Bind(a))
			c.do(b.Jump(done))
			c.do(b.Bind(done))
			c.do(b.Branch(c.p(b.IsZero(y)), out))
			c.do(b.Bind(out))
			c.do(b.Return(x))
			return c.err
		}},
		{"non-dominating Var initial", atEnd, true, nil, func(b *Builder) error {
			var c e
			x := c.v(b.Param())
			a, done := b.NewLabel(), b.NewLabel()
			c.do(b.Branch(c.p(b.IsZero(x)), a))
			y := c.v(b.Add(x, x))
			c.do(b.Jump(done))
			c.do(b.Bind(a))
			c.do(b.Jump(done))
			c.do(b.Bind(done))
			c.do(b.Enter())
			v := c.x(b.Var(y))
			r := c.v(b.Get(v))
			c.do(b.Leave(r))
			c.do(b.Return(r))
			return c.err
		}},
		{"non-dominating export", atEnd, true, nil, func(b *Builder) error {
			var c e
			x := c.v(b.Param())
			c.do(b.Enter())
			a, done := b.NewLabel(), b.NewLabel()
			c.do(b.Branch(c.p(b.IsZero(x)), a))
			y := c.v(b.Add(x, x))
			c.do(b.Jump(done))
			c.do(b.Bind(a))
			c.do(b.Jump(done))
			c.do(b.Bind(done))
			c.do(b.Leave(y))
			c.do(b.Return(y))
			return c.err
		}},
		{"non-dominating loop value", atEnd, true, nil, func(b *Builder) error {
			var c e
			x := c.v(b.Param())
			loop, body, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
			c.do(b.Jump(loop))
			c.do(b.Bind(loop))
			c.do(b.Branch(c.p(b.IsZero(x)), done))
			c.do(b.Jump(body))
			c.do(b.Bind(body))
			y := c.v(b.Add(x, x))
			c.do(b.Jump(loop))
			c.do(b.Bind(done))
			c.do(b.Return(y))
			return c.err
		}},
		{"parameter after a branching Enter", atEnd, false, nil, func(b *Builder) error {
			var c e
			c.do(b.Enter())
			x := c.v(b.Param())
			l := b.NewLabel()
			c.do(b.Jump(l))
			c.do(b.Bind(l))
			c.do(b.Leave(x))
			c.do(b.Return(x))
			return c.err
		}},
		{"mixed return widths", atEnd, true, nil, func(b *Builder) error {
			var c e
			x := c.v(b.Param())
			a := b.NewLabel()
			c.do(b.Branch(c.p(b.IsZero(x)), a))
			c.do(b.Return(x))
			c.do(b.Bind(a))
			c.do(b.Return(c.v(b.ConstBits(128, 1))))
			return c.err
		}},
		{"program predicate budget", atEnd, false, ir.ErrPredicateLimit, func(b *Builder) error {
			var c e
			x := c.v(b.Param())
			p := c.p(b.IsZero(x))
			for i := 0; i < 1<<11; i++ {
				p = p.Or(c.p(b.CompareImmediate(x, ir.BranchEqual, int64(i))))
			}
			for i := 0; i < 9; i++ {
				next := b.NewLabel()
				c.do(b.Branch(p, next))
				c.do(b.Bind(next))
			}
			c.do(b.Return(x))
			return c.err
		}},
		{"predicate limit", atCall, false, ErrBackendLimit, func(b *Builder) error {
			var c e
			x := c.v(b.Param())
			p := c.p(b.IsZero(x))
			for i := 0; i < 1<<13; i++ {
				p = p.Or(p)
				if i > 20 {
					break
				}
			}
			l := b.NewLabel()
			c.do(b.Branch(p, l))
			c.do(b.Bind(l))
			c.do(b.Return(x))
			return c.err
		}},
		{"foreign label", atCall, false, nil, func(b *Builder) error {
			var c e
			c.v(b.Param())
			c.do(b.Jump(foreign))
			return c.err
		}},
		{"zero label", atCall, false, nil, func(b *Builder) error {
			var c e
			x := c.v(b.Param())
			c.do(b.Branch(c.p(b.IsZero(x)), Label{}))
			return c.err
		}},
		{"label of another scope", atCall, false, nil, func(b *Builder) error {
			var c e
			x := c.v(b.Param())
			outer := b.NewLabel()
			c.do(b.Enter())
			c.do(b.Branch(c.p(b.IsZero(x)), outer))
			return c.err
		}},
		{"label bound twice", atCall, false, nil, func(b *Builder) error {
			var c e
			x := c.v(b.Param())
			l := b.NewLabel()
			c.do(b.Jump(l))
			c.do(b.Bind(l))
			c.do(b.Bind(l))
			c.do(b.Return(x))
			return c.err
		}},
		{"use after Leave", atCall, true, nil, func(b *Builder) error {
			var c e
			x := c.v(b.Param())
			c.do(b.Enter())
			y := c.v(b.Add(x, x))
			c.do(b.Leave())
			c.do(b.Return(c.v(b.Add(y, x))))
			return c.err
		}},
		{"Var after the entry sequence", atCall, false, nil, func(b *Builder) error {
			var c e
			x := c.v(b.Param())
			c.do(b.Enter())
			l := b.NewLabel()
			c.do(b.Jump(l))
			c.do(b.Bind(l))
			c.x(b.Var(x))
			return c.err
		}},
		{"width mismatch", atCall, false, nil, func(b *Builder) error {
			var c e
			x := c.v(b.Param())
			y := c.v(b.ParamBits(32))
			c.v(b.Add(x, y))
			return c.err
		}},
		{"argument limit", atCall, false, ErrBackendLimit, func(b *Builder) error {
			var c e
			for i := 0; i <= backendMaxArgs; i++ {
				c.v(b.Param())
			}
			return c.err
		}},
		{"indexed address across an edge", atCall, false, nil, func(b *Builder) error {
			var c e
			base := c.v(b.ParamNative())
			bad := b.NewLabel()
			address := c.v(b.IndexedAddress(base, base, base, 1, bad))
			c.x(b.Var(address))
			return c.err
		}},
	}
	for _, tc := range cases {
		want := runRecording(tc.gen)
		got, _ := runSegments(tc.gen)
		wantErr, gotErr := want.endErr, got.endErr
		if tc.when == atCall {
			wantErr, gotErr = want.callErr, got.callErr
		} else if want.callErr != nil || got.callErr != nil {
			t.Errorf("%s: unexpected method errors %v, %v", tc.name, want.callErr, got.callErr)
			continue
		}
		if wantErr == nil || gotErr == nil {
			t.Errorf("%s: Finish mode error %v, segment mode error %v", tc.name, wantErr, gotErr)
			continue
		}
		if !errors.Is(wantErr, ErrInvalidFunction) && !errors.Is(wantErr, ErrBackendLimit) {
			t.Errorf("%s: error %v is neither invalid nor a limit", tc.name, wantErr)
		}
		if !sameError(wantErr, gotErr) {
			t.Errorf("%s: Finish mode error %v, segment mode error %v", tc.name, wantErr, gotErr)
		}
		var v *ir.ValidationError
		if tc.validation != errors.As(gotErr, &v) {
			t.Errorf("%s: segment error %v: ValidationError present %v, want %v", tc.name, gotErr, !tc.validation, tc.validation)
		}
		if tc.limit != nil && (!errors.Is(wantErr, tc.limit) || !errors.Is(gotErr, tc.limit)) {
			t.Errorf("%s: errors %v, %v do not match %v", tc.name, wantErr, gotErr, tc.limit)
		}
		if tc.when == atEnd && !errors.Is(want.endErr, ErrInvalidFunction) {
			t.Errorf("%s: Finish error %v does not wrap ErrInvalidFunction", tc.name, want.endErr)
		}
		if tc.when == atEnd && !errors.Is(got.endErr, ErrInvalidFunction) {
			t.Errorf("%s: End error %v does not wrap ErrInvalidFunction", tc.name, got.endErr)
		}
	}
}

func TestSegmentModeMismatch(t *testing.T) {
	b := NewBuilder()
	x, _ := b.Param()
	_ = b.Return(x)
	if err := b.End(); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("End on a recording Builder: %v", err)
	}
	if _, err := b.Finish(); err != nil {
		t.Fatalf("Finish after a rejected End: %v", err)
	}
	s := NewSegmentBuilder(func(*ir.Segment) error { return nil })
	y, _ := s.Param()
	_ = s.Return(y)
	if _, err := s.Finish(); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("Finish on a segment Builder: %v", err)
	}
	if err := s.End(); err != nil {
		t.Fatalf("End after a rejected Finish: %v", err)
	}
	if err := s.End(); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("second End: %v", err)
	}
	if _, err := s.Add(y, y); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("method after End: %v", err)
	}
	// A Label of an earlier run of the same generator is rejected.
	first := NewSegmentBuilder(func(*ir.Segment) error { return nil })
	stale := first.NewLabel()
	second := NewSegmentBuilder(func(*ir.Segment) error { return nil })
	second.NewLabel()
	if err := second.Jump(stale); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("stale label: %v", err)
	}
}

// TestSegmentEmitErrors makes emit fail for each segment in turn. The method
// that handed the segment over returns emit's error, later methods return
// ErrInvalidFunction, and End returns emit's error.
func TestSegmentEmitErrors(t *testing.T) {
	type step struct {
		name string
		run  func(b *Builder, c *e, x Value, labels []Label) error
	}
	// Each step ends exactly one block, except where noted.
	steps := []step{
		{"Jump", func(b *Builder, c *e, x Value, l []Label) error { return b.Jump(l[0]) }},
		{"Bind", func(b *Builder, c *e, x Value, l []Label) error {
			if _, err := b.Add(x, x); err != nil {
				return err
			}
			return b.Bind(l[1]) // the implicit edge to l[1]
		}},
		{"Branch then Jump", func(b *Builder, c *e, x Value, l []Label) error {
			p, err := b.IsZero(x)
			if err != nil {
				return err
			}
			if err := b.Branch(p, l[2]); err != nil {
				return err
			}
			return b.Jump(l[3]) // merges into the Branch and hands it over
		}},
		{"Return", func(b *Builder, c *e, x Value, l []Label) error { return b.Return(x) }},
	}
	build := func(b *Builder, failing int) (failure error, after error) {
		x, _ := b.Param()
		labels := []Label{b.NewLabel(), b.NewLabel(), b.NewLabel(), b.NewLabel()}
		var c e
		for i, s := range steps {
			if i > 0 && i < 4 {
				// Bind the label the previous step jumped to.
				switch i {
				case 1:
					c.do(b.Bind(labels[0]))
				case 3:
					c.do(b.Bind(labels[3]))
				}
			}
			err := s.run(b, &c, x, labels)
			if i == failing {
				failure = err
				_, after = b.Add(x, x)
				return failure, after
			}
		}
		return nil, nil
	}
	for failing := range steps {
		rec := NewSegmentRecorder()
		rec.FailAt = failing
		b := NewSegmentBuilder(rec.Emit)
		failure, after := build(b, failing)
		if !errors.Is(failure, ErrFailedEmit) {
			t.Errorf("%s: method returned %v, want emit's error", steps[failing].name, failure)
		}
		if !errors.Is(after, ErrInvalidFunction) {
			t.Errorf("%s: later method returned %v", steps[failing].name, after)
		}
		if err := b.End(); !errors.Is(err, ErrFailedEmit) {
			t.Errorf("%s: End returned %v", steps[failing].name, err)
		}
	}
	// The staged block of a Branch that no Jump follows is handed over by
	// the next operation; End reports emit's error and later methods fail.
	rec := NewSegmentRecorder()
	rec.FailAt = 0
	b := NewSegmentBuilder(rec.Emit)
	x, _ := b.Param()
	l := b.NewLabel()
	p, _ := b.IsZero(x)
	if err := b.Branch(p, l); err != nil {
		t.Fatalf("staged Branch: %v", err)
	}
	b.Add(x, x)
	if _, err := b.Add(x, x); !errors.Is(err, ErrInvalidFunction) {
		t.Errorf("method after the failed hand-over: %v", err)
	}
	if err := b.End(); !errors.Is(err, ErrFailedEmit) {
		t.Errorf("End after a staged failure: %v", err)
	}
}

// TestSegmentShapes checks the segments themselves: program-wide IDs, a
// capture delivered by a later segment, the entry segment's parameters, and
// that the Builder reuses its storage.
func TestSegmentShapes(t *testing.T) {
	r, rec := runSegments(segmentCaptures)
	if r.callErr != nil || r.endErr != nil {
		t.Fatal(r.callErr, r.endErr)
	}
	enters := map[int]int{} // scope -> segment holding its OpScopeEnter
	for i, s := range rec.Segments {
		for j, ins := range s.Code {
			if ins.Op == ir.OpParam && i != 0 {
				t.Errorf("segment %d: OpParam outside the entry segment", i)
			}
			if ins.Op == ir.OpScopeEnter {
				enters[int(ins.Imm)] = i
				if len(ins.Args) != 0 {
					t.Errorf("segment %d: OpScopeEnter %d carries Args %v; captures arrive later", i, s.Base+j, ins.Args)
				}
			}
		}
	}
	later := 0
	for i, s := range rec.Segments {
		for _, v := range s.Variables {
			if v.Immutable {
				if enters[v.Region] >= i {
					t.Errorf("capture of scope %d declared in segment %d, at or before its Enter's segment", v.Region, i)
				}
				later++
			}
		}
	}
	if later == 0 {
		t.Fatal("no capture was delivered by a later segment")
	}
	if len(rec.Segments[0].Labels) != 0 {
		t.Errorf("entry segment binds %v", rec.Segments[0].Labels)
	}
	// One code buffer serves every segment.
	var buffers []*ir.Instruction
	b := NewSegmentBuilder(func(s *ir.Segment) error {
		if len(s.Code) != 0 {
			buffers = append(buffers, &s.Code[:1][0])
		}
		return nil
	})
	if err := segmentDiamonds(16)(b); err != nil {
		t.Fatal(err)
	}
	if err := b.End(); err != nil {
		t.Fatal(err)
	}
	distinct := map[*ir.Instruction]bool{}
	for _, p := range buffers {
		distinct[p] = true
	}
	if len(distinct) > 3 {
		t.Errorf("segments used %d code buffers", len(distinct))
	}
}
