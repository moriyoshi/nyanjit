//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestDeepPredicateLimit(t *testing.T) {
	if os.Getenv("NYANJIT_PREDICATE_CHILD") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDeepPredicateLimit$", "-test.v")
		cmd.Env = append(os.Environ(), "NYANJIT_PREDICATE_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("predicate subprocess: %v\n%s", err, out)
		}
		return
	}
	debug.SetMaxStack(1 << 20)
	count := 10000
	if v := os.Getenv("NYANJIT_PREDICATE_CHAIN"); v != "" {
		var err error
		count, err = strconv.Atoi(v)
		if err != nil || count < 1 {
			t.Fatalf("invalid predicate count %q", v)
		}
	}
	b := builder.NewBuilder()
	x, _ := b.Param()
	// Force vector legalization to remap the deep scalar predicate as well.
	if _, err := b.Splat(ir.Vec(2, 64), x); err != nil {
		t.Fatal(err)
	}
	leaf, err := b.IsZero(x)
	if err != nil {
		t.Fatal(err)
	}
	predicate := leaf
	for range count {
		predicate = predicate.And(leaf).Not()
	}
	// A small shared graph exercises the expanded-work bound cheaply in the
	// normal suite. The million-node environment override retains the crash case.
	for nodes := 3*count + 1; nodes <= ir.MaxPredicateNodes; nodes = 2*nodes + 1 {
		predicate = predicate.And(predicate)
	}
	yes, no := b.NewLabel(), b.NewLabel()
	if err := b.Branch(predicate, yes); !errors.Is(err, builder.ErrBackendLimit) {
		t.Fatalf("Builder Branch: %v", err)
	}
	if err := b.Branch(leaf, yes); err != nil {
		t.Fatal(err)
	}
	if err := b.Jump(no); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(yes); err != nil {
		t.Fatal(err)
	}
	v, _ := b.Const(1)
	if err := b.Return(v); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(no); err != nil {
		t.Fatal(err)
	}
	v, _ = b.Const(0)
	if err := b.Return(v); err != nil {
		t.Fatal(err)
	}
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	var branch *ir.ControlStep
	for k := range p.Control.Steps {
		if p.Control.Steps[k].Term == ir.TermBranch {
			branch = &p.Control.Steps[k]
			break
		}
	}
	root := branch.Predicate
	leftValue := root.Value
	withinLimit := root
	for range ir.MaxPredicateNodes / 2 {
		withinLimit = &ir.PredicateExpr{Kind: ir.PredicateNot, Left: withinLimit}
	}
	branch.Predicate = withinLimit
	for _, arch := range []string{"386", "amd64", "arm64", "riscv64", "ppc64le", "loong64"} {
		spec := compile.TargetSpec{Architecture: arch}
		fn := compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}}
		if _, err := lowerListing(p, spec, fn); err != nil {
			t.Fatalf("%s deep predicate: %v", arch, err)
		}
		u, err := lowerUnit(p, spec, fn)
		if err != nil {
			t.Fatalf("%s deep predicate lower: %v", arch, err)
		}
		if _, err := u.RegisterScopes(); err != nil {
			t.Fatalf("%s deep predicate preflight: %v", arch, err)
		}
	}
	branch.Predicate = root
	for range count {
		root = &ir.PredicateExpr{Kind: ir.PredicateNot, Left: &ir.PredicateExpr{Kind: ir.PredicateAnd, Left: root, Right: branch.Predicate}}
	}
	for nodes := 3*count + 1; nodes <= ir.MaxPredicateNodes; nodes = 2*nodes + 1 {
		root = &ir.PredicateExpr{Kind: ir.PredicateAnd, Left: root, Right: root}
	}
	branch.Predicate = root
	// Public copies and queries must be safe even for caller-mutated IR that
	// has not passed Validate. Compilation rejects it before recursive walks.
	copy := p.Clone()
	if !ir.PredicateUsesLeftValue(ir.RebasePredicate(root, 0), leftValue) {
		t.Fatal("iterative predicate query lost values")
	}
	if !errors.Is(copy.Validate(), ir.ErrPredicateLimit) {
		t.Fatal("IR accepted oversized predicate")
	}
	for _, arch := range []string{"386", "amd64", "arm64", "riscv64", "ppc64le", "loong64"} {
		_, err := lowerListing(copy, compile.TargetSpec{Architecture: arch}, compile.Func{})
		var limit *compile.LimitError
		if !errors.Is(err, compile.ErrBackendLimit) || !strings.Contains(err.Error(), "predicate") ||
			!errors.As(err, &limit) || limit.Kind != compile.LimitPredicateNodes || limit.Limit != ir.MaxPredicateNodes || limit.Needed <= limit.Limit {
			t.Fatalf("%s: %v", arch, err)
		}
	}
}
