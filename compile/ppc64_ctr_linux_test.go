//go:build linux && (ppc64 || ppc64le)

package compile_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/target/ppc64"
)

func ctrLoopProgram(t *testing.T, useCounter bool) *ir.Program {
	t.Helper()
	b := nyanjit.NewBuilder()
	n := b.Param()
	loop, done := b.NewLabel(), b.NewLabel()
	b.BranchArgs(b.IsZero(n), done, []nyanjit.Value{b.Const(0), n}, loop, []nyanjit.Value{n, b.Const(0)})
	state := b.BindParams(loop, 2)
	addend := b.Const(1)
	if useCounter {
		addend = state[0]
	}
	sum := b.Add(state[1], addend)
	next := b.Sub(state[0], b.Const(1))
	b.BranchArgs(b.IsZero(next).Not(), loop, []nyanjit.Value{next, sum}, done, []nyanjit.Value{sum, next})
	result := b.BindParams(done, 2)
	b.Return(b.Concat(result[0], result[1]))
	return finishPPC(t, b)
}

func TestPPC64CTRLoopExecutionAndCounterRestoration(t *testing.T) {
	cpu, _ := ppc64.HostCPU()
	for _, compact := range []bool{false, true} {
		program := ctrLoopProgram(t, false)
		options := abi.CompileOptions{PPC64CPU: cpu, Stack: abi.StackPlatform, StackBytesAvailable: 512, PreferCompactLoops: compact}
		listing, err := compile.EmitAssembly(program, options)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(listing, "bdnz ") != compact {
			t.Fatalf("compact=%t: wrong CTR selection:\n%s", compact, listing)
		}
		if compact && !strings.Contains(listing, "mtctr ") {
			t.Fatalf("CTR was not initialized:\n%s", listing)
		}
		template, err := compile.Prepare(program, options)
		if err != nil {
			t.Fatal(err)
		}
		if compact && !slices.Contains(template.Report().ClobberedSpecialRegisters, "CTR") {
			t.Fatalf("CTR omitted from effects: %+v", template.Report())
		}
		code, err := template.Bind(nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, count := range []uint64{0, 1, 2, 17, 1000} {
			sum, counter, err := code.CallPair(uintptr(count))
			if err != nil || uint64(sum) != count || counter != 0 {
				t.Fatalf("compact=%t count=%d: (%d,%d),%v; want (%d,0)\n%s", compact, count, sum, counter, err, count, listing)
			}
		}
		if err := code.Close(); err != nil {
			t.Fatal(err)
		}
		scopes, err := compile.PreflightRegisterScopes(program, options)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, scope := range scopes {
			for _, opportunity := range scope.Opportunities {
				found = found || opportunity.Register == "CTR" && opportunity.Kind == "BDNZ"
			}
		}
		if !found {
			t.Fatal("scoped preflight omitted the CTR counter opportunity")
		}
	}
}

func TestPPC64CTRLoopKeepsCounterUsedByBody(t *testing.T) {
	cpu, _ := ppc64.HostCPU()
	program := ctrLoopProgram(t, true)
	options := abi.CompileOptions{PPC64CPU: cpu, Stack: abi.StackPlatform, StackBytesAvailable: 512, PreferCompactLoops: true}
	listing, err := compile.EmitAssembly(program, options)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(listing, "bdnz ") {
		t.Fatalf("body consumes the GPR count, but it was moved to CTR:\n%s", listing)
	}
	code, err := compile.Compile(program, options)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, n := range []uint64{0, 1, 2, 17, 100} {
		sum, counter, err := code.CallPair(uintptr(n))
		if err != nil || uint64(sum) != n*(n+1)/2 || counter != 0 {
			t.Fatalf("sum loop(%d)=(%d,%d),%v", n, sum, counter, err)
		}
	}
}

func TestPPC64CTRLoopLongBranchPreservesCounter(t *testing.T) {
	cpu, _ := ppc64.HostCPU()
	b := nyanjit.NewBuilder()
	n := b.Param()
	loop, done := b.NewLabel(), b.NewLabel()
	b.JumpArgs(loop, n, b.Const(0))
	state := b.BindParams(loop, 2)
	sum := state[1]
	// Keep this body beyond BC's signed 16-bit reach, using operations that
	// remain observable as the returned sum.
	for i := 0; i < 9000; i++ {
		sum = b.Add(sum, b.Const(1))
	}
	next := b.Sub(state[0], b.Const(1))
	b.BranchArgs(b.IsZero(next).Not(), loop, []nyanjit.Value{next, sum}, done, []nyanjit.Value{sum})
	b.Return(b.BindParams(done, 1)[0])
	program := finishPPC(t, b)
	options := abi.CompileOptions{PPC64CPU: cpu, Stack: abi.StackPlatform, StackBytesAvailable: 512, PreferCompactLoops: true}
	listing, err := compile.EmitAssembly(program, options)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listing, "bdz ") || !strings.Contains(listing, "mtctr ") {
		t.Fatalf("distant CTR loop did not retain its long form (listing bytes=%d)", len(listing))
	}
	code, err := compile.Compile(program, options)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	if got, err := code.Call(2); err != nil || got != 18000 {
		t.Fatalf("long CTR loop=%d,%v; want 18000", got, err)
	}
}
