package compile_test

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// POWER9 remainders use mod*, POWER8 the divide-multiply-subtract formula;
// checked semantics guard the divisor with cmpdi and isel.
func TestPPC64DivisionCPUForms(t *testing.T) {
	if builder.NativeBits == 32 {
		t.Skip("64-bit builder values are paired on 32-bit hosts")
	}
	for _, level := range []abi.PPC64CPU{abi.PPC64Power8, abi.PPC64Power9} {
		for _, semantics := range []ir.DivisionSemantics{ir.DivisionZero, ir.DivisionUnchecked} {
			b := nyanjit.NewBuilder()
			x, y := b.ParamBits(64), b.ParamBits(64)
			b.Return(b.Xor(b.DivWith(semantics, x, y), b.RemWith(semantics, x, y)))
			listing, err := lowerListing(finishDivisionPPC(t, b), ppc64LevelSpec("ppc64le", level), compile.Func{})
			if err != nil {
				t.Fatal(err)
			}
			power9 := level >= abi.PPC64Power9
			if strings.Contains(listing, "modud ") != power9 || strings.Count(listing, "divdu ") != map[bool]int{false: 2, true: 1}[power9] {
				t.Fatalf("%s: remainder form:\n%s", level, listing)
			}
			if checked := semantics != ir.DivisionUnchecked; strings.Contains(listing, "cmpdi ") != checked || strings.Contains(listing, "isel ") != checked {
				t.Fatalf("%s semantics %d: divisor guard:\n%s", level, semantics, listing)
			}
		}
	}
}

func TestPPC64ConversionForms(t *testing.T) {
	if builder.NativeBits == 32 {
		t.Skip("64-bit builder values are paired on 32-bit hosts")
	}
	b := nyanjit.NewBuilder()
	x := b.ParamBits(64)
	b.Return(b.FToSigned(b.FFromUnsigned(x, 64), 64))
	listing, err := lowerListing(finishDivisionPPC(t, b), ppc64LevelSpec("ppc64le", abi.PPC64Power8), compile.Func{})
	if err != nil {
		t.Fatal(err)
	}
	for _, mnemonic := range []string{"fcfidu ", "fctidz ", "fcmpu ", "isel "} {
		if !strings.Contains(listing, mnemonic) {
			t.Fatalf("missing %s:\n%s", mnemonic, listing)
		}
	}
}

func finishDivisionPPC(t *testing.T, b *nyanjit.Builder) *ir.Program {
	t.Helper()
	program, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	return program
}
