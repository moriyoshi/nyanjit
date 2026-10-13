//go:build linux && arm64

package compile_test

import (
	"fmt"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func subtractImmediateBranch(t *testing.T, width uint8, immediate uint64, predicate string) *execution.Code {
	t.Helper()
	b := nyanjit.NewBuilder()
	x := b.ParamBits(width)
	k := b.ConstBits(width, immediate)
	r := b.Sub(x, k)
	p := b.Carry(r)
	switch predicate {
	case "below":
		p = b.Compare(x, ir.BranchBelow, k)
	case "aboveEqual":
		p = b.Compare(x, ir.BranchAboveEqual, k)
	}
	// Keep the subtraction live even when the predicate compares its inputs.
	v := b.CastUnsigned(r, 64)
	yes, no := b.NewLabel(), b.NewLabel()
	// v reaches both targets by dominance.
	b.Branch(p, yes)
	b.Jump(no)
	b.Bind(yes)
	b.Return(b.Or(v, b.Const(1<<32)))
	b.Bind(no)
	b.Return(v)
	code, err := nyanjit.Compile(finishCrossAssembly(t, b), compile.TargetSpec{}, compile.Func{})
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func checkSubtractImmediateBranch(t *testing.T, width uint8, immediate uint64, predicate string) {
	t.Helper()
	code := subtractImmediateBranch(t, width, immediate, predicate)
	defer code.Close()
	mask := ^uint64(0) >> (64 - width)
	for _, x := range []uint64{immediate, mask, 0, (immediate - 1) & mask, (immediate + 1) & mask} {
		want := (x - immediate) & mask
		match := x < immediate
		if predicate == "aboveEqual" {
			match = !match
		}
		if match {
			want |= 1 << 32
		}
		got, err := code.Call(uintptr(x))
		if err != nil || uint64(got) != want {
			t.Fatalf("width %d k=%#x predicate %s x=%#x: got %#x,%v; want %#x", width, immediate, predicate, x, got, err, want)
		}
	}
}

func TestARM64SubImmediateBorrowReview(t *testing.T) {
	for _, predicate := range []string{"carry", "below", "aboveEqual"} {
		for _, k := range []uint64{0xf000, 0xf001, 0xfffb, 0xffff} {
			t.Run(fmt.Sprintf("%s/%x", predicate, k), func(t *testing.T) { checkSubtractImmediateBranch(t, 16, k, predicate) })
		}
	}
}

// This is the review's exhaustive immediate scan, with failures asserted.
func TestARM64SubImmediateBorrowExhaustive(t *testing.T) {
	for _, width := range []uint8{8, 16} {
		mask := ^uint64(0) >> (64 - width)
		for k := uint64(1); k <= mask; k++ {
			checkSubtractImmediateBranch(t, width, k, "carry")
		}
	}
}
