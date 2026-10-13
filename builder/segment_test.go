package builder

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// generator builds one function. It returns the first error a Builder
// method returned, as a client would see it.
type generator func(b *Builder) error

// e collects the first error of a sequence of Builder calls.
type e struct{ err error }

func (c *e) v(v Value, err error) Value {
	if c.err == nil {
		c.err = err
	}
	return v
}
func (c *e) p(p Predicate, err error) Predicate {
	if c.err == nil {
		c.err = err
	}
	return p
}
func (c *e) x(v Var, err error) Var {
	if c.err == nil {
		c.err = err
	}
	return v
}
func (c *e) do(err error) {
	if c.err == nil {
		c.err = err
	}
}

type modeResult struct {
	program *ir.SealedProgram
	callErr error // the first error a Builder method returned
	endErr  error // Finish's or End's error
}

func runRecording(gen generator) modeResult {
	b := NewBuilder()
	var r modeResult
	r.callErr = gen(b)
	r.program, r.endErr = b.Finish()
	return r
}

func runSegments(gen generator) (modeResult, *SegmentRecorder) {
	rec := NewSegmentRecorder()
	b := NewSegmentBuilder(rec.Emit)
	var r modeResult
	r.callErr = gen(b)
	if r.endErr = b.End(); r.endErr == nil {
		r.program, r.endErr = rec.Seal()
	}
	return r, rec
}

// sameError reports whether two errors of the two modes agree: both nil,
// or both of the same class.
func sameError(x, y error) bool {
	if (x == nil) != (y == nil) {
		return false
	}
	for _, class := range []error{ErrInvalidFunction, ErrBackendLimit, ir.ErrPredicateLimit} {
		if errors.Is(x, class) != errors.Is(y, class) {
			return false
		}
	}
	var vx, vy *ir.ValidationError
	return errors.As(x, &vx) == errors.As(y, &vy)
}

// checkBothModes builds gen in both modes and requires the same errors and,
// for a valid function, the same program.
func checkBothModes(t *testing.T, name string, gen generator) (valid bool) {
	t.Helper()
	want := runRecording(gen)
	got, _ := runSegments(gen)
	if !sameError(want.callErr, got.callErr) {
		t.Errorf("%s: method error %v, segment mode %v", name, want.callErr, got.callErr)
		return false
	}
	if !sameError(want.endErr, got.endErr) {
		t.Errorf("%s: Finish error %v, End error %v", name, want.endErr, got.endErr)
		return false
	}
	if want.endErr != nil {
		return false
	}
	if diff := ProgramDifference(want.program.Clone(), got.program.Clone()); diff != "" {
		t.Errorf("%s: segment program differs: %s", name, diff)
		return false
	}
	return true
}

func TestProgramDifferenceDetectsChanges(t *testing.T) {
	r := runRecording(segmentDiamonds(3))
	if r.endErr != nil {
		t.Fatal(r.endErr)
	}
	p := r.program.Clone()
	if diff := ProgramDifference(p, r.program.Clone()); diff != "" {
		t.Fatalf("a program differs from itself: %s", diff)
	}
	q := r.program.Clone()
	q.Instructions[2].Imm++
	if ProgramDifference(p, q) == "" {
		t.Fatal("changed immediate not detected")
	}
	q = r.program.Clone()
	for i := range q.Control.Steps {
		if q.Control.Steps[i].Predicate != nil {
			q.Control.Steps[i].Predicate.Immediate++
			break
		}
	}
	if ProgramDifference(p, q) == "" {
		t.Fatal("changed predicate not detected")
	}
}

// The hand-written programs cover each piece of retroactive or cross-block
// work: scope-entry splits (nested, at the entry block, after Bind), captures
// found in later blocks, staged Branch blocks and their merges, deferred
// expressions across blocks, Carry and literal folding of values from earlier
// blocks, IndexedAddress continuations, Data views, 128-bit values, vectors,
// calls and libm.
var segmentPrograms = map[string]generator{
	"straight": func(b *Builder) error {
		var c e
		x := c.v(b.Param())
		c.do(b.Return(c.v(b.Add(x, c.v(b.Const(3))))))
		return c.err
	},
	"diamonds":       segmentDiamonds(8),
	"loop":           segmentLoop,
	"nested-splits":  segmentNestedSplits,
	"captures":       segmentCaptures,
	"bind-at-entry":  segmentBindAtEntry,
	"merge-variants": segmentMergeVariants,
	"deferred":       segmentDeferred,
	"carry-literal":  segmentCarryLiteral,
	"indexed":        segmentIndexed,
	"data":           segmentData,
	"wide":           segmentWide,
	"vector-call":    segmentVectorCall,
	"unreachable":    segmentUnreachable,
}

func TestSegmentProgramsMatchFinish(t *testing.T) {
	for name, gen := range segmentPrograms {
		if !checkBothModes(t, name, gen) {
			t.Errorf("%s: not a valid program", name)
		}
	}
}

// TestSegmentListingsMatch lowers both modes' programs for arm64 and amd64.
func TestSegmentListingsMatch(t *testing.T) {
	for name, gen := range segmentPrograms {
		want := runRecording(gen)
		got, _ := runSegments(gen)
		if want.endErr != nil || got.endErr != nil {
			t.Fatalf("%s: %v, %v", name, want.endErr, got.endErr)
		}
		for _, arch := range []string{"arm64", "amd64"} {
			spec := compile.TargetSpec{Architecture: arch}
			f := compile.Func{Context: compile.ContextExternal, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 14}}
			l1, e1 := lowerListing(want.program, spec, f)
			l2, e2 := lowerListing(got.program, spec, f)
			if l1 != l2 || (e1 == nil) != (e2 == nil) {
				t.Errorf("%s %s: listings differ (%v, %v)", name, arch, e1, e2)
			}
		}
	}
}

func TestSegmentRandomProgramsMatchFinish(t *testing.T) {
	seeds := 600
	if testing.Short() {
		seeds = 100
	}
	valid := 0
	for seed := 0; seed < seeds; seed++ {
		gen := func(b *Builder) error { return newRandomProgram(int64(seed), b).build() }
		if checkBothModes(t, fmt.Sprintf("seed %d", seed), gen) {
			valid++
		}
	}
	if valid < seeds/2 {
		t.Fatalf("only %d of %d random programs are valid", valid, seeds)
	}
	t.Logf("%d of %d random programs valid and identical", valid, seeds)
}

func segmentDiamonds(n int) generator {
	return func(b *Builder) error {
		var c e
		x := c.v(b.Param())
		acc := c.x(b.Var(x))
		for i := 0; i < n; i++ {
			yes, done := b.NewLabel(), b.NewLabel()
			c.do(b.Branch(c.p(b.CompareImmediate(c.v(b.Get(acc)), ir.BranchBelow, int64(100+i))), yes))
			c.do(b.Set(acc, c.v(b.Add(c.v(b.Get(acc)), c.v(b.Const(uint64(i)))))))
			c.do(b.Jump(done))
			c.do(b.Bind(yes))
			c.do(b.Set(acc, c.v(b.Xor(c.v(b.Get(acc)), x))))
			c.do(b.Bind(done))
		}
		c.do(b.Return(c.v(b.Get(acc))))
		return c.err
	}
}

func segmentLoop(b *Builder) error {
	var c e
	n := c.v(b.Param())
	k := c.v(b.Const(7))
	i, acc := c.x(b.Var(c.v(b.Const(0)))), c.x(b.Var(c.v(b.Const(0))))
	loop, done := b.NewLabel(), b.NewLabel()
	c.do(b.Bind(loop))
	c.do(b.Branch(c.p(b.Compare(c.v(b.Get(i)), ir.BranchAboveEqual, n)), done))
	c.do(b.Set(acc, c.v(b.Add(c.v(b.Get(acc)), c.v(b.Mul(c.v(b.Get(i)), k))))))
	c.do(b.Set(i, c.v(b.Add(c.v(b.Get(i)), c.v(b.Const(1))))))
	c.do(b.Jump(loop))
	c.do(b.Bind(done))
	c.do(b.Return(c.v(b.Get(acc))))
	return c.err
}

// segmentNestedSplits enters two scopes in one block and branches in the
// inner one, so the block splits at both entries. The outer scope's values
// cross both splits, and a value from before Enter is used after Leave.
func segmentNestedSplits(b *Builder) error {
	var c e
	x := c.v(b.Param())
	y := c.v(b.Add(x, x))
	c.do(b.Enter())
	z := c.v(b.Mul(y, x))
	v := c.x(b.Var(z))
	c.do(b.Enter())
	w := c.v(b.Sub(z, y))
	u := c.x(b.Var(w))
	yes, done := b.NewLabel(), b.NewLabel()
	c.do(b.Branch(c.p(b.IsZero(w)), yes))
	c.do(b.Set(u, c.v(b.Add(c.v(b.Get(u)), y))))
	c.do(b.Jump(done))
	c.do(b.Bind(yes))
	c.do(b.Set(u, c.v(b.Xor(c.v(b.Get(u)), x))))
	c.do(b.Bind(done))
	r := c.v(b.Get(u))
	c.do(b.Leave(r))
	c.do(b.Set(v, c.v(b.Add(c.v(b.Get(v)), r))))
	s := c.v(b.Get(v))
	c.do(b.Leave(s))
	c.do(b.Return(c.v(b.Add(s, y))))
	return c.err
}

// segmentCaptures uses outer values in later blocks of nested scopes. The
// captures are discovered after their scopes' OpScopeEnter was emitted.
func segmentCaptures(b *Builder) error {
	var c e
	x := c.v(b.Param())
	y := c.v(b.Param())
	k := c.v(b.Mul(x, y))
	c.do(b.Enter())
	t1 := c.v(b.Add(k, x))
	i := c.x(b.Var(c.v(b.Const(3))))
	loop, done := b.NewLabel(), b.NewLabel()
	c.do(b.Bind(loop))
	c.do(b.Branch(c.p(b.IsZero(c.v(b.Get(i)))), done))
	c.do(b.Enter())
	inner := b.NewLabel()
	c.do(b.Jump(inner))
	c.do(b.Bind(inner))
	// k and t1 come from earlier blocks: k is captured by the outer scope,
	// and through it by the inner one.
	m := c.v(b.Add(c.v(b.Mul(k, t1)), c.v(b.Get(i))))
	c.do(b.Leave())
	_ = m
	c.do(b.Set(i, c.v(b.Sub(c.v(b.Get(i)), c.v(b.Const(1))))))
	c.do(b.Jump(loop))
	c.do(b.Bind(done))
	r := c.v(b.Add(k, t1))
	c.do(b.Leave(r))
	c.do(b.Return(r))
	return c.err
}

// segmentBindAtEntry binds a loop label before any instruction, so the entry
// segment carries a label.
func segmentBindAtEntry(b *Builder) error {
	var c e
	top, done := b.NewLabel(), b.NewLabel()
	c.do(b.Bind(top))
	x := c.v(b.Const(5))
	c.do(b.Branch(c.p(b.IsZero(x)), top))
	c.do(b.Jump(done))
	c.do(b.Bind(done))
	c.do(b.Return(x))
	return c.err
}

// segmentMergeVariants exercises the staged Branch block: merged with a
// Jump, not merged because a label intervenes, followed by Bind, Return,
// Leave and another Branch.
func segmentMergeVariants(b *Builder) error {
	var c e
	x := c.v(b.Param())
	v := c.x(b.Var(x))
	a, d, f, g := b.NewLabel(), b.NewLabel(), b.NewLabel(), b.NewLabel()
	c.do(b.Branch(c.p(b.IsZero(x)), a)) // merged
	c.do(b.Jump(d))
	c.do(b.Bind(a))
	c.do(b.Branch(c.p(b.IsNegative(x)), d))
	extra := b.NewLabel() // the fallthrough label is no longer the last
	c.do(b.Jump(d))
	c.do(b.Bind(d))
	c.do(b.Branch(c.p(b.CompareImmediate(x, ir.BranchEqual, 3)), f))
	c.do(b.Bind(extra)) // Bind on the empty fallthrough block
	c.do(b.Branch(c.p(b.CompareImmediate(x, ir.BranchEqual, 4)), f))
	c.do(b.Branch(c.p(b.CompareImmediate(x, ir.BranchEqual, 5)), g)) // Branch on it
	c.do(b.Set(v, c.v(b.Add(x, x))))
	c.do(b.Jump(f))
	c.do(b.Bind(g))
	c.do(b.Jump(f))
	c.do(b.Bind(f))
	c.do(b.Return(c.v(b.Get(v))))
	return c.err
}

// segmentDeferred reuses deferred expressions in the block that
// materialized them and rematerializes them elsewhere.
func segmentDeferred(b *Builder) error {
	var c e
	x := c.v(b.Param())
	y := c.v(b.Param())
	shared := x.Mul(y).Add(x.MulConst(8)).Xor(y.ShiftLeft(3))
	acc := c.x(b.Var(shared))
	yes, done := b.NewLabel(), b.NewLabel()
	c.do(b.Branch(c.p(b.Compare(shared, ir.BranchBelow, y)), yes))
	c.do(b.Set(acc, c.v(b.Add(shared, x))))
	c.do(b.Jump(done))
	c.do(b.Bind(yes))
	c.do(b.Set(acc, c.v(b.Sub(shared, y))))
	c.do(b.Bind(done))
	lit := c.v(b.Literal(12))
	c.do(b.Return(c.v(b.Add(c.v(b.Get(acc)), lit.Add(shared)))))
	return c.err
}

// segmentCarryLiteral takes Carry of an Add and a Sub from earlier blocks,
// and folds a constant from an earlier block into a scaled address.
func segmentCarryLiteral(b *Builder) error {
	var c e
	x := c.v(b.ParamNative())
	y := c.v(b.ParamNative())
	sum := c.v(b.Add(x, y))
	one := c.v(b.ConstNative(1))
	diff := c.v(b.Sub(x, one))
	scale := c.v(b.ConstNative(8))
	r := c.x(b.Var(sum))
	l1, l2, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
	c.do(b.Jump(l1))
	c.do(b.Bind(l1))
	c.do(b.Branch(c.p(b.Carry(sum)), l2))
	c.do(b.Set(r, c.v(b.Add(x, y.Mul(scale)))))
	c.do(b.Jump(done))
	c.do(b.Bind(l2))
	c.do(b.Branch(c.p(b.Carry(diff)), done))
	c.do(b.Set(r, diff))
	c.do(b.Jump(done))
	c.do(b.Bind(done))
	c.do(b.Return(c.v(b.Get(r))))
	return c.err
}

func segmentIndexed(b *Builder) error {
	var c e
	base := c.v(b.ParamNative())
	index := c.v(b.ParamNative())
	count := c.v(b.ParamNative())
	bad := b.NewLabel()
	address := c.v(b.IndexedAddress(base, index, count, 8, bad))
	loaded := c.v(b.Load64(address))
	c.do(b.Return(c.v(b.Add(loaded, index))))
	c.do(b.Bind(bad))
	c.do(b.Return(c.v(b.Const(0))))
	return c.err
}

type segmentRecord struct {
	A, B  uint64
	Items []uint32
}

func segmentData(b *Builder) error {
	var c e
	d, err := PointerParam[segmentRecord](b)
	c.do(err)
	if c.err != nil {
		return c.err
	}
	a, err := d.At("A")
	c.do(err)
	items, err := d.At("Items")
	c.do(err)
	n := c.v(items.Len())
	bad := b.NewLabel()
	first, err := items.Index(c.v(b.ConstNative(0)), bad)
	c.do(err)
	if c.err != nil {
		return c.err
	}
	x := c.v(first.Load())
	c.do(a.Store(c.v(b.Add(c.v(b.CastUnsigned(x, 64)), c.v(b.CastUnsigned(n, 64))))))
	c.do(b.Return(c.v(b.CastUnsigned(x, 64))))
	c.do(b.Bind(bad))
	c.do(b.Return(c.v(b.Const(0))))
	return c.err
}

func segmentWide(b *Builder) error {
	var c e
	x := c.v(b.ParamBits(128))
	y := c.v(b.ParamBits(128))
	v := c.x(b.Var(x))
	yes, done := b.NewLabel(), b.NewLabel()
	c.do(b.Branch(c.p(b.Compare(x, ir.BranchLess, y)), yes))
	c.do(b.Set(v, c.v(b.Mul(x, y))))
	c.do(b.Jump(done))
	c.do(b.Bind(yes))
	c.do(b.Set(v, c.v(b.ShiftLeft(c.v(b.Add(x, y)), 3))))
	c.do(b.Bind(done))
	c.do(b.Return(c.v(b.Get(v))))
	return c.err
}

func segmentVectorCall(b *Builder) error {
	var c e
	p := c.v(b.ParamNative())
	f := c.v(b.Float64(1.5))
	vec := c.v(b.VLoad(ir.Vec(2, 64), p))
	sum := c.v(b.Add(vec, vec))
	c.do(b.VStore(p, sum))
	lane := c.v(b.ExtractLane(sum, 1))
	done := b.NewLabel()
	c.do(b.Jump(done))
	c.do(b.Bind(done))
	s := c.v(b.FSin(f))
	call := c.v(b.CallC(p, lane, c.v(b.Const(2))))
	c.do(b.Return(c.v(b.Xor(call, s))))
	return c.err
}

// segmentUnreachable uses a non-dominating value only in an unreachable
// block, which both modes accept.
func segmentUnreachable(b *Builder) error {
	var c e
	x := c.v(b.Param())
	a, z, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
	c.do(b.Branch(c.p(b.IsZero(x)), a))
	y := c.v(b.Add(x, x))
	c.do(b.Jump(done))
	c.do(b.Bind(a))
	c.do(b.Jump(done))
	c.do(b.Bind(z)) // no edge reaches z
	c.do(b.Return(y))
	c.do(b.Bind(done))
	c.do(b.Return(x))
	return c.err
}

// randomProgram builds structured random functions: arithmetic on Values
// and deferred expressions, Vars, nested scopes with exports, diamonds,
// loops, captures of outer values in later blocks, and predicates.
type randomProgram struct {
	r      *rand.Rand
	b      *Builder
	c      e
	values [][]Value // usable values per scope level
	vars   [][]Var
	steps  int
}

func newRandomProgram(seed int64, b *Builder) *randomProgram {
	return &randomProgram{r: rand.New(rand.NewSource(seed)), b: b}
}

func (g *randomProgram) pick() Value {
	level := g.r.Intn(len(g.values))
	for len(g.values[level]) == 0 {
		level = (level + 1) % len(g.values)
	}
	vs := g.values[level]
	return vs[g.r.Intn(len(vs))]
}

func (g *randomProgram) push(v Value) {
	if v.builder != nil {
		g.values[len(g.values)-1] = append(g.values[len(g.values)-1], v)
	}
}

func (g *randomProgram) arith() Value {
	b, x, y := g.b, g.pick(), g.pick()
	switch g.r.Intn(9) {
	case 0:
		return g.c.v(b.Add(x, y))
	case 1:
		return g.c.v(b.Sub(x, y))
	case 2:
		return g.c.v(b.Mul(x, y))
	case 3:
		return g.c.v(b.Xor(x, y))
	case 4:
		return g.c.v(b.ShiftRight(x, uint8(g.r.Intn(70))))
	case 5:
		return x.Add(y.MulConst(uint64(1 << g.r.Intn(4))))
	case 6:
		return x.Xor(y).Add(g.c.v(b.Literal(uint64(g.r.Intn(9)))))
	case 7:
		return g.c.v(b.Select(x, y, g.pick()))
	default:
		return g.c.v(b.Const(uint64(g.r.Intn(1000))))
	}
}

func (g *randomProgram) predicate() Predicate {
	b := g.b
	p := g.c.p(b.CompareImmediate(g.pick(), ir.BranchCondition(g.r.Intn(10)), int64(g.r.Intn(64))))
	switch g.r.Intn(4) {
	case 0:
		return p.And(g.c.p(b.Compare(g.pick(), ir.BranchBelow, g.pick())))
	case 1:
		return p.Or(g.c.p(b.IsZero(g.pick()))).Not()
	}
	return p
}

func (g *randomProgram) block(depth int) {
	b := g.b
	for n := 1 + g.r.Intn(4); n > 0 && g.c.err == nil && g.steps < 400; n-- {
		g.steps++
		switch k := g.r.Intn(10); {
		case k < 4:
			g.push(g.arith())
		case k == 4 && len(g.vars[len(g.vars)-1]) != 0:
			vs := g.vars[len(g.vars)-1]
			v := vs[g.r.Intn(len(vs))]
			g.c.do(b.Set(v, g.c.v(b.Add(g.c.v(b.Get(v)), g.pick()))))
		case k == 5 && depth < 3:
			// diamond merging through a Var of this scope, declared first
			// if the scope's entry sequence is still open
			yes, done := b.NewLabel(), b.NewLabel()
			g.c.do(b.Branch(g.predicate(), yes))
			g.block(depth + 1)
			g.c.do(b.Jump(done))
			g.c.do(b.Bind(yes))
			g.block(depth + 1)
			g.c.do(b.Bind(done))
			g.pruneBlockLocal()
		case k == 6 && depth < 3:
			g.scope(depth)
		case k == 7 && depth < 2 && len(g.vars[len(g.vars)-1]) != 0:
			loop, done := b.NewLabel(), b.NewLabel()
			counter := g.vars[len(g.vars)-1][0]
			g.c.do(b.Jump(loop))
			g.c.do(b.Bind(loop))
			g.c.do(b.Branch(g.c.p(b.CompareImmediate(g.c.v(b.Get(counter)), ir.BranchAbove, 20)), done))
			g.block(depth + 1)
			g.c.do(b.Set(counter, g.c.v(b.Add(g.c.v(b.Get(counter)), g.c.v(b.Const(7))))))
			g.c.do(b.Jump(loop))
			g.c.do(b.Bind(done))
			g.pruneBlockLocal()
		default:
			p := g.c.p(b.Carry(g.c.v(b.Add(g.pick(), g.pick()))))
			skip := b.NewLabel()
			g.c.do(b.Branch(p, skip))
			g.push(g.arith())
			g.c.do(b.Bind(skip))
			g.pruneBlockLocal()
		}
	}
}

// pruneBlockLocal drops values that may not dominate the merge just bound:
// only values of the scope's first entries stay.
func (g *randomProgram) pruneBlockLocal() {
	level := len(g.values) - 1
	if keep := min(len(g.values[level]), 2); keep < len(g.values[level]) {
		g.values[level] = g.values[level][:keep]
	}
}

func (g *randomProgram) scope(depth int) {
	b := g.b
	g.c.do(b.Enter())
	g.values = append(g.values, nil)
	g.vars = append(g.vars, nil)
	g.push(g.arith())
	g.vars[len(g.vars)-1] = append(g.vars[len(g.vars)-1], g.c.x(b.Var(g.pick())))
	if g.r.Intn(2) == 0 {
		g.vars[len(g.vars)-1] = append(g.vars[len(g.vars)-1], g.c.x(b.Var(g.c.v(b.Const(1)))))
	}
	g.block(depth + 1)
	result := g.c.v(b.Get(g.vars[len(g.vars)-1][0]))
	g.values = g.values[:len(g.values)-1]
	g.vars = g.vars[:len(g.vars)-1]
	if g.r.Intn(2) == 0 {
		g.c.do(b.Leave(result))
		g.push(result)
	} else {
		g.c.do(b.Leave())
	}
}

func (g *randomProgram) build() error {
	b := g.b
	g.values = [][]Value{nil}
	g.vars = [][]Var{nil}
	for i := 1 + g.r.Intn(3); i > 0; i-- {
		g.push(g.c.v(b.Param()))
	}
	g.vars[0] = append(g.vars[0], g.c.x(b.Var(g.c.v(b.Const(0)))), g.c.x(b.Var(g.pick())))
	g.block(0)
	g.c.do(b.Return(g.c.v(b.Add(g.c.v(b.Get(g.vars[0][1])), g.pick()))))
	return g.c.err
}

// TestSegmentConcurrentBuilders builds on several goroutines, which share
// the recycled segment buffers.
func TestSegmentConcurrentBuilders(t *testing.T) {
	const workers = 8
	errs := make(chan string, workers)
	for w := 0; w < workers; w++ {
		go func() {
			for seed := w; seed < 200; seed += workers {
				gen := func(b *Builder) error { return newRandomProgram(int64(seed), b).build() }
				want := runRecording(gen)
				got, _ := runSegments(gen)
				if !sameError(want.endErr, got.endErr) {
					errs <- fmt.Sprintf("seed %d: %v, %v", seed, want.endErr, got.endErr)
					return
				}
				if want.endErr == nil {
					if diff := ProgramDifference(want.program.Clone(), got.program.Clone()); diff != "" {
						errs <- fmt.Sprintf("seed %d: %s", seed, diff)
						return
					}
				}
			}
			errs <- ""
		}()
	}
	for w := 0; w < workers; w++ {
		if msg := <-errs; msg != "" {
			t.Error(msg)
		}
	}
}
