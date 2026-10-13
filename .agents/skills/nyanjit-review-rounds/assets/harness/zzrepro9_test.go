//go:build linux && arm64

package varfuzz

import (
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

// ZZR9_GEN, ZZR9_SEED, ZZR9_MUT: replay one zzmut9 mutation and print the
// full panic stack and the mutated step.
func TestZZRepro9(t *testing.T) {
	gen := os.Getenv("ZZR9_GEN")
	if gen == "" {
		t.Skip()
	}
	seed, _ := strconv.ParseInt(os.Getenv("ZZR9_SEED"), 10, 64)
	name := os.Getenv("ZZR9_MUT")
	b, err := zzBuild(gen, seed)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	in := sealed.Clone()
	for _, m := range zzM9Muts(in) {
		if m.name != name {
			continue
		}
		p := in.Clone()
		m.fn(p)
		for si, st := range p.Control.Steps {
			if st.IsLabel {
				continue
			}
			t.Logf("step %d term=%d start=%d end=%d result=%d (orig %d) arity=%d op(result)=%v", si, st.Term, st.Start, st.End, st.Result, in.Control.Steps[si].Result, st.Arity, func() any {
				if st.Result >= 0 && st.Result < len(p.Instructions) {
					return int(p.Instructions[st.Result].Op)
				}
				return "-"
			}())
		}
		t.Logf("validate: %v", p.Clone().Validate())
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Logf("PANIC %v\n%s", r, debug.Stack())
				}
			}()
			cs := zzConfigs("arm64", false)
			_, err := zzListing(p, cs[0].opt)
			t.Logf("err=%v", err)
		}()
		_ = fmt.Sprint
		_ = ir.OpSet
	}
}
