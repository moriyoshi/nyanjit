//go:build linux

package builder_test

import (
	"strconv"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// A deferred expression shared by both arms of a diamond is materialized in
// each arm. Reusing the first arm's materialization in the second arm would
// use a value from a block that does not dominate it, and Finish rejects that.
func TestDeferredExpressionSharedAcrossDiamondArms(t *testing.T) {
	b := builder.NewBuilder()
	must := expressionValue(t)
	x := must(b.Param())
	y := must(b.Param())
	literal := must(b.Literal(5))
	shared := x.Mul(y).Add(literal)
	result, err := b.Var(must(b.Const(0)))
	if err != nil {
		t.Fatal(err)
	}
	then, otherwise, join := b.NewLabel(), b.NewLabel(), b.NewLabel()
	predicate, err := b.CompareImmediate(x, ir.BranchBelow, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Branch(predicate, then); err != nil {
		t.Fatal(err)
	}
	if err := b.Jump(otherwise); err != nil {
		t.Fatal(err)
	}
	for _, arm := range []struct {
		label builder.Label
		value builder.Value
	}{
		{then, shared.Add(literal)},
		{otherwise, shared.Xor(literal)},
	} {
		if err := b.Bind(arm.label); err != nil {
			t.Fatal(err)
		}
		if err := b.Set(result, arm.value); err != nil {
			t.Fatal(err)
		}
		if err := b.Jump(join); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Bind(join); err != nil {
		t.Fatal(err)
	}
	// The literal is used again on its own after the join.
	if err := b.Return(must(b.Add(must(b.Get(result)), literal))); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.C})
	requireExpressionBackend(t, err)
	defer code.Close()
	for _, pair := range [][2]uint64{{3, 4}, {9, 9}, {10, 2}, {1 << 40, 7}} {
		got, err := call64(code, pair[0], pair[1])
		if err != nil {
			t.Fatal(err)
		}
		shared := pair[0]*pair[1] + 5
		want := shared ^ 5
		if pair[0] < 10 {
			want = shared + 5
		}
		want += 5
		if uint64(got) != want {
			t.Fatalf("Call(%d, %d) = %d, want %d", pair[0], pair[1], got, want)
		}
	}
}

// Within one block a shared deferred expression is still materialized once.
func TestDeferredExpressionReusedWithinBlock(t *testing.T) {
	b := builder.NewBuilder()
	must := expressionValue(t)
	x := must(b.Param())
	shared := x.Mul(x)
	label := b.NewLabel()
	if err := b.Jump(label); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(label); err != nil {
		t.Fatal(err)
	}
	first := must(b.Add(shared, x))
	if err := b.Return(must(b.Xor(first, shared))); err != nil {
		t.Fatal(err)
	}
	program, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	muls := 0
	for _, ins := range program.Instructions {
		if ins.Op == ir.OpMul {
			muls++
		}
	}
	if muls != 1 {
		t.Fatalf("shared subtree emitted %d multiplications in one block, want one", muls)
	}
}

// An expression exported by Leave keeps its materialization in a later block
// once its scope-local inputs are hidden, because it cannot be rebuilt there.
func TestDeferredExpressionExportedAcrossBlocks(t *testing.T) {
	b := builder.NewBuilder()
	must := expressionValue(t)
	x := must(b.Param())
	body, done := b.NewLabel(), b.NewLabel()
	if err := b.Jump(body); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(body); err != nil {
		t.Fatal(err)
	}
	if err := b.Enter(); err != nil {
		t.Fatal(err)
	}
	shared := x.Add(must(b.Const(3)))
	if err := b.Leave(shared); err != nil {
		t.Fatal(err)
	}
	if err := b.Jump(done); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(done); err != nil {
		t.Fatal(err)
	}
	if !b.Valid(shared) {
		t.Fatal("exported expression is invalid in a dominated block")
	}
	if err := b.Return(shared.Mul(x)); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.C})
	requireExpressionBackend(t, err)
	defer code.Close()
	if got, err := call64(code, 5); err != nil || got != 40 {
		t.Fatalf("Call(5) = %d, %v; want 40", got, err)
	}
}

// Deferred nodes come from the Builder's arena, not one heap object each.
func TestDeferredExpressionNodesUseArena(t *testing.T) {
	b := builder.NewBuilder()
	x := expressionValue(t)(b.Param())
	v := x
	allocs := testing.AllocsPerRun(1000, func() { v = v.Add(x) })
	// The arena allocates one slab per 256 nodes.
	if allocs != 0 {
		t.Fatalf("composing a node allocated %v times, want 0", allocs)
	}
}

// call64 calls code with 64-bit arguments and returns its 64-bit result,
// which take two words each on a 32-bit host.
func call64(code *execution.Code, args ...uint64) (uint64, error) {
	values := make([]any, len(args))
	for i, a := range args {
		values[i] = a
	}
	lo, hi, err := code.CallPair(execution.Words(values...)...)
	if strconv.IntSize == 32 {
		return uint64(uint32(lo)) | uint64(hi)<<32, err
	}
	return uint64(lo), err
}
