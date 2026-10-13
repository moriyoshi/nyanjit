package irinterp_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/instrument/irinterp"
	"github.com/moriyoshi/nyanjit/ir"
)

// Each Run gets zeroed areas at the alignment they declare: the read
// before the write sees zero although the previous Run wrote x.
func TestStackAreasZeroedAndAligned(t *testing.T) {
	p := &ir.Program{Arity: 1, StackAreas: []ir.StackArea{{Bytes: 1, Align: 1}, {Bytes: 24, Align: 16}}}
	p.Instructions = []ir.Instruction{
		{Op: ir.OpParam},
		{Op: ir.OpStackAddress, Imm: 1},
		{Op: ir.OpLoad64, A: 1},
		{Op: ir.OpStore64, A: 1, B: 0},
		{Op: ir.OpConst, Imm: 15},
		{Op: ir.OpAnd, A: 1, B: 4},
		{Op: ir.OpOr, A: 2, B: 5},
	}
	p.Result = 6
	sealed, err := ir.Seal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range []uint64{0x55, 0x77} {
		r, err := irinterp.Run(sealed, []uint64{x}, nil)
		if err != nil || !r.Matches(0, 0) {
			t.Fatalf("Run(%#x) = %v, %v", x, r, err)
		}
	}
}
