//go:build linux && arm64

package varfuzz

// Args/OtherArgs/Values/arity mutation check (reviewer-owned, identical in
// pre and post). ZZMA_OUT=file ZZMA_GEN=g ZZMA_FROM=a ZZMA_TO=b

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"runtime/debug"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func zzNonValue(op ir.Opcode) bool {
	switch op {
	case ir.OpSet, ir.OpScopeEnter, ir.OpScopeLeave, ir.OpStore64, ir.OpStore8, ir.OpVStore:
		return true
	}
	return false
}

func TestZZMutArgs(t *testing.T) {
	out := os.Getenv("ZZMA_OUT")
	if out == "" {
		t.Skip()
	}
	gen := os.Getenv("ZZMA_GEN")
	from, to := zzEnv("ZZMA_FROM", 0), zzEnv("ZZMA_TO", 10)
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()
	var cfgs []zzCfg
	for _, arch := range []string{"amd64", "arm64", "386", "riscv64"} {
		cs := zzConfigs(arch, false)
		cfgs = append(cfgs, zzCfg{arch + " " + cs[0].name, cs[0].opt}, zzCfg{arch + " " + cs[3].name, cs[3].opt})
	}
	emit := func(key string, p *ir.Program) {
		for _, cf := range cfgs {
			k := key + " " + cf.name
			l, err := func() (s string, err error) {
				defer func() {
					if r := recover(); r != nil {
						err = fmt.Errorf("panic: %v %s", r, zzFirst(string(debug.Stack())))
					}
				}()
				return zzListing(p, cf.opt)
			}()
			if err != nil {
				fmt.Fprintln(w, k+" ERR "+zzFirst(err.Error()))
				continue
			}
			d := sha256.Sum256([]byte(l))
			fmt.Fprintln(w, k+" OK "+hex.EncodeToString(d[:8]))
		}
	}
	stats := map[string]int{}
	for seed := from; seed < to; seed++ {
		b, err := zzBuild(gen, seed)
		if err != nil {
			continue
		}
		sealed, err := b.Finish()
		if err != nil {
			continue
		}
		in := sealed.Clone()
		emit(fmt.Sprintf("%s %d - none", gen, seed), in.Clone())
		for _, d := range []int{1, -1} {
			if in.Arity+d < 0 {
				continue
			}
			p := in.Clone()
			p.Arity += d
			emit(fmt.Sprintf("%s %d - arity%+d", gen, seed, d), p)
		}
		if in.Control == nil {
			continue
		}
		done := map[string]int{}
		for si, st := range in.Control.Steps {
			if st.IsLabel {
				continue
			}
			nv := -1
			for id := st.Start; id < st.End; id++ {
				if zzNonValue(in.Instructions[id].Op) {
					nv = id
					break
				}
			}
			type mut struct {
				name string
				ok   bool
				fn   func(s *ir.ControlStep)
			}
			muts := []mut{
				{"argsnonvalue", nv >= 0 && len(st.Args) > 0, func(s *ir.ControlStep) { s.Args = append([]int(nil), s.Args...); s.Args[0] = nv }},
				{"argslastnonvalue", nv >= 0 && len(st.Args) > 1, func(s *ir.ControlStep) { s.Args = append([]int(nil), s.Args...); s.Args[len(s.Args)-1] = nv }},
				{"otherargsnonvalue", nv >= 0 && len(st.OtherArgs) > 0, func(s *ir.ControlStep) {
					s.OtherArgs = append([]int(nil), s.OtherArgs...)
					s.OtherArgs[0] = nv
				}},
				{"valuesnonvalue", nv >= 0, func(s *ir.ControlStep) { s.Values = append(append([]int(nil), s.Values...), nv) }},
				{"argsdrop", len(st.Args) > 0, func(s *ir.ControlStep) { s.Args = append([]int(nil), s.Args[:len(s.Args)-1]...) }},
				{"argsextra", len(st.Args) > 0, func(s *ir.ControlStep) { s.Args = append(append([]int(nil), s.Args...), s.Args[0]) }},
			}
			for _, m := range muts {
				if !m.ok || done[m.name] >= 3 {
					continue
				}
				done[m.name]++
				stats[m.name]++
				p := in.Clone()
				m.fn(&p.Control.Steps[si])
				emit(fmt.Sprintf("%s %d s%d %s", gen, seed, si, m.name), p)
			}
		}
	}
	t.Logf("mutations: %v", stats)
}
