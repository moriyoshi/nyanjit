//go:build linux && (arm64 || riscv64)

package compile_test

import (
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"runtime"
	"strings"
	"testing"
)

func TestCompactZeroCountdownExecution(t *testing.T) {
	b := nyanjit.NewBuilder()
	n := b.ParamBits(64)
	loop, done := b.NewLabel(), b.NewLabel()
	b.BranchArgs(b.IsZero(n), done, []nyanjit.Value{n}, loop, []nyanjit.Value{n})
	state := b.BindParams(loop, 1)
	next := b.Sub(state[0], b.Const(1))
	b.BranchArgs(b.IsZero(next).Not(), loop, []nyanjit.Value{next}, done, []nyanjit.Value{next})
	result := b.BindParams(done, 1)
	b.Return(result[0])
	p := finishCrossAssembly(t, b)
	for _, compact := range []bool{false, true} {
		o := abi.CompileOptions{PreferCompactLoops: compact}
		asm, err := compile.EmitAssembly(p, o)
		if err != nil {
			t.Fatal(err)
		}
		if compact && runtime.GOARCH == "arm64" && !strings.Contains(asm, "cbnz") {
			t.Fatalf("missing flag-neutral countdown branch:\n%s", asm)
		}
		if compact && runtime.GOARCH == "arm64" && strings.Contains(asm, "subs ") {
			t.Fatalf("decrement unnecessarily modifies NZCV:\n%s", asm)
		}
		code, err := compile.Compile(p, o)
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
