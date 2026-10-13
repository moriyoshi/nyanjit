package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestExpiredSelectionsStayPoisonedAfterReuse(t *testing.T) {
	var w selectionWorkspace
	target := incSelectionTarget{arch: "amd64", bits: 64}
	ops := []ir.Instruction{{Op: op.OpConst, Imm: 1}}
	selected, err := selectProgramBuffers(ops, nil, 0, 0, nil, 8, target, w.lease())
	if err != nil {
		t.Fatal(err)
	}
	w.reset()
	ops[0].Imm = 2
	if _, err = selectProgramBuffers(ops, nil, 0, 0, nil, 8, target, w.lease()); err != nil {
		t.Fatal(err)
	}
	if scratchPoison && (selected.Values[0].Bits != 255 || selected.Instructions[0].Op != 255) {
		t.Fatal("re-selection overwrote expired selection poison")
	}
}
