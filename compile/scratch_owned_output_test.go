package compile_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/workload"
	"github.com/moriyoshi/nyanjit/ir"
)

// A Unit keeps its physical program for Listing and Template, so the program
// must be owned output: lowerings that reuse the scratch afterwards must not
// change it. Each Unit is checked before its lazy outputs first run, so a
// program aliasing scratch would hold the later lowerings' instructions (or,
// with nyanjit_scratch_poison, poison) instead of its own.
func TestScratchReuseLeavesUnitPhysicalOwned(t *testing.T) {
	programs := []*ir.SealedProgram{workload.Chain(53), workload.FP(13), workload.Loops(19), workload.Scopes(17), workload.Pressure(24)}
	f := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}}
	for _, arch := range []string{"386", "amd64", "arm64", "riscv64", "ppc64le", "loong64"} {
		spec := compile.TargetSpec{Architecture: arch}
		pinned := compile.PinnedScratchTarget(t, spec)
		fresh, err := compile.NewTarget(spec)
		if err != nil {
			t.Fatal(err)
		}
		for i, p := range programs {
			u, err := compile.Lower(pinned, p, f)
			if err != nil {
				t.Fatalf("%s program %d: %v", arch, i, err)
			}
			unchanged := compile.UnitPhysicalKeeper(u)
			for _, other := range programs {
				if _, err := compile.Lower(pinned, other, f); err != nil {
					t.Fatal(err)
				}
			}
			if !unchanged() {
				t.Fatalf("%s program %d: later lowerings changed the Unit's physical program", arch, i)
			}
			want, err := compile.Lower(fresh, p, f)
			if err != nil {
				t.Fatal(err)
			}
			got, err := u.Listing()
			if err != nil {
				t.Fatal(err)
			}
			if listing, err := want.Listing(); err != nil || got != listing {
				t.Fatalf("%s program %d: listing after scratch reuse differs: %v", arch, i, err)
			}
		}
	}
}

// Finishing the literal pool appends its data instruction to the owned
// program, which reserves room for it instead of being copied.
func TestLiteralPoolFinishKeepsProgramStorage(t *testing.T) {
	f := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}}
	target, err := compile.NewTarget(compile.TargetSpec{Architecture: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	pools := 0
	for _, p := range []*ir.SealedProgram{workload.FP(13), workload.Vectors(8), workload.Vectors(40), workload.SHA256(0)} {
		u, err := compile.Lower(target, p, f)
		if err != nil {
			t.Fatal(err)
		}
		before, n, literals, _ := compile.UnitPhysicalStorage(u)
		if !literals {
			continue
		}
		pools++
		if _, err := u.Listing(); err != nil {
			t.Fatal(err)
		}
		after, m, _, last := compile.UnitPhysicalStorage(u)
		if m != n+1 || last != machine.OpLiteralData {
			t.Fatalf("finishing the pool did not append its data: %d -> %d, last %v", n, m, last)
		}
		if after != before {
			t.Fatal("finishing the pool copied the program")
		}
	}
	if pools == 0 {
		t.Fatal("no workload has an arm64 literal pool")
	}
}
