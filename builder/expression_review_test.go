package builder_test

import (
	"testing"
	"time"

	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestDeferredOuterLeavesCapturedOnce(t *testing.T) {
	b := builder.NewBuilder()
	entry := b.NewLabel()
	if err := b.Bind(entry); err != nil {
		t.Fatal(err)
	}
	x, _ := b.Param()
	y, _ := b.Param()
	if err := b.Enter(); err != nil {
		t.Fatal(err)
	}
	next := b.NewLabel()
	if err := b.Jump(next); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(next); err != nil {
		t.Fatal(err)
	}
	v, err := b.Add(x.Add(y).Xor(x.Mul(y)), x.Sub(y))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Leave(v); err != nil {
		t.Fatal(err)
	}
	if err := b.Return(v); err != nil {
		t.Fatal(err)
	}
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	gets := 0
	for _, ins := range p.Instructions {
		if ins.Op == ir.OpGet {
			gets++
		}
	}
	if gets != 6 || len(p.Instructions) != 15 {
		t.Fatalf("duplicate captures: %d Gets, %d instructions", gets, len(p.Instructions))
	}
}

func TestVectorExpressionValidationLinear(t *testing.T) {
	b := builder.NewBuilder()
	x, _ := b.ParamBits(32)
	v, err := b.Splat(ir.Vec(4, 32), x)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for range 100000 {
		// Builder.binary validates this accumulated deferred tree each time.
		v, err = b.Add(v.Xor(v), v)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Emulators and race instrumentation need more room than native Go.
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("100k vector chain took %v", elapsed)
	}
	t.Logf("100k vector operations: %v", time.Since(start))
}

func TestExpressionValidityFollowsScopesAndBlocks(t *testing.T) {
	b := builder.NewBuilder()
	x, _ := b.Param()
	if err := b.Enter(); err != nil {
		t.Fatal(err)
	}
	local, _ := b.Const(7)
	v := x.Xor(local)
	if !b.Valid(v) {
		t.Fatal("local expression invalid")
	}
	if err := b.Leave(); err != nil {
		t.Fatal(err)
	}
	if b.Valid(v) {
		t.Fatal("cached expression escaped its scope")
	}
	outer := x.Xor(x)
	if !b.Valid(outer) {
		t.Fatal("outer expression invalid")
	}
	label := b.NewLabel()
	if err := b.Jump(label); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(label); err != nil {
		t.Fatal(err)
	}
	// A value stays usable in the blocks its definition dominates; Finish
	// checks dominance once the graph is complete.
	if !b.Valid(outer) {
		t.Fatal("dominating expression became invalid in a later block")
	}
}
