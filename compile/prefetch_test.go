package compile_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// TestPrefetchListing lowers a read and a write prefetch on every target and
// tier: neither is dropped, though neither defines a used value.
func TestPrefetchListing(t *testing.T) {
	read := map[string]string{"amd64": "prefetcht0", "386": "prefetcht0", "arm64": "pldl1keep", "riscv64": "prefetch.r", "loong64": "preld 0,", "ppc64le": "dcbt 0,"}
	write := map[string]string{"amd64": "prefetcht0", "386": "prefetcht0", "arm64": "pstl1keep", "riscv64": "prefetch.w", "loong64": "preld 8,", "ppc64le": "dcbtst 0,"}
	for arch := range read {
		bits := uint8(64)
		if arch == "386" {
			bits = 32
		}
		program, err := ir.Seal(&ir.Program{Arity: 1, Result: 4, Instructions: []ir.Instruction{
			{Op: ir.OpParam, Bits: bits},
			{Op: ir.OpPrefetch, A: 0, Imm: uint64(ir.PrefetchRead)},
			{Op: ir.OpConst, Bits: bits},
			{Op: ir.OpPrefetch, A: 2, Imm: uint64(ir.PrefetchWrite)},
			{Op: ir.OpConst, Imm: 7},
		}})
		if err != nil {
			t.Fatal(err)
		}
		for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
			target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch, Tier: tier})
			if err != nil {
				t.Fatal(err)
			}
			unit, err := compile.Lower(target, program, compile.Func{ABI: abi.C})
			if err != nil {
				t.Fatalf("%s tier %d: %v", arch, tier, err)
			}
			listing, err := unit.Listing()
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if read[arch] == write[arch] {
				want = 2
			}
			if strings.Count(listing, read[arch]) != want || strings.Count(listing, write[arch]) != want {
				t.Errorf("%s tier %d: want %q and %q in\n%s", arch, tier, read[arch], write[arch], listing)
			}
			if r := unit.Report(); r.MayReadMemory() || r.MayWriteMemory() {
				t.Errorf("%s tier %d: a prefetch reported a memory access", arch, tier)
			}
		}
	}
}

// TestPrefetchValidation rejects a prefetch without a PrefetchKind, a width
// or a 32- or 64-bit address.
func TestPrefetchValidation(t *testing.T) {
	for _, ins := range []ir.Instruction{
		{Op: ir.OpPrefetch, A: 0, Imm: 2},
		{Op: ir.OpPrefetch, A: 0, Bits: 64},
		{Op: ir.OpPrefetch, A: 1},
		{Op: ir.OpPrefetch, A: 0, ByteOrder: ir.ByteOrderLittleEndian},
	} {
		_, err := ir.Seal(&ir.Program{Arity: 1, Result: 0, Instructions: []ir.Instruction{
			{Op: ir.OpParam},
			{Op: ir.OpConst, Bits: 16},
			ins,
		}})
		if !errors.Is(err, ir.ErrInvalidProgram) {
			t.Errorf("%+v: got %v", ins, err)
		}
	}
}
