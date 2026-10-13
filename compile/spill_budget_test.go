package compile_test

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/workload"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestScopedSpillBudgetReportsStorageLimit(t *testing.T) {
	// Home storage counts every declared Var; promoted Vars do not use it.
	compile.WithoutVarPromotion(t)
	tests := []struct {
		name           string
		build          func(int) *ir.SealedProgram
		size, reserved int
		control        bool
	}{
		{"many-vars", workload.ManyVars, 4000, 4001, false},
		{"deep-scopes", workload.DeepScopes, 8000, 8002, false},
		{"deep-scope-loops", workload.DeepScopeLoops, 4200, 4202, true},
		{"nested-loops", workload.NestedLoops, 4200, 4202, true},
		{"sibling-scopes", siblingScopeVars, 4100, 4102, false},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			program := c.build(c.size)
			for _, arch := range []string{"arm64", "amd64", "386", "riscv64", "loong64", "ppc64le"} {
				t.Run(arch, func(t *testing.T) {
					_, err := lowerUnit(program, compile.TargetSpec{Architecture: arch}, compile.Func{ABI: abi.C})
					if !errors.Is(err, compile.ErrBackendLimit) {
						t.Fatalf("expected storage budget limit: %v", err)
					}
					reserved, limit := c.reserved, 4096
					if arch == "386" {
						reserved *= 2
					}
					for _, part := range []string{fmt.Sprintf("spill storage needs %d words", max(reserved, limit+1)), fmt.Sprintf("reserved homes/parameters %d", reserved), fmt.Sprintf("limit %d", limit)} {
						if !strings.Contains(err.Error(), part) {
							t.Fatalf("missing %q in %v", part, err)
						}
					}
					if strings.Contains(err.Error(), "(pins ") {
						t.Fatalf("internal register pins leaked: %v", err)
					}
				})
			}
		})
	}
}

// Each sibling's Var is dead before the next one is declared, but fixed home
// storage still counts every declaration rather than only simultaneous lives.
func siblingScopeVars(n int) *ir.SealedProgram {
	b := nyanjit.NewBuilder()
	acc := b.Var(b.Param())
	for i := 0; i < n; i++ {
		b.Enter()
		v := b.Var(b.Const(uint64(i + 1)))
		b.Set(acc, b.Add(b.Get(acc), b.Get(v)))
		b.Leave()
	}
	b.Return(b.Get(acc))
	p, err := b.Finish()
	if err != nil {
		panic(err)
	}
	return p
}

// MaxSpillSlots values cross one edge by dominance beside 200 Vars, so
// lowering threads them through as label parameters.
func TestLabelParameterSpillBudgetReportsStorageLimit(t *testing.T) {
	// The Vars fill home storage.
	compile.WithoutVarPromotion(t)
	b := nyanjit.NewBuilder()
	x := b.Param()
	for i := 0; i < 200; i++ {
		b.Var(b.Const(uint64(i)))
	}
	values := make([]nyanjit.Value, compile.MaxSpillSlots)
	for i := range values {
		values[i] = b.Add(x, b.Const(uint64(i)))
	}
	label := b.NewLabel()
	b.Jump(label)
	b.Bind(label)
	sum := values[0]
	for _, v := range values[1:] {
		sum = b.Xor(sum, v)
	}
	b.Return(sum)
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"arm64", "amd64", "386", "riscv64", "loong64", "ppc64le"} {
		t.Run(arch, func(t *testing.T) {
			_, err := lowerUnit(p, compile.TargetSpec{Architecture: arch}, compile.Func{ABI: abi.C})
			if !errors.Is(err, compile.ErrBackendLimit) {
				t.Fatalf("expected label parameter storage limit: %v", err)
			}
			required := compile.MaxSpillSlots + 1
			if arch == "386" {
				required++ // Each 64-bit label parameter occupies two target words.
			}
			want := fmt.Sprintf("spill storage needs %d words (reserved homes/parameters 4096), limit 4096: %v", required, compile.ErrBackendLimit)
			if err.Error() != want {
				t.Fatalf("error %q, want %q", err, want)
			}
		})
	}
}

// Promoted straight-line Vars need storage only while their values live, so
// the shapes that exceed fixed home storage compile.
func TestPromotedVarsFitSpillBudget(t *testing.T) {
	if os.Getenv("NYANJIT_INTERNAL_VAR_PROMOTION") == "0" {
		t.Skip("Var promotion is disabled")
	}
	for _, c := range []struct {
		name  string
		build func(int) *ir.SealedProgram
		size  int
	}{
		{"many-vars", workload.ManyVars, 4000},
		{"deep-scopes", workload.DeepScopes, 8000},
		{"sibling-scopes", siblingScopeVars, 4100},
	} {
		program := c.build(c.size)
		for _, arch := range []string{"arm64", "amd64"} {
			if _, err := lowerUnit(program, compile.TargetSpec{Architecture: arch}, compile.Func{ABI: abi.C}); err != nil {
				t.Errorf("%s/%s: %v", c.name, arch, err)
			}
		}
	}
}
