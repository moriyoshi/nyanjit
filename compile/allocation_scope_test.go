package compile_test

import (
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestSparseScopeIdentifiersKeepHomes(t *testing.T) {
	b := n.NewBuilder()
	x := b.ParamBits(64)
	b.Enter()
	v := b.Var(x)
	b.Enter()
	b.Set(v, b.Add(b.Get(v), b.Const(7)))
	out := b.Get(v)
	b.Leave(out)
	b.Leave(out)
	b.Return(out)
	sealed, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	raw := sealed.Clone()
	if raw.Control != nil {
		t.Fatal("fixture should use straight-line scope boundaries")
	}
	rename := func(id int) int {
		if id == 0 {
			return 0
		}
		return 1000003 + id*7
	}
	for i := range raw.ScopeExits {
		s := &raw.ScopeExits[i]
		s.ID = rename(s.ID)
		s.Parent = rename(s.Parent)
	}
	for i := range raw.Variables {
		raw.Variables[i].Scope = rename(raw.Variables[i].Scope)
		raw.Variables[i].Region = rename(raw.Variables[i].Region)
	}
	for i := range raw.Instructions {
		ins := &raw.Instructions[i]
		if ins.Op == ir.OpScopeEnter || ins.Op == ir.OpScopeLeave {
			ins.Imm = uint64(rename(int(ins.Imm)))
		}
	}
	if err := raw.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"amd64", "386", "arm64", "riscv64", "loong64", "ppc64le"} {
		spec := compile.TargetSpec{Architecture: arch}
		before, err := lowerListing(sealed, spec, compile.Func{})
		if err != nil {
			t.Fatal(err)
		}
		after, err := lowerListing(raw, spec, compile.Func{})
		if err != nil {
			t.Fatalf("%s: %v", arch, err)
		}
		if before != after {
			t.Fatalf("%s: sparse scope IDs changed homes\nbefore%s\nafter%s", arch, before, after)
		}
	}
}
