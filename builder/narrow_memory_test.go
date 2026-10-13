package builder_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestNarrowMemoryCompactAlignmentAndScopes(t *testing.T) {
	b := builder.NewBuilder()
	d, e := builder.PointerParam[struct {
		A uint32
		B uint16
	}](b)
	if e != nil {
		t.Fatal(e)
	}
	// Cross both the compact-prefix and block boundaries before aligned accesses.
	for i := 0; i < 1600; i++ {
		if _, e = b.Const(uint64(i)); e != nil {
			t.Fatal(e)
		}
	}
	if e = b.Enter(); e != nil {
		t.Fatal(e)
	}
	a, e := d.Field("A")
	if e != nil {
		t.Fatal(e)
	}
	f, e := d.At("A")
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Store(a); e != nil {
		t.Fatal(e)
	}
	f, e = d.At("B")
	if e != nil {
		t.Fatal(e)
	}
	v, e := f.Load()
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Store(v); e != nil {
		t.Fatal(e)
	}
	if e = b.Leave(a, v); e != nil {
		t.Fatal(e)
	}
	if e = b.Return(a); e != nil {
		t.Fatal(e)
	}
	p, e := b.Finish()
	if e != nil {
		t.Fatal(e)
	}
	counts := map[ir.Opcode]int{}
	for _, i := range p.Clone().Instructions {
		if i.NarrowMemoryBits() != 0 {
			if !i.Aligned {
				t.Fatalf("alignment lost: %+v", i)
			}
			counts[i.Op]++
		}
	}
	for _, op := range []ir.Opcode{ir.OpLoad16, ir.OpStore16, ir.OpLoad32, ir.OpStore32} {
		if counts[op] != 1 {
			t.Fatalf("counts%v", counts)
		}
	}
}
