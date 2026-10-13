package compile_test

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

type predicateFuzzBase struct {
	name string
	p    *ir.Program
}

func predicateFuzzBases(t *testing.T) []predicateFuzzBase {
	var out []predicateFuzzBase
	add := func(name string, b *n.Builder) {
		s, err := b.Finish()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out = append(out, predicateFuzzBase{name, s.Clone()})
	}
	{ // B0: 32-bit params, vars
		b := n.NewBuilder()
		x, y := b.ParamBits(32), b.ParamBits(32)
		vx, vy := b.Var(x), b.Var(y)
		yes, no := b.NewLabel(), b.NewLabel()
		b.Branch(b.IsZero(x), yes)
		b.Jump(no)
		b.Bind(yes)
		b.Return(b.Get(vx))
		b.Bind(no)
		b.Return(b.Get(vy))
		add("b0", b)
	}
	{ // B1: mixed widths, consts, float, vector, 128-bit in branch block
		b := n.NewBuilder()
		x, y, z := b.Param(), b.Param(), b.ParamBits(8)
		a := b.Add(x, y)
		c := b.Const(5)
		c8 := b.ConstBits(8, 3)
		f := b.FAdd(b.Float64(1.5), b.Float64(2))
		f32 := b.Float32(3)
		w := b.ParamBits(16)
		v := b.Splat(ir.Vec(2, 64), x)
		wide := b.Concat(x, y)
		_ = v
		_ = wide
		_, _, _, _ = c, c8, f, f32
		va := b.Var(a)
		vw := b.Var(b.CastUnsigned(w, 64))
		vc := b.Var(c)
		yes, no := b.NewLabel(), b.NewLabel()
		_ = z
		b.Branch(b.Compare(a, ir.BranchCondition(0), y), yes)
		b.Jump(no)
		b.Bind(yes)
		b.Return(b.Add(b.Get(va), b.Get(vw)))
		b.Bind(no)
		b.Return(b.Sub(b.Get(va), b.Get(vc)))
		add("b1", b)
	}
	{ // B2: loop with Var state, nested scope var, compare against const
		b := n.NewBuilder()
		x := b.Param()
		limit := b.Param()
		vl := b.Var(limit)
		head, body, exit := b.NewLabel(), b.NewLabel(), b.NewLabel()
		zero := b.Const(0)
		vi, vacc := b.Var(zero), b.Var(x)
		b.Bind(head)
		k := b.Const(10)
		one := b.Const(1)
		_ = one
		b.Branch(b.Compare(b.Get(vi), ir.BranchCondition(2), k), body)
		b.Jump(exit)
		b.Bind(body)
		b.Enter()
		v := b.Var(b.Get(vacc))
		b.Set(v, b.Add(b.Get(v), b.Get(vl)))
		nx := b.Get(v)
		b.Leave(nx)
		b.Set(vi, b.Add(b.Get(vi), b.Const(1)))
		b.Set(vacc, nx)
		b.Jump(head)
		b.Bind(exit)
		b.Return(b.Get(vacc))
		add("b2", b)
	}
	{ // B3: call in branch block (flags clobber) and float compare value
		b := n.NewBuilder()
		x, y := b.Param(), b.Param()
		r := b.CallC(b.ConstNative(0x1234), x)
		f := b.FMul(b.Float64(2), b.Float64(3))
		_ = f
		yes, no := b.NewLabel(), b.NewLabel()
		// r and y reach both targets by dominance.
		b.Branch(b.Compare(r, ir.BranchCondition(1), y), yes)
		b.Jump(no)
		b.Bind(yes)
		b.Return(r)
		b.Bind(no)
		b.Return(y)
		add("b3", b)
	}
	{ // Every non-value instruction is available to the random operand walker.
		b := n.NewBuilder()
		address, x := b.Param(), b.Param()
		b.Store64(address, x)
		b.Store8(address, x)
		b.VStore(address, b.Splat(ir.Vec(2, 64), x))
		b.Enter()
		v := b.Var(x)
		b.Set(v, x)
		b.Leave(b.Get(v))
		yes, no := b.NewLabel(), b.NewLabel()
		b.Branch(b.IsZero(x), yes)
		b.Jump(no)
		b.Bind(yes)
		b.Return(b.Const(1))
		b.Bind(no)
		b.Return(b.Const(2))
		add("nonvalues", b)
	}
	{ // Vector legalization must not read ignored jump/predicate operands.
		b := n.NewBuilder()
		address := b.ParamNative()
		b.VStore(address, b.VLoad(ir.Vec(4, 32), address))
		next := b.NewLabel()
		b.Jump(next)
		b.Bind(next)
		x := b.Const(3)
		yes, no := b.NewLabel(), b.NewLabel()
		b.Branch(b.IsZero(x), yes)
		b.Jump(no)
		b.Bind(yes)
		b.Return(b.Const(1))
		b.Bind(no)
		b.Return(b.Const(2))
		add("vector-jump", b)
	}

	return out
}

type predicateFuzzGen struct {
	r     *rand.Rand
	vals  []int
	nodes []*ir.PredicateExpr
}

func (g *predicateFuzzGen) val() int {
	switch g.r.Intn(20) {
	case 0:
		return -1
	case 1:
		return 1 << 20
	}
	return g.vals[g.r.Intn(len(g.vals))]
}

// node builds a random predicate; bad controls the probability of malformed slots.
func (g *predicateFuzzGen) node(depth int, bad int) *ir.PredicateExpr {
	if len(g.nodes) > 0 && g.r.Intn(8) == 0 {
		return g.nodes[g.r.Intn(len(g.nodes))] // DAG sharing (possibly cycle if ancestor)
	}
	nd := &ir.PredicateExpr{}
	k := g.r.Intn(10)
	switch {
	case depth <= 0 || k < 4:
		nd.Kind = ir.PredicateCompare
	case k < 6:
		nd.Kind = ir.PredicateAnd
	case k < 8:
		nd.Kind = ir.PredicateOr
	default:
		nd.Kind = ir.PredicateNot
	}
	if g.r.Intn(bad+60) < bad {
		nd.Kind = ir.PredicateKind(4 + g.r.Intn(250))
	}
	// fill every scalar field randomly regardless of kind
	nd.Condition = ir.BranchCondition(g.r.Intn(int(ir.BranchAbove) + 1))
	if g.r.Intn(bad+60) < bad {
		nd.Condition = ir.BranchCondition(int(ir.BranchAbove) + 1 + g.r.Intn(3))
	}
	nd.Value, nd.Other, nd.Anchor = g.val(), g.val(), g.val()
	imms := []int64{0, 1, -1, 127, 128, 255, 1 << 31, -1 << 31, 1<<32 - 1, 1 << 40, -1 << 63, 1<<63 - 1}
	nd.Immediate = imms[g.r.Intn(len(imms))]
	nd.WithValue = g.r.Intn(2) == 0
	nd.HasAnchor = g.r.Intn(4) == 0
	g.nodes = append(g.nodes, nd)
	child := func(need bool) *ir.PredicateExpr {
		if need {
			if g.r.Intn(bad+60) < bad {
				return nil
			}
			return g.node(depth-1, bad)
		}
		if g.r.Intn(bad+60) < bad {
			return g.node(depth-1, bad) // stray child
		}
		return nil
	}
	switch nd.Kind {
	case ir.PredicateCompare:
		nd.Left, nd.Right = child(false), child(false)
	case ir.PredicateNot:
		nd.Left, nd.Right = child(true), child(false)
	case ir.PredicateAnd, ir.PredicateOr:
		nd.Left, nd.Right = child(true), child(true)
	default:
		nd.Left, nd.Right = child(g.r.Intn(2) == 0), child(g.r.Intn(2) == 0)
	}
	return nd
}

var predicateFuzzArchs = []string{"amd64", "386", "arm64", "riscv64", "loong64", "ppc64le"}

func predicateFuzzIsValidationErr(err error) bool {
	return errors.Is(err, compile.ErrInvalidFunction) || errors.Is(err, ir.ErrInvalidProgram) || errors.Is(err, ir.ErrPredicateLimit)
}

func TestPredicateMutationFuzz(t *testing.T) {
	iters, _ := strconv.Atoi(os.Getenv("NYANJIT_PREDICATE_ITERS"))
	if iters == 0 {
		iters = 200
	}
	seed, _ := strconv.ParseInt(os.Getenv("NYANJIT_PREDICATE_SEED"), 10, 64)
	bases := predicateFuzzBases(t)
	type branchInfo struct {
		step int
		vals []int
	}
	infos := make([][]branchInfo, len(bases))
	for bi, b := range bases {
		for si, st := range b.p.Control.Steps {
			if st.Term == ir.TermBranch && st.Predicate != nil {
				var vals []int
				for id := st.Start; id < st.End; id++ {
					if os.Getenv("NYANJIT_PREDICATE_NOSET") != "" && b.p.Instructions[id].Op == ir.OpSet {
						continue
					}
					vals = append(vals, id)
				}
				infos[bi] = append(infos[bi], branchInfo{si, vals})
			}
		}
		if len(infos[bi]) == 0 {
			t.Fatalf("base %s has no branch", b.name)
		}
	}
	r := rand.New(rand.NewSource(seed))
	stats := map[string]int{}
	var failures []string
	fail := func(s string) {
		if len(failures) < 50 {
			failures = append(failures, s)
		}
		stats["FAIL"]++
	}
	valid, invalid := 0, 0
	var out *os.File
	if path := os.Getenv("NYANJIT_PREDICATE_OUT"); path != "" {
		out, _ = os.Create(path)
		defer out.Close()
	}
	var line strings.Builder
	for it := 0; it < iters; it++ {
		bi := r.Intn(len(bases))
		info := infos[bi][r.Intn(len(infos[bi]))]
		p := bases[bi].p.Clone()
		bad := []int{0, 0, 3, 10}[r.Intn(4)]
		g := &predicateFuzzGen{r: r, vals: info.vals}
		root := g.node(r.Intn(4), bad)
		p.Control.Steps[info.step].Predicate = root
		if os.Getenv("NYANJIT_PREDICATE_NOSYNC") == "" && r.Intn(2) == 0 {
			p.Control.Steps[info.step].Values = predicateFuzzRefs(root)
		}
		switch r.Intn(12) {
		case 0:
			p.Result = g.val()
		case 1:
			p.ResultParts = []int{g.val(), g.val()}
		case 2:
			for si, step := range p.Control.Steps {
				if !step.IsLabel {
					p.Control.Steps[si].Arity = r.Intn(6) - 1
					break
				}
			}
		case 3:
			for i := range p.Instructions {
				if p.Instructions[i].Op == ir.OpParam {
					p.Instructions[i].Op = ir.OpConst
					p.Arity--
					break
				}
			}
		case 4, 5:
			for si, step := range p.Control.Steps {
				if !step.IsLabel && step.Term == ir.TermReturn {
					if r.Intn(2) == 0 {
						p.Control.Steps[si].Result = g.val()
					} else {
						p.Control.Steps[si].ResultParts = []int{step.Result, g.val()}
					}
					break
				}
			}
		case 6, 7:
			if len(p.ScopeExits) > 0 {
				i := r.Intn(len(p.ScopeExits))
				if r.Intn(2) == 0 {
					p.ScopeExits[i].Exports = append(p.ScopeExits[i].Exports, g.val())
				} else {
					p.ScopeExits[i].Hidden = append(p.ScopeExits[i].Hidden, g.val())
				}
			}
		case 8, 9:
			for si, step := range p.Control.Steps {
				if !step.IsLabel && step.Term == ir.TermJump {
					p.Control.Steps[si].Result = g.val()
					break
				}
			}
		case 10:
			for si, step := range p.Control.Steps {
				if !step.IsLabel && step.Term != ir.TermBranch {
					p.Control.Steps[si].Predicate = &ir.PredicateExpr{Kind: ir.PredicateCompare, Value: g.val(), WithValue: true, Other: g.val(), HasAnchor: true, Anchor: g.val()}
					break
				}
			}
		}
		desc := fmt.Sprintf("seed=%d it=%d base=%s step=%d", seed, it, bases[bi].name, info.step)
		safe := func(entry string, f func() error) (err error) {
			defer func() {
				if v := recover(); v != nil {
					fail(desc + fmt.Sprintf(" %s panicked: %v", entry, v))
					err = fmt.Errorf("panic in %s: %v", entry, v)
				}
			}()
			return f()
		}
		verr := safe("Validate", p.Validate)
		line.Reset()
		fmt.Fprintf(&line, "%d validate=%v", it, verr)
		serr := safe("Seal", func() error { _, err := ir.Seal(p); return err })
		if (verr == nil) != (serr == nil) {
			fail(desc + fmt.Sprintf(": Validate %v vs Seal %v", verr, serr))
		}
		if verr != nil {
			invalid++
			msg := verr.Error()
			stats["vmsg:"+msg]++
			if !errors.Is(verr, ir.ErrInvalidProgram) && !errors.Is(verr, ir.ErrPredicateLimit) {
				fail(desc + ": validate error identity " + msg)
			}
		} else {
			valid++
		}
		for _, arch := range predicateFuzzArchs {
			spec := compile.TargetSpec{Architecture: arch}
			if r.Intn(4) == 0 {
				spec.Vectors = compile.VectorsOff()
			}
			var opts compile.Func
			if r.Intn(4) == 0 {
				opts.PreferCompactLoops = true
			}
			if r.Intn(2) == 0 {
				opts.Stack = compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 16}
				opts.AssumePointerFree = true
			}
			// External lowering performs no callable checks, like the report
			// and listing paths; the other contexts add them.
			external, foreign, managed := opts, opts, opts
			external.Context = compile.ContextExternal
			foreign.Context = compile.ContextForeign
			managed.Context = compile.ContextGoManaged
			e1 := safe(arch+"/Listing", func() error { _, err := lowerListing(p, spec, external); return err })
			var u *compile.Unit
			e2 := safe(arch+"/Lower", func() error { var err error; u, err = lowerUnit(p, spec, external); return err })
			var e3 error
			if e2 == nil {
				e3 = safe(arch+"/Unit.Listing", func() error { _, err := u.Listing(); return err })
			}
			check := func(entry string, err error) {
				fmt.Fprintf(&line, " %s/%s=%v", arch, entry, err)
				key := fmt.Sprintf("%s/%s", arch, entry)
				if verr == nil {
					if err != nil {
						stats["accepted-err:"+key+":"+err.Error()]++
						if predicateFuzzIsValidationErr(err) {
							fail(desc + fmt.Sprintf(" %s: validated predicate rejected as invalid: %v\n%s", key, err, predicateFuzzDump(root, p)))
						}
					}
				} else if err == nil {
					fail(desc + fmt.Sprintf(" %s: invalid predicate compiled (validate: %v)", key, verr))
				} else if !predicateFuzzIsValidationErr(err) {
					fail(desc + fmt.Sprintf(" %s: invalid predicate wrong error %v", key, err))
				}
			}
			for _, entry := range []struct {
				name string
				call func() error
			}{
				{"Template", func() error { _, err := lowerTemplate(p, spec, opts); return err }},
				{"Report", func() error { _, err := lowerReport(p, spec, external); return err }},
				{"NativeReport", func() error { _, err := lowerReport(p, spec, opts); return err }},
				{"ExternalTemplate", func() error { _, err := lowerTemplate(p, spec, external); return err }},
			} {
				check(entry.name, safe(arch+"/"+entry.name, entry.call))
			}
			check("Listing", e1)
			check("Lower", e2)
			if e2 == nil {
				check("Unit.Listing", e3)
			}
			if (e1 == nil) != (e2 == nil && e3 == nil) {
				fail(desc + fmt.Sprintf(" %s: Listing %v vs Lower %v/%v", arch, e1, e2, e3))
			}
			if arch == runtime.GOARCH {
				_, a := lowerReport(p, spec, external)
				_, pf := lowerReport(p, spec, opts)
				_, pr := lowerTemplate(p, spec, opts)
				_, pg := lowerTemplate(p, spec, managed)
				_, pfo := lowerTemplate(p, spec, foreign)
				ps := func() error {
					u, err := lowerUnit(p, spec, external)
					if err == nil {
						_, err = u.RegisterScopes()
					}
					return err
				}()
				check("Report", a)
				check("NativeReport", pf)
				check("Template", pr)
				check("ManagedTemplate", pg)
				check("ForeignTemplate", pfo)
				check("RegisterScopes", ps)
			}
		}
		if out != nil {
			fmt.Fprintln(out, line.String())
		}
	}
	keys := make([]string, 0, len(stats))
	_ = 0
	for k := range stats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	fmt.Fprintf(&sb, "iters=%d valid=%d invalid=%d\n", iters, valid, invalid)
	for _, k := range keys {
		fmt.Fprintf(&sb, "%8d %s\n", stats[k], k)
	}
	t.Log(sb.String())
	for _, f := range failures {
		t.Error(f)
	}
}

func predicateFuzzDump(root *ir.PredicateExpr, p *ir.Program) string {
	ids := map[*ir.PredicateExpr]int{}
	var sb strings.Builder
	var walk func(n *ir.PredicateExpr, ind string)
	walk = func(nd *ir.PredicateExpr, ind string) {
		if nd == nil {
			fmt.Fprintf(&sb, "%snil\n", ind)
			return
		}
		if id, ok := ids[nd]; ok {
			fmt.Fprintf(&sb, "%s#%d (shared)\n", ind, id)
			return
		}
		ids[nd] = len(ids)
		ty := func(v int) string {
			if v >= 0 && v < len(p.Instructions) {
				return fmt.Sprintf("%v/op%d", p.Instructions[v].ValueType(), p.Instructions[v].Op)
			}
			return "?"
		}
		fmt.Fprintf(&sb, "%s#%d kind=%d cond=%d val=%d(%s) other=%d(%s) withV=%v anchor=%d(%s) hasA=%v imm=%d\n", ind, ids[nd], nd.Kind, nd.Condition, nd.Value, ty(nd.Value), nd.Other, ty(nd.Other), nd.WithValue, nd.Anchor, ty(nd.Anchor), nd.HasAnchor, nd.Immediate)
		walk(nd.Left, ind+"  L ")
		walk(nd.Right, ind+"  R ")
	}
	walk(root, "")
	return sb.String()
}

func predicateFuzzRefs(root *ir.PredicateExpr) []int {
	seen := map[*ir.PredicateExpr]bool{}
	ids := map[int]bool{}
	var out []int
	add := func(id int) {
		if !ids[id] {
			ids[id] = true
			out = append(out, id)
		}
	}
	work := []*ir.PredicateExpr{root}
	for len(work) != 0 {
		nd := work[len(work)-1]
		work = work[:len(work)-1]
		if nd == nil || seen[nd] {
			continue
		}
		seen[nd] = true
		if nd.Kind == ir.PredicateCompare {
			if nd.HasAnchor {
				add(nd.Anchor)
			}
			add(nd.Value)
			if nd.WithValue {
				add(nd.Other)
			}
			continue
		}
		work = append(work, nd.Right, nd.Left)
	}
	return out
}
