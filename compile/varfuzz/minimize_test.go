//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || loong64 || 386)

package varfuzz

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func walkStmts(ss []*Stmt, slices *[]*[]*Stmt, exprs *[]**Expr, scopes *[]*Scope) {
	for _, s := range ss {
		addE := func(e **Expr) {
			if *e != nil {
				walkExpr(e, exprs)
			}
		}
		addE(&s.e)
		addE(&s.pa)
		addE(&s.pb)
		addE(&s.xinit)
		addE(&s.xnext)
		for i := range s.args {
			addE(&s.args[i])
		}
		switch s.kind {
		case "if":
			*slices = append(*slices, &s.then, &s.els)
			walkStmts(s.then, slices, exprs, scopes)
			walkStmts(s.els, slices, exprs, scopes)
		case "loopv":
			*slices = append(*slices, &s.body)
			walkStmts(s.body, slices, exprs, scopes)
		case "loopp", "scope":
			walkScope(s.scope, slices, exprs, scopes)
		}
	}
}

func walkScope(sc *Scope, slices *[]*[]*Stmt, exprs *[]**Expr, scopes *[]*Scope) {
	*scopes = append(*scopes, sc)
	for _, v := range sc.vars {
		walkExpr(&v.init, exprs)
	}
	*slices = append(*slices, &sc.body)
	walkStmts(sc.body, slices, exprs, scopes)
}

func walkExpr(e **Expr, exprs *[]**Expr) {
	*exprs = append(*exprs, e)
	x := *e
	if x.a != nil {
		walkExpr(&x.a, exprs)
	}
	if x.b != nil {
		walkExpr(&x.b, exprs)
	}
	if x.c != nil {
		walkExpr(&x.c, exprs)
	}
}

func collect(p *Program) ([]*[]*Stmt, []**Expr, []*Scope) {
	var sl []*[]*Stmt
	var ex []**Expr
	var sc []*Scope
	walkScope(p.root, &sl, &ex, &sc)
	return sl, ex, sc
}

func varUsed(p *Program, v *VarD) bool {
	_, exprs, _ := collect(p)
	for _, e := range exprs {
		if (*e).v == v {
			return true
		}
	}
	used := false
	var visit func(ss []*Stmt)
	visit = func(ss []*Stmt) {
		for _, s := range ss {
			if s.v == v || s.ctr == v || s.useX == v {
				used = true
			}
			for _, x := range s.vs {
				if x == v {
					used = true
				}
			}
			visit(s.then)
			visit(s.els)
			visit(s.body)
			if s.scope != nil {
				visit(s.scope.body)
			}
		}
	}
	visit(p.root.body)
	return used
}

// Edit is a deterministic AST reduction step.
type Edit struct {
	K       byte // 'r' remove/inline stmt, 'n' loop count, 'u' useX, 'e' drop else, 'o' rot3->2, 'v' var, 'x' expr
	A, B, C int
}

func (e Edit) String() string { return fmt.Sprintf("%c.%d.%d.%d", e.K, e.A, e.B, e.C) }

func parseEdits(s string) []Edit {
	var out []Edit
	for _, f := range strings.Split(s, ",") {
		if f == "" {
			continue
		}
		parts := strings.Split(f, ".")
		a, _ := strconv.Atoi(parts[1])
		b, _ := strconv.Atoi(parts[2])
		c, _ := strconv.Atoi(parts[3])
		out = append(out, Edit{parts[0][0], a, b, c})
	}
	return out
}

func editsString(es []Edit) string {
	var s []string
	for _, e := range es {
		s = append(s, e.String())
	}
	return strings.Join(s, ",")
}

// apply returns false if the edit does not apply.
func apply(p *Program, e Edit) bool {
	slices, exprs, scopes := collect(p)
	switch e.K {
	case 'r', 'n', 'u', 'e', 'o':
		if e.A >= len(slices) || e.B >= len(*slices[e.A]) {
			return false
		}
		sp := slices[e.A]
		old := *sp
		s := old[e.B]
		switch e.K {
		case 'r':
			var rep []*Stmt
			switch {
			case e.C == 0:
			case e.C == 1 && s.kind == "if":
				rep = s.then
			case e.C == 2 && s.kind == "if":
				rep = s.els
			case e.C == 1 && s.kind == "loopv":
				rep = s.body
			default:
				return false
			}
			*sp = append(append(append([]*Stmt{}, old[:e.B]...), rep...), old[e.B+1:]...)
		case 'n':
			if s.kind != "loopv" && s.kind != "loopp" || uint64(e.C) >= s.n || s.kind == "loopv" && s.form == 2 && e.C == 0 {
				return false
			}
			s.n = uint64(e.C)
		case 'u':
			if s.kind != "loopp" || s.useX == nil {
				return false
			}
			s.useX = nil
		case 'e':
			if s.kind != "if" || s.els == nil {
				return false
			}
			s.els = nil
		case 'o':
			if s.kind != "rot" || len(s.vs) != 3 {
				return false
			}
			s.vs = s.vs[:2]
		}
	case 'v':
		if e.A >= len(scopes) || e.B >= len(scopes[e.A].vars) {
			return false
		}
		sc := scopes[e.A]
		v := sc.vars[e.B]
		if v == p.hash || varUsed(p, v) {
			return false
		}
		sc.vars = append(append([]*VarD{}, sc.vars[:e.B]...), sc.vars[e.B+1:]...)
	case 'x':
		if e.A >= len(exprs) {
			return false
		}
		ep := exprs[e.A]
		x := *ep
		var r *Expr
		switch e.C {
		case 0, 1:
			if x.fp || x.op == "const" && x.val <= 1 {
				return false
			}
			r = &Expr{op: "const", bits: x.bits, val: uint64(e.C)}
		case 2:
			if !x.fp || x.op == "fconst" {
				return false
			}
			r = &Expr{op: "fconst", bits: 64, fp: true, f: 1}
		case 3, 4, 5:
			r = []*Expr{x.a, x.b, x.c}[e.C-3]
			if r == nil || r.bits != x.bits || r.fp != x.fp {
				return false
			}
		}
		*ep = r
	}
	return true
}

func candidates(p *Program) []Edit {
	slices, exprs, scopes := collect(p)
	var out []Edit
	for a, sp := range slices {
		for b, s := range *sp {
			out = append(out, Edit{'r', a, b, 0})
			if s.kind == "if" {
				out = append(out, Edit{'r', a, b, 1}, Edit{'r', a, b, 2}, Edit{'e', a, b, 0})
			}
			if s.kind == "loopv" {
				out = append(out, Edit{'r', a, b, 1})
			}
			if s.kind == "loopv" || s.kind == "loopp" {
				out = append(out, Edit{'n', a, b, 0}, Edit{'n', a, b, 1}, Edit{'n', a, b, 2})
			}
			if s.kind == "loopp" {
				out = append(out, Edit{'u', a, b, 0})
			}
			if s.kind == "rot" {
				out = append(out, Edit{'o', a, b, 0})
			}
		}
	}
	for a, sc := range scopes {
		for b := range sc.vars {
			out = append(out, Edit{'v', a, b, 0})
		}
	}
	for a := range exprs {
		for c := 0; c <= 5; c++ {
			out = append(out, Edit{'x', a, 0, c})
		}
	}
	return out
}

type minimizeCase struct {
	Data   []byte
	FP     bool
	Config string
	Params [3]uint64
	Edits  []Edit
}

func reducedBytes(data []byte, fp bool, edits []Edit) *Program {
	p := generateBytes(data, 0, fp, true)
	for _, e := range edits {
		apply(p, e)
	}
	return p
}

// candidateFailure runs generated code only in a subprocess. A signal or
// timeout is a failure; malformed AST reductions are discarded beforehand.
func candidateFailure(c minimizeCase) bool {
	p := reducedBytes(c.Data, c.FP, c.Edits)
	if _, bad := Interpret(p, c.Params); bad != "" {
		return false
	}
	if _, _, err := finish(p); err != nil {
		return false
	}
	file, err := os.CreateTemp("", "nyanjit-var-min-*.json")
	if err != nil {
		panic(err)
	}
	defer os.Remove(file.Name())
	if err := json.NewEncoder(file).Encode(c); err != nil {
		panic(err)
	}
	file.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	argv := []string{os.Args[0]}
	if launch := os.Getenv("NYANJIT_VAR_LAUNCH"); launch != "" {
		argv = strings.Fields(launch)
	}
	argv = append(argv, "-test.run=^TestScopedVarsMinimizeChild$", "-test.timeout=50s")
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "NYANJIT_VAR_MIN_CASE="+file.Name())
	_, err = cmd.CombinedOutput()
	return err != nil
}

func TestScopedVarsMinimizeChild(t *testing.T) {
	path := os.Getenv("NYANJIT_VAR_MIN_CASE")
	if path == "" {
		t.Skip("minimizer subprocess")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var c minimizeCase
	if err := json.NewDecoder(file).Decode(&c); err != nil {
		t.Fatal(err)
	}
	p := reducedBytes(c.Data, c.FP, c.Edits)
	for _, cfg := range configs(p) {
		if cfg.name == c.Config {
			if failed, why := failure(p, cfg, c.Params); failed {
				t.Fatal(why)
			}
			return
		}
	}
	if c.Config == "Foreign/C/Platform" && !p.hasCall {
		cfg := Config{name: c.Config, opt: compile.Func{ABI: abi.C, Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}, AssumePointerFree: true}}
		if failed, why := failure(p, cfg, c.Params); failed {
			t.Fatal(why)
		}
		return
	}
	t.Fatalf("unknown configuration %s", c.Config)
}

func minimizeBytes(data []byte, fp bool, cfg Config, params [3]uint64) []Edit {
	c := minimizeCase{Data: data, FP: fp, Config: cfg.name, Params: params}
	if !candidateFailure(c) {
		return nil
	}
	for changed := true; changed; {
		changed = false
		for i := 0; ; i++ {
			p := reducedBytes(data, fp, c.Edits)
			edits := candidates(p)
			if i >= len(edits) {
				break
			}
			e := edits[i]
			if !apply(p, e) {
				continue
			}
			trial := c
			trial.Edits = append(append([]Edit{}, c.Edits...), e)
			if candidateFailure(trial) {
				c = trial
				changed = true
				i--
			}
		}
	}
	return c.Edits
}
