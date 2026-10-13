package compile

import (
	"reflect"
	"sync"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/ir"
)

// PinnedScratchTarget resolves an uncached target whose lowerings all lease
// one scratch, so each lowering reuses (and, with nyanjit_scratch_poison,
// poisons) the storage the previous one released.
func PinnedScratchTarget(t *testing.T, spec TargetSpec) Target {
	t.Helper()
	target, err := resolveTarget(spec)
	if err != nil {
		t.Fatal(err)
	}
	scratch := new(compileScratch)
	target.scratch = &sync.Pool{New: func() any { return scratch }}
	return target
}

// UnitPhysicalKeeper copies u's physical program now and reports later
// whether the program still equals that copy.
func UnitPhysicalKeeper(u *Unit) func() bool {
	kept := u.physical.code.decoded()
	return func() bool { return reflect.DeepEqual(u.physical.code.decoded(), kept) }
}

// UnitPhysicalStorage reports where u's physical program is stored, its
// length, whether it loads literals and its last opcode.
func UnitPhysicalStorage(u *Unit) (unsafe.Pointer, int, bool, machine.Opcode) {
	code := &u.physical.code
	last, literals := machine.Opcode(0), false
	for n := range code.count() {
		last, literals = code.opAt(n), literals || code.opAt(n) == machine.OpLiteralLoad
	}
	storage := unsafe.Pointer(unsafe.SliceData(code.packed))
	if len(code.located) != 0 {
		storage = unsafe.Pointer(unsafe.SliceData(code.located))
	}
	return storage, code.count(), literals, last
}

// verifySelectedProgram leases its value tables from the selection buffers,
// and a reused, dirty or poisoned table gives the same verdict as a fresh one.
func TestVerifyTablesReuseSelectionBuffers(t *testing.T) {
	var w selectionWorkspace
	var target selectionTarget = incSelectionTarget{arch: "amd64", bits: 64}
	ops := []ir.Instruction{{Op: op.OpParam, Bits: 64}, {Op: op.OpConst, Imm: 3, Bits: 64}, {Op: op.OpAdd, A: 0, B: 1, Bits: 64}}
	if _, err := selectProgramBuffers(ops, nil, 1, 2, nil, 8, target, w.lease()); err != nil {
		t.Fatal(err)
	}
	b := w.selections[0]
	if cap(b.verify) < 8+2*len(ops) {
		t.Fatalf("verification used no selection table (cap %d)", cap(b.verify))
	}
	table := unsafe.SliceData(b.verify)
	w.reset()
	if scratchPoison && b.verify[:1][0] != 0x5a5a5a5a5a5a5a5a {
		t.Fatal("release did not poison the verification table")
	}
	for i := range b.verify[:cap(b.verify)] {
		b.verify[:cap(b.verify)][i] = ^uint64(0)
	}
	if _, err := selectProgramBuffers(ops, nil, 1, 2, nil, 8, target, w.lease()); err != nil {
		t.Fatalf("a dirty table changed the verdict: %v", err)
	}
	if !scratchPoison && unsafe.SliceData(b.verify) != table {
		t.Fatal("verification did not reuse its table")
	}
	// A wrong selection still fails with the same error on a reused table.
	selected, err := selectProgramBuffers(ops, nil, 1, 2, nil, 8, target, w.lease())
	if err != nil {
		t.Fatal(err)
	}
	selected.Instructions[len(selected.Instructions)-1].Arithmetic = op.OpSub
	fresh := verifySelectedProgram(ops, selected, 2, 8, 64, op.ByteOrderLittleEndian)
	if fresh == nil {
		t.Fatal("verification accepted a wrong selection")
	}
	if err := verifySelectedProgram(ops, selected, 2, 8, 64, op.ByteOrderLittleEndian, w.lease()); err == nil || err.Error() != fresh.Error() {
		t.Fatalf("reused table changed the mismatch error: %v, want %v", err, fresh)
	}
	if scratchPoison {
		return
	}
	if allocs := testing.AllocsPerRun(20, func() {
		w.reset()
		if _, err := selectProgramBuffers(ops, nil, 1, 2, nil, 8, target, w.lease()); err != nil {
			t.Fatal(err)
		}
	}); allocs != 0 {
		t.Fatalf("selection with reused buffers allocated %v times", allocs)
	}
}
