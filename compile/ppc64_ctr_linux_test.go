//go:build linux && ppc64le

package compile_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// ctrLoopProgram is raw IR: the counter and the sum are block parameters.
func ctrLoopProgram(t *testing.T, useCounter bool) *ir.Program {
	t.Helper()
	r := newRawIR()
	pb := r.pb
	entry := pb.Entry()
	word := ir.Scalar(64)
	n := pb.Param(entry, word)
	loop, done := pb.NewBlock(word, word), pb.NewBlock(word, word)
	pb.Branch(entry, rawIsZero(n), done, []int{r.constant(entry, 64, 0), n}, loop, []int{n, r.constant(entry, 64, 0)})
	state := r.params(loop, 2)
	addend := r.constant(loop, 64, 1)
	if useCounter {
		addend = state[0]
	}
	sum := r.op(loop, ir.OpAdd, 64, state[1], addend)
	next := r.op(loop, ir.OpSub, 64, state[0], r.constant(loop, 64, 1))
	pb.Branch(loop, &ir.PredicateExpr{Kind: ir.PredicateNot, Left: rawIsZero(next)}, loop, []int{next, sum}, done, []int{sum, next})
	result := r.params(done, 2)
	pb.Return(done, result[0], result[1])
	return r.seal().Clone()
}

func TestPPC64CTRLoopExecutionAndCounterRestoration(t *testing.T) {
	cpu, _ := ppc64.HostCPU()
	for _, compact := range []bool{false, true} {
		program := ctrLoopProgram(t, false)
		spec := ppc64LevelSpec("", cpu)
		fn := compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 512}, PreferCompactLoops: compact}
		listing, err := lowerListing(program, spec, fn)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(listing, "bdnz ") != compact {
			t.Fatalf("compact=%t: wrong CTR selection:\n%s", compact, listing)
		}
		if compact && !strings.Contains(listing, "mtctr ") {
			t.Fatalf("CTR was not initialized:\n%s", listing)
		}
		template, err := lowerTemplate(program, spec, fn)
		if err != nil {
			t.Fatal(err)
		}
		if compact && !slices.Contains(template.Report().ClobberedSpecialRegisters(), "CTR") {
			t.Fatalf("CTR omitted from effects: %+v", template.Report().Snapshot())
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
		unit, err := lowerUnit(program, spec, fn)
		if err != nil {
			t.Fatal(err)
		}
		scopes, err := unit.RegisterScopes()
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
	spec := ppc64LevelSpec("", cpu)
	fn := compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 512}, PreferCompactLoops: true}
	listing, err := lowerListing(program, spec, fn)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(listing, "bdnz ") {
		t.Fatalf("body consumes the GPR count, but it was moved to CTR:\n%s", listing)
	}
	code, err := nyanjit.Compile(program, spec, fn)
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
	// Raw IR: the counter and the sum are block parameters.
	r := newRawIR()
	pb := r.pb
	entry := pb.Entry()
	word := ir.Scalar(64)
	n := pb.Param(entry, word)
	loop, done := pb.NewBlock(word, word), pb.NewBlock(word)
	pb.Jump(entry, loop, n, r.constant(entry, 64, 0))
	state := r.params(loop, 2)
	sum := state[1]
	// Keep this body beyond BC's signed 16-bit reach, using operations that
	// remain observable as the returned sum.
	for i := 0; i < 9000; i++ {
		sum = r.op(loop, ir.OpAdd, 64, sum, r.constant(loop, 64, 1))
	}
	next := r.op(loop, ir.OpSub, 64, state[0], r.constant(loop, 64, 1))
	pb.Branch(loop, &ir.PredicateExpr{Kind: ir.PredicateNot, Left: rawIsZero(next)}, loop, []int{next, sum}, done, []int{sum})
	pb.Return(done, pb.BlockParam(done, 0))
	program := r.seal().Clone()
	spec := ppc64LevelSpec("", cpu)
	fn := compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 512}, PreferCompactLoops: true}
	listing, err := lowerListing(program, spec, fn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listing, "bdz ") || !strings.Contains(listing, "mtctr ") {
		t.Fatalf("distant CTR loop did not retain its long form (listing bytes=%d)", len(listing))
	}
	code, err := nyanjit.Compile(program, spec, fn)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	if got, err := code.Call(2); err != nil || got != 18000 {
		t.Fatalf("long CTR loop=%d,%v; want 18000", got, err)
	}
}
