//go:build linux && (arm64 || riscv64)

package compile_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestCompactZeroCountdownExecution(t *testing.T) {
	// Raw IR: the countdown is a block parameter.
	r := newRawIR()
	pb := r.pb
	entry := pb.Entry()
	word := ir.Scalar(64)
	n := pb.Param(entry, word)
	loop, done := pb.NewBlock(word), pb.NewBlock(word)
	pb.Branch(entry, rawIsZero(n), done, []int{n}, loop, []int{n})
	next := r.op(loop, ir.OpSub, 64, pb.BlockParam(loop, 0), r.constant(loop, 64, 1))
	pb.Branch(loop, &ir.PredicateExpr{Kind: ir.PredicateNot, Left: rawIsZero(next)}, loop, []int{next}, done, []int{next})
	pb.Return(done, pb.BlockParam(done, 0))
	p := r.seal().Clone()
	for _, compact := range []bool{false, true} {
		o := compile.Func{PreferCompactLoops: compact}
		asm, err := lowerListing(p, compile.TargetSpec{}, o)
		if err != nil {
			t.Fatal(err)
		}
		if compact && runtime.GOARCH == "arm64" && !strings.Contains(asm, "cbnz") {
			t.Fatalf("missing flag-neutral countdown branch:\n%s", asm)
		}
		if compact && runtime.GOARCH == "arm64" && strings.Contains(asm, "subs ") {
			t.Fatalf("decrement unnecessarily modifies NZCV:\n%s", asm)
		}
		code, err := nyanjit.Compile(p, compile.TargetSpec{}, o)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range []uint64{0, 1, 2, 17, 1000} {
			got, err := code.Call(uintptr(n))
			if err != nil || got != 0 {
				t.Fatalf("n=%d compact=%v: %d,%v", n, compact, got, err)
			}
		}
		code.Close()
	}
}
