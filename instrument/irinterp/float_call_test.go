package irinterp_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/instrument/irinterp"
	"github.com/moriyoshi/nyanjit/ir"
)

// TestCallCarriesFloats passes a call's float passing to Options.Call.
func TestCallCarriesFloats(t *testing.T) {
	floats := abi.FloatArgument(1) | abi.FloatResult32
	p := &ir.Program{Arity: 2, Instructions: []ir.Instruction{{Op: ir.OpParam}, {Op: ir.OpParam, Bits: 32}, {Op: ir.OpConst, Imm: 0x1000},
		{Op: ir.OpCallC, Bits: 32, Imm: uint64(floats), Args: []int{2, 0, 1}}}, Result: 3}
	var seen irinterp.Call
	r := run(t, p, []uint64{7, 0x3fc00000}, &irinterp.Options{Call: func(c irinterp.Call) (uint64, error) {
		seen = c
		return 0x1_4000_0000, nil
	}})
	if seen.Floats != floats || seen.Target != 0x1000 || len(seen.Args) != 2 || seen.Args[1] != 0x3fc00000 {
		t.Fatalf("call %+v", seen)
	}
	if r.Lo != 0x4000_0000 || r.Bits != 32 {
		t.Fatalf("result %+v, want the binary32 bits truncated to the call's width", r)
	}
}
