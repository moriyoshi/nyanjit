//go:build linux && arm64

package varfuzz

// Round-9 field mutations (reviewer-owned, identical in pre and post):
// Program.Result/ResultParts, control step Result/ResultParts, ScopeExit
// Hidden/Exports, Step.Arity, OpParam placement. Every entry point is called
// under recover. ZZM9_OUT=file ZZM9_GEN=g ZZM9_FROM=a ZZM9_TO=b

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func zzErrClass(err error) string {
	switch {
	case err == nil:
		return "OK"
	case strings.HasPrefix(err.Error(), "PANIC"):
		return "PANIC"
	case strings.Contains(err.Error(), "no materializable source"):
		return "NOMAT"
	case errors.Is(err, ir.ErrInvalidProgram):
		return "EPROG"
	case errors.Is(err, compile.ErrInvalidFunction):
		return "EFUNC"
	case errors.Is(err, compile.ErrBackendLimit) || errors.Is(err, abi.ErrBackendLimit):
		return "ELIMIT"
	}
	return "EOTHER"
}

func zzTry(f func() (string, error)) (s string, err error) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			nn := runtime.Stack(buf, false)
			err = fmt.Errorf("PANIC %v %s", r, zzFirst(string(buf[:nn])))
		}
	}()
	return f()
}

type zzEntryRes struct {
	name string
	hash string
	err  error
}

// zzEntries runs every entry point on clones of p.
func zzEntries(p *ir.Program, full bool) (validate error, res []zzEntryRes) {
	_, validate = zzTry(func() (string, error) { return "", p.Clone().Validate() })
	_, serr := zzTry(func() (string, error) { _, e := ir.Seal(p.Clone()); return "", e })
	res = append(res, zzEntryRes{"seal", "", serr})
	h := func(s string) string { d := sha256.Sum256([]byte(s)); return hex.EncodeToString(d[:6]) }
	cfgs := []zzCfg{}
	for _, arch := range []string{"amd64", "arm64", "386", "riscv64", "ppc64le", "loong64"} {
		cs := zzConfigs(arch, false)
		cfgs = append(cfgs, zzCfg{arch + "/" + cs[0].name, cs[0].opt}, zzCfg{arch + "/" + cs[3].name, cs[3].opt})
	}
	for ci, c := range cfgs {
		c := c
		l, err := zzTry(func() (string, error) { return zzListing(p.Clone(), c.opt) })
		res = append(res, zzEntryRes{"asm:" + c.name, h(l), err})
		if !full && ci > 3 {
			continue
		}
		l, err = zzTry(func() (string, error) {
			u, e := zzUnit(p.Clone(), c.opt, c.opt.fn.Context)
			if e != nil {
				return "", e
			}
			return u.Listing()
		})
		res = append(res, zzEntryRes{"lower:" + c.name, h(l), err})
		_, err = zzTry(func() (string, error) { r, e := zzReport(p.Clone(), c.opt); return fmt.Sprint(r.CodeBytes), e })
		res = append(res, zzEntryRes{"analyze:" + c.name, "", err})
		_, err = zzTry(func() (string, error) { _, e := zzReport(p.Clone(), c.opt); return "", e })
		res = append(res, zzEntryRes{"preflight:" + c.name, "", err})
		_, err = zzTry(func() (string, error) { _, e := zzTemplate(p.Clone(), c.opt, c.opt.fn.Context); return "", e })
		res = append(res, zzEntryRes{"template:" + c.name, "", err})
		_, err = zzTry(func() (string, error) {
			u, e := zzUnit(p.Clone(), c.opt, c.opt.fn.Context)
			if e != nil {
				return "", e
			}
			_, e = u.RegisterScopes()
			return "", e
		})
		res = append(res, zzEntryRes{"regscopes:" + c.name, "", err})
		if strings.HasPrefix(c.name, "arm64/") {
			_, err = zzTry(func() (string, error) { _, e := zzTemplate(p.Clone(), c.opt, compile.ContextNative); return "", e })
			res = append(res, zzEntryRes{"prepare:" + c.name, "", err})
			_, err = zzTry(func() (string, error) { _, _, e := zzEmit(p.Clone(), c.opt); return "", e })
			res = append(res, zzEntryRes{"emit:" + c.name, "", err})
		}
	}
	return
}

func zzNV(op ir.Opcode) bool {
	switch op {
	case ir.OpSet, ir.OpScopeEnter, ir.OpScopeLeave, ir.OpStore64, ir.OpStore8, ir.OpVStore:
		return true
	}
	return false
}

type zzM9 struct {
	name string
	noop bool // semantic no-op: must not be rejected and listings must match base
	fn   func(p *ir.Program) bool
}

func zzM9Muts(in *ir.Program) []zzM9 {
	ins := in.Instructions
	firstNV := func(a, b int) int {
		for id := a; id < b; id++ {
			if zzNV(ins[id].Op) {
				return id
			}
		}
		return -1
	}
	firstV := func(a, b int, not int) int {
		for id := a; id < b; id++ {
			if !zzNV(ins[id].Op) && id != not && ins[id].Op != ir.OpParam {
				return id
			}
		}
		return -1
	}
	var ms []zzM9
	add := func(name string, noop bool, fn func(p *ir.Program) bool) { ms = append(ms, zzM9{name, noop, fn}) }
	add("none", true, func(p *ir.Program) bool { return true })
	nvAll := firstNV(0, len(ins))
	// Program.Result / ResultParts.
	add("presult-nonvalue", false, func(p *ir.Program) bool {
		if nvAll < 0 {
			return false
		}
		p.Result = nvAll
		return true
	})
	add("presult-oob", false, func(p *ir.Program) bool { p.Result = len(p.Instructions); return true })
	add("presult-neg", false, func(p *ir.Program) bool { p.Result = -1; return true })
	add("presultparts-nonvalue", false, func(p *ir.Program) bool {
		if nvAll < 0 {
			return false
		}
		p.ResultParts = []int{p.Result, nvAll}
		return true
	})
	add("presultparts-bad0", false, func(p *ir.Program) bool {
		if nvAll < 0 {
			return false
		}
		p.ResultParts = []int{nvAll, p.Result}
		return true
	})
	add("presultparts-one", false, func(p *ir.Program) bool { p.ResultParts = []int{p.Result}; return true })
	add("parity+1", false, func(p *ir.Program) bool { p.Arity++; return true })
	add("parity-1", false, func(p *ir.Program) bool {
		if p.Arity == 0 {
			return false
		}
		p.Arity--
		return true
	})
	// ScopeExits.
	for ei := range in.ScopeExits {
		if ei >= 4 {
			break
		}
		ei := ei
		ex := in.ScopeExits[ei]
		nv := firstNV(0, ex.Position)
		v := firstV(0, ex.Position, -1)
		tag := fmt.Sprintf("e%d-", ei)
		add(tag+"hidden-addnonvalue", false, func(p *ir.Program) bool {
			if nv < 0 {
				return false
			}
			p.ScopeExits[ei].Hidden = append(append([]int(nil), p.ScopeExits[ei].Hidden...), nv)
			return true
		})
		add(tag+"hidden-oob", false, func(p *ir.Program) bool {
			p.ScopeExits[ei].Hidden = append(append([]int(nil), p.ScopeExits[ei].Hidden...), ex.Position)
			return true
		})
		add(tag+"hidden-dup", false, func(p *ir.Program) bool {
			if len(ex.Hidden) == 0 {
				return false
			}
			p.ScopeExits[ei].Hidden = append(append([]int(nil), p.ScopeExits[ei].Hidden...), ex.Hidden[0])
			return true
		})
		add(tag+"hidden-reverse", false, func(p *ir.Program) bool {
			if len(ex.Hidden) < 2 {
				return false
			}
			h := append([]int(nil), ex.Hidden...)
			for i, j := 0, len(h)-1; i < j; i, j = i+1, j-1 {
				h[i], h[j] = h[j], h[i]
			}
			p.ScopeExits[ei].Hidden = h
			return true
		})
		add(tag+"hidden-dropnonvalue", true, func(p *ir.Program) bool {
			h := []int{}
			changed := false
			for _, id := range ex.Hidden {
				if id >= 0 && id < len(ins) && zzNV(ins[id].Op) {
					changed = true
					continue
				}
				h = append(h, id)
			}
			p.ScopeExits[ei].Hidden = h
			return changed
		})
		add(tag+"hidden-droplast", false, func(p *ir.Program) bool {
			if len(ex.Hidden) == 0 {
				return false
			}
			p.ScopeExits[ei].Hidden = append([]int(nil), ex.Hidden[:len(ex.Hidden)-1]...)
			return true
		})
		add(tag+"hidden-addvalue", false, func(p *ir.Program) bool {
			if v < 0 {
				return false
			}
			p.ScopeExits[ei].Hidden = append(append([]int(nil), p.ScopeExits[ei].Hidden...), v)
			return true
		})
		add(tag+"exports-nonvalue", false, func(p *ir.Program) bool {
			if nv < 0 || len(ex.Exports) == 0 {
				return false
			}
			e := append([]int(nil), ex.Exports...)
			e[0] = nv
			p.ScopeExits[ei].Exports = e
			return true
		})
		add(tag+"exports-addnonvalue", false, func(p *ir.Program) bool {
			if nv < 0 {
				return false
			}
			p.ScopeExits[ei].Exports = append(append([]int(nil), ex.Exports...), nv)
			return true
		})
		add(tag+"exports-earlyvalue", false, func(p *ir.Program) bool {
			if v < 0 || len(ex.Exports) == 0 {
				return false
			}
			e := append([]int(nil), ex.Exports...)
			e[0] = v
			p.ScopeExits[ei].Exports = e
			return true
		})
		add(tag+"exports-oob", false, func(p *ir.Program) bool {
			if len(ex.Exports) == 0 {
				return false
			}
			e := append([]int(nil), ex.Exports...)
			e[0] = ex.Position
			p.ScopeExits[ei].Exports = e
			return true
		})
	}
	if in.Control == nil {
		return ms
	}
	// Control steps.
	entry := true
	nsteps := 0
	for si, st := range in.Control.Steps {
		if st.IsLabel {
			continue
		}
		isEntry := entry
		entry = false
		if nsteps >= 6 {
			break
		}
		nsteps++
		si, st := si, st
		tag := fmt.Sprintf("s%d-", si)
		nv := firstNV(st.Start, st.End)
		v := firstV(st.Start, st.End, st.Result)
		outside := -1
		if st.Start > 0 {
			outside = firstV(0, st.Start, -1)
		}
		switch st.Term {
		case ir.TermJump:
			add(tag+"jumpresult-nonvalue", true, func(p *ir.Program) bool {
				if nv < 0 {
					return false
				}
				p.Control.Steps[si].Result = nv
				return true
			})
			add(tag+"jumpresult-outside", true, func(p *ir.Program) bool {
				if outside < 0 {
					return false
				}
				p.Control.Steps[si].Result = outside
				return true
			})
		default:
			kind := "ret"
			if st.Term == ir.TermBranch {
				kind = "br"
			}
			add(tag+kind+"result-nonvalue", false, func(p *ir.Program) bool {
				if nv < 0 {
					return false
				}
				p.Control.Steps[si].Result = nv
				return true
			})
			add(tag+kind+"result-outside", false, func(p *ir.Program) bool {
				if outside < 0 {
					return false
				}
				p.Control.Steps[si].Result = outside
				return true
			})
			add(tag+kind+"result-othervalue", false, func(p *ir.Program) bool {
				if v < 0 {
					return false
				}
				p.Control.Steps[si].Result = v
				return true
			})
			add(tag+kind+"resultparts-nonvalue", false, func(p *ir.Program) bool {
				if nv < 0 {
					return false
				}
				p.Control.Steps[si].ResultParts = []int{st.Result, nv}
				return true
			})
			add(tag+kind+"resultparts-outside", false, func(p *ir.Program) bool {
				if outside < 0 {
					return false
				}
				p.Control.Steps[si].ResultParts = []int{st.Result, outside}
				return true
			})
		}
		add(tag+"arity+1", false, func(p *ir.Program) bool { p.Control.Steps[si].Arity++; return true })
		add(tag+"arity-1", false, func(p *ir.Program) bool {
			if st.Arity == 0 {
				return false
			}
			p.Control.Steps[si].Arity--
			return true
		})
		if !isEntry {
			// Turn a later constant into an OpParam (misplaced parameter).
			add(tag+"param-misplaced", false, func(p *ir.Program) bool {
				for id := st.Start; id < st.End; id++ {
					if p.Instructions[id].Op == ir.OpConst && p.Instructions[id].Bits == 64 {
						p.Instructions[id] = ir.Instruction{Op: ir.OpParam, Bits: 64}
						return true
					}
				}
				return false
			})
			add(tag+"param-misplaced-arity", false, func(p *ir.Program) bool {
				for id := st.Start; id < st.End; id++ {
					if p.Instructions[id].Op == ir.OpConst && p.Instructions[id].Bits == 64 {
						p.Instructions[id] = ir.Instruction{Op: ir.OpParam, Bits: 64, Imm: uint64(in.Arity)}
						p.Control.Steps[si].Arity++
						p.Arity++
						return true
					}
				}
				return false
			})
		}
	}
	return ms
}

func TestZZMut9(t *testing.T) {
	out := os.Getenv("ZZM9_OUT")
	if out == "" {
		t.Skip()
	}
	gen := os.Getenv("ZZM9_GEN")
	from, to := zzEnv("ZZM9_FROM", 0), zzEnv("ZZM9_TO", 10)
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()
	type res9 struct {
		seed  int64
		lines []string
	}
	seeds := make(chan int64)
	results := make(chan res9, 64)
	var wg sync.WaitGroup
	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for seed := range seeds {
				results <- res9{seed, zzM9One(gen, seed)}
			}
		}()
	}
	go func() {
		for s := from; s < to; s++ {
			seeds <- s
		}
		close(seeds)
		wg.Wait()
		close(results)
	}()
	all := []res9{}
	for r := range results {
		all = append(all, r)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].seed < all[j].seed })
	stats := map[string]int{}
	for _, r := range all {
		for _, l := range r.lines {
			fmt.Fprintln(w, l)
			stats[strings.Fields(l)[0]]++
		}
	}
	t.Logf("mutations: %v", stats)
}

func zzM9One(gen string, seed int64) (lines []string) {
	b, err := zzBuild(gen, seed)
	if err != nil {
		return nil
	}
	sealed, err := b.Finish()
	if err != nil {
		return nil
	}
	in := sealed.Clone()
	base := map[string]zzEntryRes{}
	for _, m := range zzM9Muts(in) {
		p := in.Clone()
		ok := true
		func() {
			defer func() {
				if r := recover(); r != nil {
					ok = false
				}
			}()
			ok = m.fn(p)
		}()
		if !ok {
			continue
		}
		full := m.name == "none" || seed%4 == 0
		verr, res := zzEntries(p, full)
		if m.name == "none" {
			for _, r := range res {
				base[r.name] = r
			}
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "%s %d %s noop=%v validate=%s", gen, seed, m.name, m.noop, zzErrClass(verr))
		flags := []string{}
		for _, r := range res {
			c := zzErrClass(r.err)
			if c == "PANIC" {
				flags = append(flags, "PANIC:"+r.name+":"+zzFirst(r.err.Error()))
			}
			if verr == nil && (c == "NOMAT" || c == "EFUNC") {
				flags = append(flags, "VALIDTHEN"+c+":"+r.name+":"+zzFirst(r.err.Error()))
			}
			if bb, have := base[r.name]; m.noop && have {
				if c != zzErrClass(bb.err) || (c == "OK" && r.hash != bb.hash) {
					flags = append(flags, "NOOPDIFF:"+r.name+":"+zzErrClass(bb.err)+"->"+c)
				}
			}
			if r.name != "seal" && m.name != "none" {
				if bb, have := base[r.name]; have && c == "OK" && zzErrClass(bb.err) == "OK" && r.hash != "" && r.hash != bb.hash {
					fmt.Fprintf(&sb, " %s=OKCHANGED", r.name)
					continue
				}
			}
			fmt.Fprintf(&sb, " %s=%s", r.name, c)
			if c == "OK" && r.hash != "" {
				fmt.Fprintf(&sb, ":%s", r.hash)
			}
		}
		if verr != nil {
			fmt.Fprintf(&sb, " verr=%q", zzFirst(verr.Error()))
		}
		lines = append(lines, "R "+sb.String())
		for _, fl := range flags {
			lines = append(lines, fmt.Sprintf("FLAG %s %d %s %s", gen, seed, m.name, fl))
		}
	}
	return lines
}
