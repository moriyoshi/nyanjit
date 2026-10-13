//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || 386 || loong64)

package varfuzz

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io"

	"fmt"
	"math"
	"math/bits"

	"strings"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/ir"
)

// ---------------------------------------------------------------- AST

type Temp struct {
	id   int
	bits uint8
	fp   bool
}

type VarD struct {
	id      int
	bits    uint8
	fp      bool
	counter bool
	init    *Expr
	scope   *Scope
}

type Scope struct {
	depth int
	vars  []*VarD
	body  []*Stmt
	// loop state of the scope's state loops ("loopp"): declared in the
	// entry sequence after vars, never chosen by the generator or hashed.
	state []*VarD
	// exported hash temp (set in parent after Leave)
	hashT *Temp
}

type Expr struct {
	op      string
	bits    uint8
	fp      bool
	val     uint64
	f       float64
	v       *VarD
	t       *Temp
	a, b, c *Expr
}

type Stmt struct {
	kind   string
	t      *Temp
	v      *VarD
	vs     []*VarD
	e      *Expr
	pa, pb *Expr
	cond   ir.BranchCondition
	pk     int
	imm    int64
	form   int
	then   []*Stmt
	els    []*Stmt
	n      uint64
	ctr    *VarD
	body   []*Stmt
	scope  *Scope
	// state loop: k and x are carried by kV and xV, Vars of the enclosing scope.
	kV, xV   *VarD
	kbits    uint8
	xbits    uint8
	xinit    *Expr
	xnext    *Expr // evaluated at end of inner scope
	kT, xT   *Temp // head snapshots of kV and xV
	k2T, x2T *Temp // body aliases of kT and xT, used by dominance (captures)
	knT, xnT *Temp // exports
	useX     *VarD // var that absorbs head param x each iteration
	ck       int
	args     []*Expr
	dead     bool
	target   *Stmt
}

type Program struct {
	seed    int64
	root    *Scope
	hash    *VarD
	params  []*Temp // p0 64, p1 32, p2 native
	nvars   int
	ntemps  int
	hasCall bool
	goCalls bool // needs managed (CallGo or callback)
}

// ---------------------------------------------------------------- generator

type gen struct {
	r       choices
	breaks  bool
	noScope bool
	p       *Program
	fp      bool
	calls   bool
	scopes  []*Scope
	locals  []*Temp
	caps    [][]*Temp
	budget  int
	vbudget int
	ctlDep  int
	loopDep int
	loops   []*Stmt
	loopAt  []int
	active  map[*VarD]bool
}

var widths = []uint8{8, 16, 32, 64}

func (g *gen) newTemp(bits uint8, fp bool) *Temp {
	g.p.ntemps++
	return &Temp{id: g.p.ntemps, bits: bits, fp: fp}
}

func (g *gen) visibleVars() []*VarD {
	var out []*VarD
	for _, s := range g.scopes {
		out = append(out, s.vars...)
	}
	return out
}

func (g *gen) availTemps() []*Temp {
	var out []*Temp
	for _, c := range g.caps {
		out = append(out, c...)
	}
	return append(out, g.locals...)
}

func (g *gen) pickWidth() (uint8, bool) {
	if g.fp && g.r.Intn(5) == 0 {
		return 64, true
	}
	return widths[g.r.Intn(4)], false
}

func (g *gen) constE(bits uint8) *Expr {
	var v uint64
	switch g.r.Intn(4) {
	case 0:
		v = uint64(g.r.Intn(16))
	case 1:
		v = ^uint64(0) - uint64(g.r.Intn(4))
	default:
		v = g.r.Uint64()
	}
	return &Expr{op: "const", bits: bits, val: mask(v, bits)}
}

var fconsts = []float64{0.5, 1, 1.5, 2, -0.75, 3, 0.25, -1.25, 10}

func (g *gen) leaf(bits uint8, fp bool) *Expr {
	var cands []*Expr
	for _, v := range g.visibleVars() {
		if v.bits == bits && v.fp == fp || !fp && v.fp && bits == 64 && g.r.Intn(3) == 0 {
			cands = append(cands, &Expr{op: "get", bits: bits, fp: fp, v: v})
		}
	}
	for _, t := range g.availTemps() {
		if t.bits == bits && t.fp == fp {
			cands = append(cands, &Expr{op: "temp", bits: bits, fp: fp, t: t}, &Expr{op: "temp", bits: bits, fp: fp, t: t})
		}
	}
	if fp {
		cands = append(cands, &Expr{op: "fconst", bits: 64, fp: true, f: fconsts[g.r.Intn(len(fconsts))]})
	} else {
		cands = append(cands, g.constE(bits))
	}
	return cands[g.r.Intn(len(cands))]
}

func (g *gen) expr(bits uint8, fp bool, depth int) *Expr {
	if depth <= 0 || g.r.Intn(3) == 0 {
		return g.leaf(bits, fp)
	}
	if fp {
		ops := []string{"fadd", "fsub", "fmul"}
		op := ops[g.r.Intn(3)]
		a := g.expr(64, true, depth-1)
		var b *Expr
		if op == "fmul" {
			// keep magnitudes in check
			b = &Expr{op: "fconst", bits: 64, fp: true, f: []float64{0.5, -0.75, 1, 0.25, -1}[g.r.Intn(5)]}
			if g.r.Intn(4) == 0 {
				b = g.leaf(64, true)
			}
		} else {
			b = g.expr(64, true, depth-1)
		}
		return &Expr{op: op, bits: 64, fp: true, a: a, b: b}
	}
	switch k := g.r.Intn(14); {
	case k < 6:
		op := []string{"add", "sub", "mul", "and", "or", "xor"}[k]
		return &Expr{op: op, bits: bits, a: g.expr(bits, false, depth-1), b: g.expr(bits, false, depth-1)}
	case k < 9:
		op := []string{"rotl", "rotr", "shr"}[k-6]
		return &Expr{op: op, bits: bits, val: uint64(1 + g.r.Intn(int(bits)-1)), a: g.expr(bits, false, depth-1)}
	case k < 11:
		from, _ := widths[g.r.Intn(4)], 0
		return &Expr{op: "cast", bits: bits, a: g.expr(from, false, depth-1)}
	case k < 12 && bits == 64:
		from := widths[g.r.Intn(3)]
		src := widths[g.r.Intn(4)]
		if src < from {
			src = from
		}
		return &Expr{op: "sext", bits: 64, val: uint64(from), a: g.expr(src, false, depth-1)}
	case k < 13:
		cb := widths[g.r.Intn(4)]
		return &Expr{op: "sel", bits: bits, a: g.expr(cb, false, depth-1), b: g.expr(bits, false, depth-1), c: g.expr(bits, false, depth-1)}
	default:
		return g.leaf(bits, fp)
	}
}

func (g *gen) newVar(s *Scope, bits uint8, fp bool, init *Expr) *VarD {
	g.p.nvars++
	v := &VarD{id: g.p.nvars, bits: bits, fp: fp, init: init, scope: s}
	s.vars = append(s.vars, v)
	return v
}

// stateVar declares loop state that only its loop reads and writes.
func (g *gen) stateVar(s *Scope, bits uint8) *VarD {
	g.p.nvars++
	v := &VarD{id: g.p.nvars, bits: bits, init: &Expr{op: "const", bits: bits}, scope: s}
	s.state = append(s.state, v)
	return v
}

func (g *gen) declVars(s *Scope, count int) {
	for i := 0; i < count; i++ {
		bits, fp := g.pickWidth()
		var init *Expr
		if g.r.Intn(3) == 0 {
			init = g.leaf(bits, fp)
		} else {
			init = g.expr(bits, fp, 2)
		}
		g.newVar(s, bits, fp, init)
	}
}

func (g *gen) mutableVars(bits uint8, fp bool, any bool) []*VarD {
	var out []*VarD
	for _, v := range g.visibleVars() {
		if v.counter || v == g.p.hash {
			continue
		}
		if any || v.bits == bits && v.fp == fp {
			out = append(out, v)
		}
	}
	return out
}

func (g *gen) stmts(maxn int) []*Stmt {
	var out []*Stmt
	cnt := 1 + g.r.Intn(maxn)
	for i := 0; i < cnt && g.budget > 0; i++ {
		out = append(out, g.stmt()...)
	}
	return out
}

func (g *gen) pred(s *Stmt) {
	bits := widths[g.r.Intn(len(widths))]
	s.pk = g.r.Intn(4)
	s.pa = g.expr(bits, false, 2)
	s.cond = ir.BranchCondition(g.r.Intn(10))

	switch s.pk {
	case 0, 3:
		s.pb = g.expr(bits, false, 1)
	case 2:
		s.imm = int64(g.r.Intn(20)) - 10
	}
}

func (g *gen) stmt() []*Stmt {
	g.budget--
	if g.breaks && len(g.loops) > 0 && g.loopAt[len(g.loopAt)-1] == len(g.scopes) && g.r.Intn(10) == 0 {
		s := &Stmt{kind: "brk", target: g.loops[len(g.loops)-1]}
		g.pred(s)
		g.locals = nil
		return []*Stmt{s}
	}
	k := g.r.Intn(100)
	vars := g.mutableVars(0, false, true)
	switch {
	case k < 18:
		bits, fp := g.pickWidth()
		var e *Expr
		if g.r.Intn(2) == 0 && len(vars) > 0 {
			v := vars[g.r.Intn(len(vars))]
			bits, fp = v.bits, v.fp
			e = &Expr{op: "get", bits: bits, fp: fp, v: v}
		} else {
			e = g.expr(bits, fp, 3)
		}
		t := g.newTemp(bits, fp)
		g.locals = append(g.locals, t)
		return []*Stmt{{kind: "let", t: t, e: e}}
	case k < 45 && len(vars) > 0:
		v := vars[g.r.Intn(len(vars))]
		if v.fp && g.r.Intn(6) == 0 {
			// integer op on FP home: sign flip
			return []*Stmt{{kind: "set", v: v, e: &Expr{op: "xor", bits: 64, a: &Expr{op: "get", bits: 64, v: v}, b: &Expr{op: "const", bits: 64, val: 1 << 63}}}}
		}
		return []*Stmt{{kind: "set", v: v, e: g.expr(v.bits, v.fp, 3)}}
	case k < 53 && len(vars) > 1:
		x := vars[g.r.Intn(len(vars))]
		same := g.mutableVars(x.bits, x.fp, false)
		if len(same) < 2 {
			return nil
		}
		g.r.Shuffle(len(same), func(i, j int) { same[i], same[j] = same[j], same[i] })
		m := 2
		if len(same) > 2 && g.r.Intn(2) == 0 {
			m = 3
		}
		return []*Stmt{{kind: "rot", vs: same[:m]}}
	case k < 63 && g.ctlDep < 4:
		s := &Stmt{kind: "if", form: g.r.Intn(2)}
		g.pred(s)
		g.ctlDep++
		g.locals = nil
		s.then = g.stmts(4)
		g.locals = nil
		if g.r.Intn(3) != 0 {
			s.els = g.stmts(4)
		}
		g.locals = nil
		g.ctlDep--
		return []*Stmt{s}
	case k < 74 && g.loopDep < 3 && g.ctlDep < 4:
		s := &Stmt{kind: "loopv", form: g.r.Intn(3), n: uint64(g.r.Intn(5))}
		if s.form == 2 && s.n == 0 {
			s.n = 1
		}
		// counter: reuse a free counter of an enclosing scope, or a new one in the current scope.
		var free []*VarD
		for _, v := range g.visibleVars() {
			if v.counter && !g.active[v] {
				free = append(free, v)
			}
		}
		if len(free) > 0 && g.r.Intn(3) == 0 {
			s.ctr = free[g.r.Intn(len(free))]
		} else {
			bits := widths[g.r.Intn(len(widths))]
			s.ctr = g.newVar(g.scopes[len(g.scopes)-1], bits, false, &Expr{op: "const", bits: bits})
			s.ctr.counter = true
		}
		g.active[s.ctr] = true
		g.loopDep++
		g.ctlDep++
		g.locals = nil
		if s.form != 2 {
			g.loops = append(g.loops, s)
			g.loopAt = append(g.loopAt, len(g.scopes))
		}
		s.body = g.stmts(5)
		if s.form != 2 {
			g.loops = g.loops[:len(g.loops)-1]
			g.loopAt = g.loopAt[:len(g.loopAt)-1]
		}
		g.locals = nil
		g.ctlDep--
		g.loopDep--
		g.active[s.ctr] = false
		return []*Stmt{s}
	case k < 82 && !g.noScope && g.loopDep < 3 && len(g.scopes) < 4 && g.ctlDep < 4:
		s := &Stmt{kind: "loopp", n: uint64(g.r.Intn(5)), kbits: widths[g.r.Intn(len(widths))]}
		if g.r.Intn(3) != 0 {
			s.xbits, _ = g.pickWidth()
			s.xinit = g.expr(s.xbits, false, 2)
			if mv := g.mutableVars(s.xbits, false, false); len(mv) > 0 {
				s.useX = mv[g.r.Intn(len(mv))]
			}
		}
		// The loop state lives in Vars of the current scope, declared in its
		// entry sequence. They draw no choices, so a choice stream gives the
		// same program shape as when the state was label parameters.
		current := g.scopes[len(g.scopes)-1]
		s.kV = g.stateVar(current, s.kbits)
		if s.xbits != 0 {
			s.xV = g.stateVar(current, s.xbits)
		}
		g.locals = nil
		s.kT = g.newTemp(s.kbits, false)
		if s.xbits != 0 {
			s.xT = g.newTemp(s.xbits, false)
		}
		g.locals = nil
		s.k2T = g.newTemp(s.kbits, false)
		g.locals = []*Temp{s.k2T}
		if s.xbits != 0 {
			s.x2T = g.newTemp(s.xbits, false)
			g.locals = append(g.locals, s.x2T)
		}
		g.loopDep++
		g.ctlDep++
		s.scope = g.scopeBody(func() {
			if s.xbits != 0 {
				s.xnext = &Expr{op: "xor", bits: s.xbits, a: g.expr(s.xbits, false, 2), b: &Expr{op: "cast", bits: s.xbits, a: &Expr{op: "get", bits: 64, v: g.p.hash}}}
			}
		})
		g.ctlDep--
		g.loopDep--
		s.knT = g.newTemp(s.kbits, false)
		if s.xbits != 0 {
			s.xnT = g.newTemp(s.xbits, false)
		}
		g.locals = nil
		return []*Stmt{s}
	case k < 90 && !g.noScope && len(g.scopes) < 4:
		s := &Stmt{kind: "scope"}
		s.scope = g.scopeBody(nil)
		g.locals = []*Temp{s.scope.hashT}
		return []*Stmt{s}
	case k < 100 && g.calls:
		s := &Stmt{kind: "call", ck: g.r.Intn(3)}
		ar := 1 + g.r.Intn(3)
		for i := 0; i < ar; i++ {
			s.args = append(s.args, &Expr{op: "cast", bits: builder.NativeBits, a: g.expr(widths[g.r.Intn(4)], false, 2)})
		}
		s.t = g.newTemp(64, false)
		g.locals = append(g.locals, s.t)
		if s.ck != 2 {
			g.p.goCalls = true
		}
		g.p.hasCall = true
		return []*Stmt{s}
	}
	return nil
}

// anyVarOfWidthForExport returns a visible var whose Get gives a value of
// width bits (cast later); it always returns hash if nothing matches.
func (g *gen) anyVarOfWidthForExport(bits uint8) *VarD {
	var c []*VarD
	for _, v := range g.visibleVars() {
		if v.bits == bits && !v.fp {
			c = append(c, v)
		}
	}
	if len(c) == 0 {
		return nil
	}
	return c[g.r.Intn(len(c))]
}

func (g *gen) scopeBody(tail func()) *Scope {
	s := &Scope{depth: len(g.scopes)}
	g.caps = append(g.caps, g.locals)
	g.locals = nil
	g.scopes = append(g.scopes, s)
	nv := 0
	if g.vbudget > 0 {
		nv = g.r.Intn(min(g.vbudget, 10) + 1)
		g.vbudget -= nv
	}
	g.declVars(s, nv)
	s.body = g.stmts(6)
	if tail != nil {
		tail()
	}
	s.hashT = g.newTemp(64, false)
	g.scopes = g.scopes[:len(g.scopes)-1]
	g.caps = g.caps[:len(g.caps)-1]
	return s
}

//go:embed testdata/legacy/*
var legacyCorpus embed.FS

// Generate is used only by the historical regression fixtures and minimizer.
// It replays recorded choices; new fuzz cases use generateBytes directly.
func Generate(seed int64, fp, calls bool) *Program {
	data, err := readLegacyProgram(fmt.Sprintf("seed-%d-%t", seed, fp))
	if err != nil {
		data = sweepBytes(uint64(seed))
	}
	return generateBytes(data, seed, fp, calls)
}

// readLegacyProgram decodes one stored stream. Compression is only storage;
// the generator consumes the exact original decision bytes.
func readLegacyProgram(name string) ([]byte, error) {
	packed, err := legacyCorpus.ReadFile("testdata/legacy/" + name + ".gz")
	if err != nil {
		return nil, err
	}
	reader, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func generateBytes(data []byte, seed int64, fp, calls bool) *Program {
	if len(data) >= 4 && string(data[:3]) == "var" {
		p := minimizedProgram(data[3])
		p.seed = seed
		return p
	}
	return generateChoices(&byteChoices{data: data}, seed, fp, calls)
}

func generateChoices(r choices, seed int64, fp, calls bool) *Program {
	features := r.Intn(4)
	p := &Program{seed: seed}
	g := &gen{r: r, p: p, fp: fp, calls: calls && r.Intn(2) == 0, active: map[*VarD]bool{}}
	g.breaks, g.noScope = features&1 != 0, features&2 != 0
	g.budget = 10 + r.Intn(50)
	g.vbudget = 1 + r.Intn(30)
	root := &Scope{}
	p.root = root
	root.hashT = &Temp{id: -1, bits: 64}
	p.params = []*Temp{g.newTemp(64, false), g.newTemp(32, false), g.newTemp(builder.NativeBits, false)}
	g.locals = append([]*Temp(nil), p.params...)
	g.scopes = []*Scope{root}
	p.hash = g.newVar(root, 64, false, &Expr{op: "const", bits: 64, val: 0x1234})
	nv := 1 + r.Intn(min(g.vbudget, 16))
	g.vbudget -= nv
	g.declVars(root, nv)
	for g.budget > 0 {
		root.body = append(root.body, g.stmt()...)
	}
	return p
}

// ---------------------------------------------------------------- semantics

func mask(v uint64, bits uint8) uint64 {
	if bits >= 64 {
		return v
	}
	return v & (1<<bits - 1)
}

func sext(v uint64, bits uint8) int64 {
	return int64(v<<(64-bits)) >> (64 - bits)
}

type badProgram struct{ why string }

type interp struct {
	vars  map[*VarD]uint64
	temps map[*Temp]uint64
	steps int
	p     *Program
	calls int
	brk   *Stmt
}

func (in *interp) fpcheck(f float64) uint64 {
	if math.IsNaN(f) || math.IsInf(f, 0) || f != 0 && math.Abs(f) < 1e-300 || math.Abs(f) > 1e300 {
		panic(badProgram{"fp range"})
	}
	return math.Float64bits(f)
}

func (in *interp) eval(e *Expr) uint64 {
	in.steps++
	if in.steps > 5_000_000 {
		panic(badProgram{"too long"})
	}
	switch e.op {
	case "const":
		return mask(e.val, e.bits)
	case "fconst":
		return math.Float64bits(e.f)
	case "get":
		return in.vars[e.v]
	case "temp":
		v, ok := in.temps[e.t]
		if !ok {
			panic(badProgram{"undefined temp"})
		}
		return v
	case "fadd", "fsub", "fmul":
		a, b := math.Float64frombits(in.eval(e.a)), math.Float64frombits(in.eval(e.b))
		switch e.op {
		case "fadd":
			return in.fpcheck(a + b)
		case "fsub":
			return in.fpcheck(a - b)
		}
		return in.fpcheck(a * b)
	case "rotl", "rotr", "shr":
		a := in.eval(e.a)
		k := uint(e.val) % uint(e.bits)
		w := uint(e.bits)
		switch e.op {
		case "rotl":
			return mask(a<<k|a>>(w-k), e.bits)
		case "rotr":
			return mask(a>>k|a<<(w-k), e.bits)
		}
		return a >> k
	case "cast":
		return mask(in.eval(e.a), e.bits)
	case "sext":
		return uint64(sext(in.eval(e.a), uint8(e.val)))
	case "sel":
		c := in.eval(e.a)
		if c != 0 {
			return in.eval(e.b)
		}
		return in.eval(e.c)
	}
	a, b := in.eval(e.a), in.eval(e.b)
	var r uint64
	switch e.op {
	case "add":
		r = a + b
	case "sub":
		r = a - b
	case "mul":
		r = a * b
	case "and":
		r = a & b
	case "or":
		r = a | b
	case "xor":
		r = a ^ b
	default:
		panic("op " + e.op)
	}
	return mask(r, e.bits)
}

func compare(a, b uint64, bits uint8, c ir.BranchCondition) bool {
	sa, sb := sext(a, bits), sext(b, bits)
	switch c {
	case ir.BranchEqual:
		return a == b
	case ir.BranchNotEqual:
		return a != b
	case ir.BranchLess:
		return sa < sb
	case ir.BranchGreaterEqual:
		return sa >= sb
	case ir.BranchLessEqual:
		return sa <= sb
	case ir.BranchGreater:
		return sa > sb
	case ir.BranchBelow:
		return a < b
	case ir.BranchAboveEqual:
		return a >= b
	case ir.BranchBelowEqual:
		return a <= b
	case ir.BranchAbove:
		return a > b
	}
	panic("cond")
}

func (in *interp) pred(s *Stmt) bool {
	a := in.eval(s.pa)
	bits := s.pa.bits
	switch s.pk {
	case 0:
		return compare(a, in.eval(s.pb), bits, s.cond)
	case 1:
		return a == 0
	case 2:
		return compare(a, mask(uint64(s.imm), bits), bits, s.cond)
	default:
		return sext(a, bits) < 0
	}
}

func calleeFormula(args []uint64) uint64 {
	var r uint64 = 0x1234567
	for i, a := range args {
		r += a * []uint64{0x9E37, 0x85EB, 0xC2B2}[i]
	}
	return r
}

func mixStep(h, x uint64) uint64 {
	return bits.RotateLeft64((h^x)*0x9E3779B97F4A7C15, 13) + x
}

func (in *interp) hashScope(s *Scope) uint64 {
	h := in.vars[in.p.hash]
	for _, v := range s.vars {
		h = mixStep(h, in.vars[v])
	}
	return h
}

func (in *interp) declare(s *Scope) {
	for _, v := range s.vars {
		in.vars[v] = in.eval(v.init)
	}
}

func (in *interp) scope(s *Scope, tail func()) {
	in.declare(s)
	in.block(s.body)
	if tail != nil {
		tail()
	}

	in.temps[s.hashT] = in.hashScope(s)
}

func (in *interp) block(ss []*Stmt) {
	for _, s := range ss {
		in.stmt(s)
		if in.brk != nil {
			return
		}
	}
}

// broke reports whether the loop body was left by a break; it consumes a
// break aimed at s.
func (in *interp) broke(s *Stmt) bool {
	if in.brk == nil {
		return false
	}
	if in.brk == s {
		in.brk = nil
	}
	return true
}

func (in *interp) stmt(s *Stmt) {
	switch s.kind {
	case "brk":
		if in.pred(s) {
			in.brk = s.target
		}
	case "let":
		in.temps[s.t] = in.eval(s.e)
	case "set":
		in.vars[s.v] = in.eval(s.e)
	case "rot":
		vals := make([]uint64, len(s.vs))
		for i, v := range s.vs {
			vals[i] = in.vars[v]
		}
		for i, v := range s.vs {
			in.vars[v] = vals[(i+1)%len(vals)]
		}
	case "if":
		if in.pred(s) {
			in.block(s.then)
		} else {
			in.block(s.els)
		}
	case "loopv":
		switch s.form {
		case 0:
			in.vars[s.ctr] = s.n
			for in.vars[s.ctr] != 0 {
				in.block(s.body)
				if in.broke(s) {
					break
				}
				in.vars[s.ctr] = mask(in.vars[s.ctr]-1, s.ctr.bits)
			}
		case 1:
			in.vars[s.ctr] = 0
			for in.vars[s.ctr] < s.n {
				in.block(s.body)
				if in.broke(s) {
					break
				}
				in.vars[s.ctr] = mask(in.vars[s.ctr]+1, s.ctr.bits)
			}
		case 2:
			in.vars[s.ctr] = s.n
			for {
				in.block(s.body)
				if in.broke(s) {
					break
				}
				in.vars[s.ctr] = mask(in.vars[s.ctr]-1, s.ctr.bits)
				if in.vars[s.ctr] == 0 {
					break
				}
			}
		}
	case "loopp":
		k := s.n
		var x uint64
		if s.xbits != 0 {
			x = in.eval(s.xinit)
		}
		for {
			in.temps[s.kT] = k
			if s.xbits != 0 {
				in.temps[s.xT] = x
				if s.useX != nil {
					in.vars[s.useX] = mask(in.vars[s.useX]+x, s.useX.bits)
				}
			}
			if k == 0 {
				break
			}
			in.temps[s.k2T] = k
			if s.xbits != 0 {
				in.temps[s.x2T] = x
			}
			in.scope(s.scope, func() {
				in.temps[s.knT] = mask(k-1, s.kbits)
				if s.xbits != 0 {
					x2 := in.eval(s.xnext)
					in.temps[s.xnT] = x2
				}
			})
			k = in.temps[s.knT]
			if s.xbits != 0 {
				x = in.temps[s.xnT]
			}
			in.vars[in.p.hash] = mixStep(in.vars[in.p.hash], in.temps[s.scope.hashT])
		}
	case "scope":
		in.scope(s.scope, nil)
		in.vars[in.p.hash] = mixStep(in.vars[in.p.hash], in.temps[s.scope.hashT])
	case "call":
		args := make([]uint64, len(s.args))
		for i, a := range s.args {
			args[i] = in.eval(a)
		}
		in.temps[s.t] = calleeFormula(args)
		in.calls++
	}
}

// Run returns the expected result or a badProgram panic value.
func Interpret(p *Program, params [3]uint64) (res uint64, bad string) {
	defer func() {
		if r := recover(); r != nil {
			if b, ok := r.(badProgram); ok {
				bad = b.why
				return
			}
			panic(r)
		}
	}()
	in := &interp{vars: map[*VarD]uint64{}, temps: map[*Temp]uint64{}, p: p}

	for i, t := range p.params {
		in.temps[t] = mask(params[i], t.bits)
	}
	in.scope(p.root, nil)
	return in.hashScope(p.root), ""
}

// ---------------------------------------------------------------- emitter (+ Go source printer)

type Targets struct {
	goFn    [4]uintptr // CallGo targets by arity
	cbFn    [4]uintptr // RegisterForeignCallback by arity
	nativeC [4]uintptr // nyanjit-compiled C leaf by arity
}

type emitter struct {
	b     *n.Builder
	p     *Program
	vars  map[*VarD]n.Var
	temps map[*Temp]n.Value
	src   strings.Builder
	ind   int
	err   string
	tg    *Targets
	lbl   int
	exits map[*Stmt]n.Label
	exitN map[*Stmt]string
}

func (em *emitter) line(f string, a ...any) {
	em.src.WriteString(strings.Repeat("\t", em.ind))
	fmt.Fprintf(&em.src, f, a...)
	em.src.WriteByte('\n')
}

func vn(v *VarD) string { return fmt.Sprintf("v%d", v.id) }
func tn(t *Temp) string {
	if t.id < 0 {
		return "tRoot"
	}
	return fmt.Sprintf("t%d", t.id)
}
func (em *emitter) newLabel() (n.Label, string) {
	em.lbl++
	return em.b.NewLabel(), fmt.Sprintf("L%d", em.lbl)
}

func (em *emitter) expr(e *Expr) (n.Value, string) {
	b := em.b
	switch e.op {
	case "const":
		return b.ConstBits(e.bits, e.val), fmt.Sprintf("b.ConstBits(%d, %#x)", e.bits, e.val)
	case "fconst":
		return b.Float64(e.f), fmt.Sprintf("b.Float64(%v)", e.f)
	case "get":
		return b.Get(em.vars[e.v]), fmt.Sprintf("b.Get(%s)", vn(e.v))
	case "temp":
		v, ok := em.temps[e.t]
		if !ok {
			em.err = "undefined temp " + tn(e.t)
			return b.ConstBits(e.bits, 0), "MISSING"
		}
		return v, tn(e.t)
	case "rotl", "rotr", "shr":
		a, as := em.expr(e.a)
		switch e.op {
		case "rotl":
			return b.RotateLeft(a, uint8(e.val)), fmt.Sprintf("b.RotateLeft(%s, %d)", as, e.val)
		case "rotr":
			return b.RotateRight(a, uint8(e.val)), fmt.Sprintf("b.RotateRight(%s, %d)", as, e.val)
		}
		return b.ShiftRight(a, uint8(e.val)), fmt.Sprintf("b.ShiftRight(%s, %d)", as, e.val)
	case "cast":
		a, as := em.expr(e.a)
		return b.CastUnsigned(a, e.bits), fmt.Sprintf("b.CastUnsigned(%s, %d)", as, e.bits)
	case "sext":
		a, as := em.expr(e.a)
		return b.SignExtend(a, uint8(e.val)), fmt.Sprintf("b.SignExtend(%s, %d)", as, e.val)
	case "sel":
		c, cs := em.expr(e.a)
		x, xs := em.expr(e.b)
		y, ys := em.expr(e.c)
		return b.Select(c, x, y), fmt.Sprintf("b.Select(%s, %s, %s)", cs, xs, ys)
	}
	x, xs := em.expr(e.a)
	y, ys := em.expr(e.b)
	var f func(a, b n.Value) n.Value
	var name string
	switch e.op {
	case "add":
		f, name = b.Add, "Add"
	case "sub":
		f, name = b.Sub, "Sub"
	case "mul":
		f, name = b.Mul, "Mul"
	case "and":
		f, name = b.And, "And"
	case "or":
		f, name = b.Or, "Or"
	case "xor":
		f, name = b.Xor, "Xor"
	case "fadd":
		f, name = b.FAdd, "FAdd"
	case "fsub":
		f, name = b.FSub, "FSub"
	case "fmul":
		f, name = b.FMul, "FMul"
	}
	return f(x, y), fmt.Sprintf("b.%s(%s, %s)", name, xs, ys)
}

func (em *emitter) let(t *Temp, v n.Value, s string) {
	em.temps[t] = v
	em.line("%s := %s; _ = %s", tn(t), s, tn(t))
}

var condNames = []string{"n.BranchEqual", "n.BranchNotEqual", "n.BranchLess", "n.BranchGreaterEqual", "n.BranchLessEqual", "n.BranchGreater", "n.BranchBelow", "n.BranchAboveEqual", "n.BranchBelowEqual", "n.BranchAbove"}

func (em *emitter) pred(s *Stmt) (n.Predicate, string) {
	a, as := em.expr(s.pa)
	switch s.pk {
	case 0:
		c, cs := em.expr(s.pb)
		return em.b.Compare(a, s.cond, c), fmt.Sprintf("b.Compare(%s, %s, %s)", as, condNames[s.cond], cs)
	case 1:
		return em.b.IsZero(a), fmt.Sprintf("b.IsZero(%s)", as)
	case 2:
		return em.b.CompareImmediate(a, s.cond, s.imm), fmt.Sprintf("b.CompareImmediate(%s, %s, %d)", as, condNames[s.cond], s.imm)
	default:
		return em.b.IsNegative(a), fmt.Sprintf("b.IsNegative(%s)", as)
	}
}

func (em *emitter) mix(h n.Value, hs string, x n.Value, xs string) (n.Value, string) {
	b := em.b
	r := b.Add(b.RotateLeft(b.Mul(b.Xor(h, x), b.Const(0x9E3779B97F4A7C15)), 13), x)
	return r, fmt.Sprintf("mix(b, %s, %s)", hs, xs)
}

func (em *emitter) cast64(v *VarD) (n.Value, string) {
	g := em.b.Get(em.vars[v])
	if v.bits == 64 {
		return g, fmt.Sprintf("b.Get(%s)", vn(v))
	}
	return em.b.CastUnsigned(g, 64), fmt.Sprintf("b.CastUnsigned(b.Get(%s), 64)", vn(v))
}

func (em *emitter) hashScope(s *Scope) (n.Value, string) {
	h := em.b.Get(em.vars[em.p.hash])
	hs := fmt.Sprintf("b.Get(%s)", vn(em.p.hash))
	for _, v := range s.vars {
		x, xs := em.cast64(v)
		h, hs = em.mix(h, hs, x, xs)
	}
	return h, hs
}

func (em *emitter) declare(s *Scope) {
	for _, v := range s.vars {
		x, xs := em.expr(v.init)
		em.vars[v] = em.b.Var(x)
		em.line("%s := b.Var(%s); _ = %s", vn(v), xs, vn(v))
	}
	for _, v := range s.state {
		em.vars[v] = em.b.Var(em.b.ConstBits(v.bits, 0))
		em.line("%s := b.Var(b.ConstBits(%d, 0)) // loop state", vn(v), v.bits)
	}
}

func (em *emitter) block(ss []*Stmt) {
	for _, s := range ss {
		em.stmt(s)
	}
}

func (em *emitter) stmt(s *Stmt) {
	b := em.b
	switch s.kind {
	case "brk":
		xl, ok := em.exits[s.target]
		if !ok {
			em.err = "break without loop"
			return
		}
		p, ps := em.pred(s)
		b.Branch(p, xl)
		em.line("b.Branch(%s, %s) // break", ps, em.exitN[s.target])
	case "let":
		v, vs := em.expr(s.e)
		em.let(s.t, v, vs)
	case "set":
		v, vs := em.expr(s.e)
		b.Set(em.vars[s.v], v)
		em.line("b.Set(%s, %s)", vn(s.v), vs)
	case "rot":
		vals := make([]n.Value, len(s.vs))
		names := []string{}
		for i, v := range s.vs {
			vals[i] = b.Get(em.vars[v])
			names = append(names, fmt.Sprintf("r%d", i))
		}
		em.line("{ // rotate")
		for i, v := range s.vs {
			em.line("r%d := b.Get(%s)", i, vn(v))
		}
		for i, v := range s.vs {
			b.Set(em.vars[v], vals[(i+1)%len(vals)])
			em.line("b.Set(%s, %s)", vn(v), names[(i+1)%len(vals)])
		}
		em.line("}")
	case "if":
		p, ps := em.pred(s)
		tl, tls := em.newLabel()
		dl, dls := em.newLabel()
		em.line("%s, %s := b.NewLabel(), b.NewLabel()", tls, dls)
		if s.form == 0 {
			el, els := em.newLabel()
			em.line("%s := b.NewLabel()", els)
			b.Branch(p, tl)
			b.Jump(el)
			em.line("b.Branch(%s, %s); b.Jump(%s)", ps, tls, els)
			b.Bind(tl)
			em.line("b.Bind(%s)", tls)
			em.ind++
			em.block(s.then)
			em.ind--
			b.Jump(dl)
			b.Bind(el)
			em.line("b.Jump(%s)", dls)
			em.line("b.Bind(%s)", els)
			em.ind++
			em.block(s.els)
			em.ind--
			b.Jump(dl)
			b.Bind(dl)
			em.line("b.Jump(%s)", dls)
			em.line("b.Bind(%s)", dls)
		} else {
			b.Branch(p, tl)
			em.line("b.Branch(%s, %s) // false falls through", ps, tls)
			em.ind++
			em.block(s.els)
			em.ind--
			b.Jump(dl)
			b.Bind(tl)
			em.line("b.Jump(%s)", dls)
			em.line("b.Bind(%s)", tls)
			em.ind++
			em.block(s.then)
			em.ind--
			b.Jump(dl)
			b.Bind(dl)
			em.line("b.Jump(%s)", dls)
			em.line("b.Bind(%s)", dls)
		}
	case "loopv":
		c := em.vars[s.ctr]
		cb := s.ctr.bits
		cv := vn(s.ctr)
		ll, lls := em.newLabel()
		var xl n.Label
		var xls string
		if s.form != 2 {
			xl, xls = em.newLabel()
			em.exits[s], em.exitN[s] = xl, xls
			em.line("%s, %s := b.NewLabel(), b.NewLabel()", lls, xls)
		} else {
			em.line("%s := b.NewLabel()", lls)
		}
		switch s.form {
		case 0:
			b.Set(c, b.ConstBits(cb, s.n))
			b.Jump(ll)
			b.Bind(ll)
			b.Branch(b.IsZero(b.Get(c)), xl)
			em.line("b.Set(%s, b.ConstBits(%d, %d)); b.Jump(%s); b.Bind(%s)", cv, cb, s.n, lls, lls)
			em.line("b.Branch(b.IsZero(b.Get(%s)), %s)", cv, xls)
			em.ind++
			em.block(s.body)
			em.ind--
			b.Set(c, b.Sub(b.Get(c), b.ConstBits(cb, 1)))
			b.Jump(ll)
			b.Bind(xl)
			em.line("b.Set(%s, b.Sub(b.Get(%s), b.ConstBits(%d, 1))); b.Jump(%s); b.Bind(%s)", cv, cv, cb, lls, xls)
		case 1:
			b.Set(c, b.ConstBits(cb, 0))
			b.Jump(ll)
			b.Bind(ll)
			b.Branch(b.Compare(b.Get(c), ir.BranchAboveEqual, b.ConstBits(cb, s.n)), xl)
			em.line("b.Set(%s, b.ConstBits(%d, 0)); b.Jump(%s); b.Bind(%s)", cv, cb, lls, lls)
			em.line("b.Branch(b.Compare(b.Get(%s), n.BranchAboveEqual, b.ConstBits(%d, %d)), %s)", cv, cb, s.n, xls)
			em.ind++
			em.block(s.body)
			em.ind--
			b.Set(c, b.Add(b.Get(c), b.ConstBits(cb, 1)))
			b.Jump(ll)
			b.Bind(xl)
			em.line("b.Set(%s, b.Add(b.Get(%s), b.ConstBits(%d, 1))); b.Jump(%s); b.Bind(%s)", cv, cv, cb, lls, xls)
		case 2:
			b.Set(c, b.ConstBits(cb, s.n))
			b.Jump(ll)
			b.Bind(ll)
			em.line("b.Set(%s, b.ConstBits(%d, %d)); b.Jump(%s); b.Bind(%s)", cv, cb, s.n, lls, lls)
			em.ind++
			em.block(s.body)
			em.ind--
			b.Set(c, b.Sub(b.Get(c), b.ConstBits(cb, 1)))
			b.Branch(b.CompareImmediate(b.Get(c), ir.BranchNotEqual, 0), ll)
			em.line("b.Set(%s, b.Sub(b.Get(%s), b.ConstBits(%d, 1)))", cv, cv, cb)
			em.line("b.Branch(b.CompareImmediate(b.Get(%s), n.BranchNotEqual, 0), %s) // falls through", cv, lls)
		}
	case "loopp":
		ll, lls := em.newLabel()
		bl, bls := em.newLabel()
		xl, xls := em.newLabel()
		em.line("%s, %s, %s := b.NewLabel(), b.NewLabel(), b.NewLabel()", lls, bls, xls)
		kv := em.vars[s.kV]
		b.Set(kv, b.ConstBits(s.kbits, s.n))
		em.line("b.Set(%s, b.ConstBits(%d, %d))", vn(s.kV), s.kbits, s.n)
		if s.xbits != 0 {
			xi, xis := em.expr(s.xinit)
			b.Set(em.vars[s.xV], xi)
			em.line("b.Set(%s, %s)", vn(s.xV), xis)
		}
		b.Jump(ll)
		b.Bind(ll)
		em.line("b.Jump(%s); b.Bind(%s)", lls, lls)
		em.let(s.kT, b.Get(kv), fmt.Sprintf("b.Get(%s)", vn(s.kV)))
		if s.xbits != 0 {
			em.let(s.xT, b.Get(em.vars[s.xV]), fmt.Sprintf("b.Get(%s)", vn(s.xV)))
			if s.useX != nil {
				uv := em.vars[s.useX]
				b.Set(uv, b.Add(b.Get(uv), em.temps[s.xT]))
				em.line("b.Set(%s, b.Add(b.Get(%s), %s))", vn(s.useX), vn(s.useX), tn(s.xT))
			}
		}
		b.Branch(b.IsZero(em.temps[s.kT]), xl)
		b.Jump(bl)
		b.Bind(bl)
		em.line("b.Branch(b.IsZero(%s), %s); b.Jump(%s); b.Bind(%s)", tn(s.kT), xls, bls, bls)
		// The head snapshots dominate the body: it uses them directly.
		em.let(s.k2T, em.temps[s.kT], tn(s.kT))
		if s.xbits != 0 {
			em.let(s.x2T, em.temps[s.xT], tn(s.xT))
		}
		b.Enter()
		em.line("b.Enter()")
		em.ind++
		var exports []n.Value
		var exs []string
		em.scope(s.scope, func() {
			kn := b.Sub(em.temps[s.k2T], b.ConstBits(s.kbits, 1))
			exports = append(exports, kn)
			em.line("k%s := b.Sub(%s, b.ConstBits(%d, 1))", lls, tn(s.k2T), s.kbits)
			exs = append(exs, "k"+lls)
			if s.xbits != 0 {
				x, xs := em.expr(s.xnext)
				exports = append(exports, x)
				em.line("x%s := %s", lls, xs)
				exs = append(exs, "x"+lls)
			}
		})
		em.ind--
		h := em.temps[s.scope.hashT]
		b.Leave(append(exports, h)...)
		em.line("b.Leave(%s, %s)", strings.Join(exs, ", "), tn(s.scope.hashT))
		hv := em.vars[em.p.hash]
		mixed, _ := em.mix(b.Get(hv), "", h, "")
		b.Set(hv, mixed)
		em.line("b.Set(%s, mix(b, b.Get(%s), %s))", vn(em.p.hash), vn(em.p.hash), tn(s.scope.hashT))
		b.Set(kv, exports[0])
		em.line("b.Set(%s, %s)", vn(s.kV), exs[0])
		if s.xbits != 0 {
			b.Set(em.vars[s.xV], exports[1])
			em.line("b.Set(%s, %s)", vn(s.xV), exs[1])
		}
		b.Jump(ll)
		b.Bind(xl)
		em.line("b.Jump(%s); b.Bind(%s)", lls, xls)
	case "scope":
		b.Enter()
		em.line("b.Enter()")
		em.ind++
		em.scope(s.scope, nil)
		em.ind--
		h := em.temps[s.scope.hashT]
		b.Leave(h)
		em.line("b.Leave(%s)", tn(s.scope.hashT))
		hv := em.vars[em.p.hash]
		mixed, _ := em.mix(b.Get(hv), "", h, "")
		b.Set(hv, mixed)
		em.line("b.Set(%s, mix(b, b.Get(%s), %s))", vn(em.p.hash), vn(em.p.hash), tn(s.scope.hashT))
	case "call":
		args := make([]n.Value, len(s.args))
		as := []string{}
		for i, a := range s.args {
			var x string
			args[i], x = em.expr(a)
			as = append(as, x)
		}
		ar := len(args)
		var r n.Value
		var fn string
		switch s.ck {
		case 0:
			r = b.CallGo(b.ConstNative(em.tg.goFn[ar]), args...)
			fn = fmt.Sprintf("b.CallGo(b.ConstNative(goCallee%d)", ar)
		case 1:
			r = b.CallC(b.ConstNative(em.tg.cbFn[ar]), args...)
			fn = fmt.Sprintf("b.CallC(b.ConstNative(foreignCallback%d)", ar)
		case 2:
			r = b.CallC(b.ConstNative(em.tg.nativeC[ar]), args...)
			fn = fmt.Sprintf("b.CallC(b.ConstNative(nyanjitLeafC%d)", ar)
		}
		em.let(s.t, r, fn+", "+strings.Join(as, ", ")+")")
	}
}

func (em *emitter) scope(s *Scope, tail func()) {
	em.declare(s)
	em.block(s.body)
	if tail != nil {
		tail()
	}

	h, hs := em.hashScope(s)
	em.let(s.hashT, h, hs)
}

// Emit builds the program. src is a Go rendition.
func Emit(p *Program, tg *Targets) (*n.Builder, string, string) {
	em := &emitter{b: n.NewBuilder(), p: p, vars: map[*VarD]n.Var{}, temps: map[*Temp]n.Value{}, tg: tg, exits: map[*Stmt]n.Label{}, exitN: map[*Stmt]string{}}
	b := em.b
	em.temps[p.params[0]] = b.ParamBits(64)
	em.temps[p.params[1]] = b.ParamBits(32)
	em.temps[p.params[2]] = b.ParamNative()
	em.line("b := n.NewBuilder()")
	em.line("%s, %s, %s := b.ParamBits(64), b.ParamBits(32), b.ParamNative()", tn(p.params[0]), tn(p.params[1]), tn(p.params[2]))
	em.line("_, _, _ = %s, %s, %s", tn(p.params[0]), tn(p.params[1]), tn(p.params[2]))
	em.scope(p.root, nil)
	b.Return(em.temps[p.root.hashT])
	em.line("b.Return(%s)", tn(p.root.hashT))
	return b, em.src.String(), em.err
}
