package compile_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func baselineTarget(t testing.TB) compile.Target {
	t.Helper()
	target, err := compile.NewTarget(compile.TargetSpec{Architecture: "arm64", Tier: compile.TierBaseline})
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func finished(t testing.TB, build func(*nyanjit.Builder)) *ir.SealedProgram {
	t.Helper()
	b := nyanjit.NewBuilder()
	build(b)
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBaselineStreamContract(t *testing.T) {
	target := baselineTarget(t)
	f := compile.Func{ABI: abi.C}
	if _, err := compile.NewStream(target, compile.Func{ABI: abi.C, Harden: true}); !errors.Is(err, compile.ErrUnsupported) {
		t.Fatalf("Harden: %v", err)
	}
	if _, err := compile.NewStream(target, compile.Func{ABI: abi.C, Context: compile.ContextGoManaged, AssumePointerFree: true}); !errors.Is(err, compile.ErrUnsupported) {
		t.Fatalf("ContextGoManaged: %v", err)
	}
	if _, err := compile.NewStream(nil, f); !errors.Is(err, compile.ErrInvalidFunction) {
		t.Fatalf("nil target: %v", err)
	}
	if _, err := compile.NewStream(target, compile.Func{Context: compile.ContextExternal + 1}); !errors.Is(err, compile.ErrInvalidFunction) {
		t.Fatalf("bad context: %v", err)
	}
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		tt, err := compile.NewTarget(compile.TargetSpec{Architecture: "arm64", Tier: tier})
		if err != nil {
			t.Fatal(err)
		}
		// A segment that is not the function's first breaks the stream.
		s, err := compile.NewStream(tt, f)
		if err != nil {
			t.Fatal(err)
		}
		bad := &ir.Segment{Base: 3, Step: ir.ControlStep{Term: ir.TermReturn, Start: 3, End: 3}}
		if err := s.Segment(bad); !errors.Is(err, compile.ErrInvalidFunction) {
			t.Fatalf("tier %d: bad base: %v", tier, err)
		}
		var ve *ir.ValidationError
		if err := s.Segment(&ir.Segment{}); !errors.As(err, &ve) {
			t.Fatalf("tier %d: later call after an error: %v", tier, err)
		}
		if _, err := s.Unit(); !errors.Is(err, compile.ErrInvalidFunction) {
			t.Fatalf("tier %d: Unit after an error: %v", tier, err)
		}
		// A complete function, then a second Unit.
		s, _ = compile.NewStream(tt, f)
		p := finished(t, func(b *nyanjit.Builder) { b.Return(b.Add(b.Param(), b.Const(1))) })
		if err := compile.ProgramSegments(sealedView(p), s.Segment); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Unit(); err != nil {
			t.Fatalf("tier %d: %v", tier, err)
		}
		if _, err := s.Unit(); !errors.Is(err, compile.ErrInvalidFunction) {
			t.Fatalf("tier %d: second Unit: %v", tier, err)
		}
		// A use that its definition does not dominate.
		s, _ = compile.NewStream(tt, f)
		segs := []ir.Segment{
			{Code: []ir.Instruction{{Op: ir.OpParam}}, Step: ir.ControlStep{Term: ir.TermBranch, End: 1,
				Predicate: &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchEqual, Value: 0}, Values: []int{0}, To: 0, Other: 1}},
			{Labels: []ir.Label{0}, Base: 1, Code: []ir.Instruction{{Op: ir.OpConst, Imm: 5}}, Step: ir.ControlStep{Term: ir.TermJump, Start: 1, End: 2, To: 2}},
			{Labels: []ir.Label{1}, Base: 2, Step: ir.ControlStep{Term: ir.TermJump, Start: 2, End: 2, To: 2}},
			{Labels: []ir.Label{2}, Base: 2, Step: ir.ControlStep{Term: ir.TermReturn, Start: 2, End: 2, Result: 1}},
		}
		for i := range segs {
			if err := s.Segment(&segs[i]); err != nil {
				t.Fatalf("tier %d segment %d: %v", tier, i, err)
			}
		}
		if _, err := s.Unit(); !errors.Is(err, compile.ErrInvalidFunction) || !errors.As(err, &ve) {
			t.Fatalf("tier %d: undominated use: %v", tier, err)
		}
		// An unbound label.
		s, _ = compile.NewStream(tt, f)
		jump := ir.Segment{Code: []ir.Instruction{{Op: ir.OpParam}}, Step: ir.ControlStep{Term: ir.TermJump, End: 1, To: 7}}
		if err := s.Segment(&jump); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Unit(); !errors.Is(err, compile.ErrInvalidFunction) {
			t.Fatalf("tier %d: unbound label: %v", tier, err)
		}
	}
}

func TestBaselineUnitViews(t *testing.T) {
	target := baselineTarget(t)
	p := finished(t, func(b *nyanjit.Builder) { b.Return(b.Mul(b.Param(), b.Const(7))) })
	u, err := compile.Lower(target, p, compile.Func{ABI: abi.C})
	if err != nil {
		t.Fatal(err)
	}
	l, err := u.Listing()
	if err != nil || !strings.Contains(l, "mul") {
		t.Fatalf("listing %q: %v", l, err)
	}
	if s, err := u.ListingSymbolic(); err != nil || s != l {
		t.Fatalf("ListingSymbolic: %v", err)
	}
	if _, err := u.RegisterScopes(); !errors.Is(err, compile.ErrUnsupported) {
		t.Fatalf("RegisterScopes: %v", err)
	}
	r := u.Report()
	if r.Architecture() != "arm64" || r.CodeBytes() == 0 || len(r.ArgumentRegisters()) != 1 || r.ResultRegister() != "X0" {
		t.Fatalf("report: %+v", r.Snapshot())
	}
}

// TestBaselineCallsList lowers calls and system calls for an external
// function, which only links, and checks the listing names them.
func TestBaselineCallsList(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("the program passes 64-bit call targets, which a Builder on a 32-bit host rejects")
	}
	target := baselineTarget(t)
	p := finished(t, func(b *nyanjit.Builder) {
		x := b.Param()
		r := b.CallC(b.Const(0x1000), x, b.Const(2))
		s := b.Syscall(b.Const(64), b.Const(1), r, b.Const(3))
		g := b.CallGo(b.Const(0x2000), s, x)
		b.Return(b.Add(b.Add(r, s), b.Add(g, x)))
	})
	for _, f := range []compile.Func{
		{ABI: abi.Go, Context: compile.ContextExternal, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}},
	} {
		u, err := compile.Lower(target, p, f)
		if err != nil {
			t.Fatal(err)
		}
		l, err := u.Listing()
		if err != nil || !strings.Contains(l, "blr X16") || !strings.Contains(l, "svc") {
			t.Fatalf("listing %q: %v", l, err)
		}
		if !u.Report().HasFunctionCalls() || !u.Report().HasSystemCalls() {
			t.Fatalf("report: %+v", u.Report().Snapshot())
		}
	}
	if _, err := compile.Lower(target, p, compile.Func{ABI: abi.C}); !errors.Is(err, compile.ErrExternalCallOnly) {
		t.Fatalf("leaf with calls: %v", err)
	}
}

func TestBaselineSymbols(t *testing.T) {
	target := baselineTarget(t)
	p := finished(t, func(b *nyanjit.Builder) { b.Return(b.Add(b.Param(), b.Symbol("k"))) })
	if _, err := compile.Lower(target, p, compile.Func{ABI: abi.C, Constants: map[string]uint64{"k": 5}}); err != nil {
		t.Fatal(err)
	}
	if _, err := compile.Lower(target, p, compile.Func{ABI: abi.C, Constants: map[string]uint64{"j": 5}}); err == nil {
		t.Fatal("unbound constant accepted")
	}
	u, err := compile.Lower(target, p, compile.Func{ABI: abi.C})
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := u.Listing(); !strings.Contains(l, "<k>") {
		t.Fatalf("listing %q", l)
	}
}
