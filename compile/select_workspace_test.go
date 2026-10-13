package compile

import (
	"reflect"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestSelectionWorkspaceResetAndOwnedSelection(t *testing.T) {
	policy := newPPC64SelectionPolicy(t, "ppc64le", abi.C)
	ops := []ir.Instruction{{Op: ir.OpParam}, {Op: ir.OpConst, Imm: 3}, {Op: ir.OpAdd, A: 0, B: 1}}
	owned, err := testSelectProgram(ops, nil, 1, 2, nil, 8, policy)
	if err != nil {
		t.Fatal(err)
	}
	var w selectionWorkspace
	borrowed, err := selectProgramBuffers(ops, nil, 1, 2, nil, 8, policy, w.lease())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(borrowed, owned) {
		t.Fatal("workspace changes selection")
	}
	w.release()
	w.reset()
	again, err := selectProgramBuffers(ops, nil, 1, 2, nil, 8, policy, w.lease())
	if err != nil || !reflect.DeepEqual(again, owned) {
		t.Fatal("reset lost selection", err)
	}
	if n, big := w.retainedBytes(1); n == 0 || !big {
		t.Fatalf("retained size %d, large %v", n, big)
	}
	w.reset()
	w.trim(0)
	if n, _ := w.retainedBytes(1); n != 0 {
		t.Fatalf("unused workspace retains %d B", n)
	}
}
