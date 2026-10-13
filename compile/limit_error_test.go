package compile_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/workload"
	"github.com/moriyoshi/nyanjit/ir"
)

// limitError checks that err is a *LimitError matching ErrBackendLimit and
// returns it.
func limitError(t *testing.T, err error, kind compile.LimitKind) *compile.LimitError {
	t.Helper()
	var limit *compile.LimitError
	if !errors.As(err, &limit) || !errors.Is(err, compile.ErrBackendLimit) {
		t.Fatalf("want a *LimitError matching ErrBackendLimit, got %v", err)
	}
	if limit.Kind != kind {
		t.Fatalf("kind %v, want %v: %v", limit.Kind, kind, err)
	}
	return limit
}

func TestLimitErrorSpillWords(t *testing.T) {
	// Home storage counts every declared Var; promoted Vars do not use it.
	compile.WithoutVarPromotion(t)
	_, err := lowerUnit(workload.ManyVars(4000), compile.TargetSpec{Architecture: "amd64"}, compile.Func{ABI: abi.C})
	limit := limitError(t, err, compile.LimitSpillWords)
	if limit.Limit != compile.MaxSpillSlots || limit.Needed <= limit.Limit {
		t.Fatalf("needed %d, limit %d", limit.Needed, limit.Limit)
	}
}

func TestLimitErrorArguments(t *testing.T) {
	p := &ir.Program{Arity: 7}
	for range p.Arity {
		p.Instructions = append(p.Instructions, ir.Instruction{Op: ir.OpParam})
	}
	_, err := lowerUnit(p, compile.TargetSpec{Architecture: "amd64"}, compile.Func{ABI: abi.C})
	if limit := limitError(t, err, compile.LimitArguments); limit.Needed != 7 || limit.Limit != 6 || limit.Arch != "amd64" {
		t.Fatalf("entry: %+v", limit)
	}

	for _, c := range []struct {
		spec   abi.CallSpec
		needed int
		limit  int
	}{
		{abi.CallSpec{Architecture: "amd64", Kind: abi.FunctionCall, ABI: abi.C, ArgumentCount: 7}, 7, 6},
		{abi.CallSpec{Architecture: "amd64", Kind: abi.FunctionCall, ABI: abi.Go, ArgumentCount: 8}, 8, 7},
		{abi.CallSpec{Architecture: "arm64", Kind: abi.SystemCall, ABI: abi.C, ArgumentCount: 7}, 7, 6},
	} {
		_, err := compile.PreflightCall(c.spec)
		if limit := limitError(t, err, compile.LimitArguments); limit.Needed != c.needed || limit.Limit != c.limit || limit.Arch != c.spec.Architecture {
			t.Fatalf("%+v: %+v", c.spec, limit)
		}
	}
	// A negative count is not a count above the limit.
	_, err = compile.PreflightCall(abi.CallSpec{Kind: abi.FunctionCall, ABI: abi.C, ArgumentCount: -1})
	var limit *compile.LimitError
	if !errors.Is(err, compile.ErrBackendLimit) || errors.As(err, &limit) {
		t.Fatalf("negative count: %v", err)
	}
}

func TestLimitErrorFrameBytes(t *testing.T) {
	b := nyanjit.NewBuilder()
	x := b.Param()
	vars := make([]nyanjit.Var, 30)
	for k := range vars {
		vars[k] = b.Var(b.Add(x, b.Const(uint64(k))))
	}
	loop, done := b.NewLabel(), b.NewLabel()
	b.Jump(loop)
	b.Bind(loop)
	values := make([]nyanjit.Value, len(vars))
	for k := range vars {
		values[k] = b.Get(vars[k])
	}
	for k := range vars {
		b.Set(vars[k], b.Add(b.Mul(values[k], b.Const(3)), values[(k+1)%len(vars)]))
	}
	b.Jump(done)
	b.Bind(done)
	sum := b.Get(vars[0])
	for _, v := range vars[1:] {
		sum = b.Add(sum, b.Get(v))
	}
	b.Return(sum)
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	fn := compile.Func{ABI: abi.C, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}, AssumePointerFree: true}
	_, err = lowerTemplate(p, compile.TargetSpec{Architecture: "386"}, fn)
	limit := limitError(t, err, compile.LimitFrameBytes)
	if limit.Arch != "386" || limit.Limit != 240 || limit.Needed <= limit.Limit {
		t.Fatalf("%+v", limit)
	}
}

func TestLimitErrorText(t *testing.T) {
	for _, c := range []struct {
		err  compile.LimitError
		want string
	}{
		{compile.LimitError{Kind: compile.LimitSpillWords, Needed: 4100, Limit: 4096, Detail: "reserved homes/parameters 12"},
			"spill storage needs 4100 words (reserved homes/parameters 12), limit 4096"},
		{compile.LimitError{Kind: compile.LimitFrameBytes, Needed: 528, Limit: 240, Arch: "386", Detail: "managed frame"},
			"386 frame needs 528 bytes (managed frame), limit 240"},
		{compile.LimitError{Kind: compile.LimitBranchRange, Arch: "arm64"}, "arm64 branch range over the limit"},
		{compile.LimitError{Kind: 99, Needed: 3, Limit: 2}, "LimitKind(99) needs 3, limit 2"},
	} {
		if got, want := c.err.Error(), c.want+": "+compile.ErrBackendLimit.Error(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	if s := compile.LimitPredicateNodes.String(); s != "predicate nodes" {
		t.Fatalf("String = %q", s)
	}
}

func TestTargetTier(t *testing.T) {
	zero, err := compile.NewTarget(compile.TargetSpec{Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	optimized, err := compile.NewTarget(compile.TargetSpec{Architecture: "amd64", Tier: compile.TierOptimized})
	if err != nil || optimized != zero {
		t.Fatalf("TierOptimized is not the default: %v", err)
	}
	if _, err := compile.NewTarget(compile.TargetSpec{Architecture: "amd64", Tier: compile.TierBaseline + 1}); !errors.Is(err, abi.ErrInvalidSpec) || !errors.Is(err, compile.ErrInvalidFunction) {
		t.Fatalf("unknown tier: %v", err)
	}
	for _, arch := range []string{"386", "loong64", "ppc64le", "riscv64"} {
		if _, err := compile.NewTarget(compile.TargetSpec{Architecture: arch, Tier: compile.TierBaseline}); err != nil && !errors.Is(err, compile.ErrUnsupported) {
			t.Fatalf("%s baseline tier: %v", arch, err)
		}
	}
	if b, err := compile.NewTarget(compile.TargetSpec{Architecture: "amd64", Tier: compile.TierBaseline}); err != nil || b == zero {
		t.Fatalf("amd64 baseline tier: %v", err)
	}
	baseline, err := compile.NewTarget(compile.TargetSpec{Architecture: "arm64", Tier: compile.TierBaseline})
	if err != nil || baseline.Spec().Tier != compile.TierBaseline {
		t.Fatalf("arm64 baseline tier: %v", err)
	}
	if again, _ := compile.NewTarget(compile.TargetSpec{Architecture: "arm64", Tier: compile.TierBaseline}); again != baseline {
		t.Fatal("baseline Targets are not shared")
	}
	if optimized, _ := compile.NewTarget(compile.TargetSpec{Architecture: "arm64"}); optimized == baseline {
		t.Fatal("baseline and optimized Targets are the same")
	}
}
