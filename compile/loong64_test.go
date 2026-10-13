package compile_test

import (
	"strings"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestLoong64PhaseBoundaries(t *testing.T) {
	for _, control := range []bool{false, true} {
		b := n.NewBuilder()
		v := b.Var(b.Float64(2))
		if control {
			l := b.NewLabel()
			b.Jump(l)
			b.Bind(l)
		}
		b.Return(b.FAdd(b.Get(v), b.Float64(3)))
		p, e := testFinishRaw(b)
		if e != nil {
			t.Fatal(e)
		}
		_, e = lowerListing(p, compile.TargetSpec{Architecture: "loong64"}, compile.Func{})
		if e != nil {
			t.Fatalf("FP lowering failed: %v", e)
		}
	}
	p := &ir.Program{Arity: 1, Result: 1, Instructions: []ir.Instruction{{Op: ir.OpParam, Bits: 64}, {Op: ir.OpCallC, Bits: 64, Args: []int{0}}}}
	_, err := lowerListing(p, compile.TargetSpec{Architecture: "loong64"}, compile.Func{Context: compile.ContextExternal, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
	if err != nil {
		t.Fatalf("call lowering failed: %v", err)
	}
	target, err := compile.NewTarget(compile.TargetSpec{Architecture: "loong64", CPU: abi.CPUProfile{Extensions: []string{"lsx", "lasx"}}})
	if err != nil {
		t.Fatalf("LSX target: %v", err)
	}
	info := target.Vectors()
	if info.PreferredBits != 128 || info.Lowering(ir.OpAdd, ir.Vec(4, 32)) != abi.Native {
		t.Fatalf("LSX lowering unavailable: %+v", info)
	}
}
func TestLoong64Countdown(t *testing.T) {
	b := n.NewBuilder()
	v := b.Var(b.ParamBits(64))
	loop, done := b.NewLabel(), b.NewLabel()
	b.Jump(loop)
	b.Bind(loop)
	b.Set(v, b.Sub(b.Get(v), b.Const(1)))
	b.Branch(b.IsZero(b.Get(v)).Not(), loop)
	b.Jump(done)
	b.Bind(done)
	b.Return(b.Get(v))
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	txt, err := lowerListing(p, compile.TargetSpec{Architecture: "loong64"}, compile.Func{PreferCompactLoops: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(txt, "bnez ") {
		t.Fatalf("countdown lost direct branch:\n%s", txt)
	}
}
