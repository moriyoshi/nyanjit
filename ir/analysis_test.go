package ir_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/ir"
)

func analysisFixture(t *testing.T) *ir.SealedProgram {
	t.Helper()
	b := builder.NewBuilder()
	must := func(v builder.Value, err error) builder.Value {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	x := must(b.ParamNative())
	acc, err := b.Var(must(b.ConstNative(0)))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Enter(); err != nil {
		t.Fatal(err)
	}
	inner, err := b.Var(x)
	if err != nil {
		t.Fatal(err)
	}
	sum := must(b.Add(must(b.Get(acc)), must(b.Get(inner))))
	if err := b.Set(acc, sum); err != nil {
		t.Fatal(err)
	}
	if err := b.Leave(); err != nil {
		t.Fatal(err)
	}
	if err := b.Return(must(b.Get(acc))); err != nil {
		t.Fatal(err)
	}
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Analyze on a program's raw copy agrees with the analysis sealed with it,
// and the queries describe the program's scopes, variables and values.
func TestAnalysisQueries(t *testing.T) {
	sealed := analysisFixture(t)
	a := sealed.Analysis()
	b, err := ir.Analyze(sealed.Clone())
	if err != nil {
		t.Fatal(err)
	}
	n := sealed.Len()
	if len(sealed.ScopeExits()) == 0 || len(sealed.Variables()) < 2 {
		t.Fatalf("fixture lacks scopes or variables: %d scopes, %d variables", len(sealed.ScopeExits()), len(sealed.Variables()))
	}
	if start, end := a.Bounds(0); start != 0 || end != n {
		t.Fatalf("root bounds [%d,%d), want [0,%d)", start, end, n)
	}
	if parent, ok := a.Parent(0); !ok || parent != -1 {
		t.Fatalf("root parent %d %v", parent, ok)
	}
	scopes := a.Scopes()
	if len(scopes) != 1+len(sealed.ScopeExits()) || scopes[0] != 0 || !slices.IsSorted(scopes) {
		t.Fatalf("scopes %v", scopes)
	}
	for _, exit := range sealed.ScopeExits() {
		if parent, ok := a.Parent(exit.ID); !ok || parent != exit.Parent {
			t.Fatalf("scope %d parent %d %v", exit.ID, parent, ok)
		}
		if start, end := a.Bounds(exit.ID); start != exit.Enter+1 || end != exit.Position {
			t.Fatalf("scope %d bounds [%d,%d)", exit.ID, start, end)
		}
		if a.ScopeAt(exit.Enter+1) != exit.ID {
			t.Fatalf("scope at %d = %d, want %d", exit.Enter+1, a.ScopeAt(exit.Enter+1), exit.ID)
		}
	}
	for id, v := range sealed.Variables() {
		if !slices.Contains(a.Locals(v.Region), id) {
			t.Fatalf("variable %d missing from scope %d locals %v", id, v.Region, a.Locals(v.Region))
		}
		for _, pos := range a.VarUses(id) {
			ins := sealed.Instructions()[pos]
			if (ins.Op != ir.OpGet && ins.Op != ir.OpSet) || ins.Variable != id {
				t.Fatalf("variable %d use at %d is %v", id, pos, ins.Op)
			}
		}
	}
	params := 0
	for id, ins := range sealed.Instructions() {
		want := -1
		if ins.Op == ir.OpParam {
			want = params
			params++
		}
		if a.ParamIndex(id) != want {
			t.Fatalf("param index of %d = %d, want %d", id, a.ParamIndex(id), want)
		}
		if a.LastUse(id) < id {
			t.Fatalf("last use of %d is %d", id, a.LastUse(id))
		}
		for _, pos := range a.Uses(id) {
			if pos <= id || pos > a.LastUse(id) {
				t.Fatalf("use of %d at %d outside (%d,%d]", id, pos, id, a.LastUse(id))
			}
		}
	}
	if a.HasLoop() || !reflect.DeepEqual(a.EntryParamBits(), []uint8{uint8(builder.NativeBits)}) {
		t.Fatalf("loop %v, entry bits %v", a.HasLoop(), a.EntryParamBits())
	}
	for name, query := range map[string]func(*ir.Analysis) any{
		"scopes":   func(x *ir.Analysis) any { return x.Scopes() },
		"bounds":   func(x *ir.Analysis) any { return x.ScopeBounds() },
		"floating": func(x *ir.Analysis) any { return x.FloatingVariables() },
		"entry":    func(x *ir.Analysis) any { return x.EntryParamBits() },
		"last": func(x *ir.Analysis) any {
			out := make([]int, n)
			for i := range out {
				out[i] = x.LastUse(i)
			}
			return out
		},
	} {
		if got, want := query(b), query(a); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: Analyze %v, sealed %v", name, got, want)
		}
	}
}

// Analyze does not validate, but a malformed program must not panic.
func TestAnalyzeMalformedProgram(t *testing.T) {
	malformed := &ir.Program{
		Variables:    []ir.Variable{{Region: 0}},
		Instructions: []ir.Instruction{{Op: ir.OpGet, Variable: 7}},
	}
	if _, err := ir.Analyze(malformed); !errors.Is(err, ir.ErrInvalidProgram) {
		t.Fatalf("Analyze(malformed) = %v, want ErrInvalidProgram", err)
	}
	if _, err := ir.Analyze(nil); !errors.Is(err, ir.ErrInvalidProgram) {
		t.Fatalf("Analyze(nil) = %v", err)
	}
}

// SealOwned borrows the caller's storage; Seal copies it.
func TestSealOwnedBorrowsStorage(t *testing.T) {
	raw := analysisFixture(t).Clone()
	copied, err := ir.Seal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if &copied.Instructions()[0] == &raw.Instructions[0] {
		t.Fatal("Seal shares the caller's storage")
	}
	owned, err := ir.SealOwned(raw)
	if err != nil {
		t.Fatal(err)
	}
	if &owned.Instructions()[0] != &raw.Instructions[0] || owned.Analysis() == nil {
		t.Fatal("SealOwned copied the program")
	}
	if (&ir.SealedProgram{}).Analysis() != nil || (*ir.SealedProgram)(nil).Analysis() != nil {
		t.Fatal("zero sealed program has an analysis")
	}
	if _, err := ir.SealOwned(&ir.Program{Result: 3}); !errors.Is(err, ir.ErrInvalidProgram) {
		t.Fatalf("SealOwned(invalid) = %v", err)
	}
}
