//go:build linux && arm64

package varfuzz

// Predicate-validation mutation check (reviewer-owned, identical in pre and
// post): clone builder programs, mutate one branch step, compile.
// ZZMUT_OUT=file ZZMUT_GEN=g ZZMUT_FROM=a ZZMUT_TO=b

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"runtime/debug"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

func zzFirstCompare(p *ir.PredicateExpr) *ir.PredicateExpr {
	work := []*ir.PredicateExpr{p}
	for len(work) > 0 {
		n := work[len(work)-1]
		work = work[:len(work)-1]
		if n == nil {
			continue
		}
		if n.Kind == ir.PredicateCompare {
			return n
		}
		work = append(work, n.Right, n.Left)
	}
	return nil
}

func zzRemove(s []int, id int) ([]int, bool) {
	for i, v := range s {
		if v == id {
			return append(append([]int(nil), s[:i]...), s[i+1:]...), true
		}
	}
	return s, false
}

func TestZZMut(t *testing.T) {
	out := os.Getenv("ZZMUT_OUT")
	if out == "" {
		t.Skip()
	}
	gen := os.Getenv("ZZMUT_GEN")
	from, to := zzEnv("ZZMUT_FROM", 0), zzEnv("ZZMUT_TO", 10)
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
	muts := []string{"none", "drop", "opset", "dropother", "anchoropset", "otherdrop", "opsetlisted", "anchoropsetlisted"}
	for seed := from; seed < to; seed++ {
		b, err := zzBuild(gen, seed)
		if err != nil {
			continue
		}
		in, err := b.Finish()
		if err != nil {
			continue
		}
		base := in.Clone()
		if base.Control == nil {
			continue
		}
		done := 0
		for si, st := range base.Control.Steps {
			if st.IsLabel || st.Term != ir.TermBranch || done >= 3 {
				continue
			}
			done++
			for mi, m := range muts {
				p := in.Clone()
				s := &p.Control.Steps[si]
				c := zzFirstCompare(s.Predicate)
				if c == nil {
					continue
				}
				ok := true
				switch m {
				case "drop":
					s.Values, ok = zzRemove(s.Values, c.Value)
				case "otherdrop":
					if !c.WithValue {
						ok = false
						break
					}
					s.Values, ok = zzRemove(s.Values, c.Other)
				case "opset", "anchoropset", "opsetlisted", "anchoropsetlisted":
					ok = false
					for id := s.Start; id < s.End; id++ {
						if p.Instructions[id].Op == ir.OpSet {
							if m == "opset" || m == "opsetlisted" {
								c.Value = id
							} else {
								c.HasAnchor, c.Anchor = true, id
							}
							if m == "opsetlisted" || m == "anchoropsetlisted" {
								s.Values = append(append([]int(nil), s.Values...), id)
							}
							ok = true
							break
						}
					}
				case "dropother":
					ok = false
					for _, v := range s.Values {
						if v != c.Value && !(c.WithValue && v == c.Other) && !(c.HasAnchor && v == c.Anchor) {
							s.Values, _ = zzRemove(s.Values, v)
							ok = true
							break
						}
					}
				}
				if !ok {
					continue
				}
				_ = mi
				for _, cf := range cfgs {
					key := fmt.Sprintf("%s %d s%d %s %s", gen, seed, si, m, cf.name)
					l, err := func() (s string, err error) {
						defer func() {
							if r := recover(); r != nil {
								err = fmt.Errorf("panic: %v %s", r, zzFirst(string(debug.Stack())))
							}
						}()
						return zzListing(p, cf.opt)
					}()
					if err != nil {
						fmt.Fprintln(w, key+" ERR "+zzFirst(err.Error()))
						continue
					}
					d := sha256.Sum256([]byte(l))
					fmt.Fprintln(w, key+" OK "+hex.EncodeToString(d[:8]))
				}
			}
		}
	}
	_ = abi.C
}
