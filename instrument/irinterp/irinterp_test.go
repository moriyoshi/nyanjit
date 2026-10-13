package irinterp_test

import (
	"errors"
	"math"
	"testing"

	"github.com/moriyoshi/nyanjit/instrument/irinterp"
	"github.com/moriyoshi/nyanjit/ir"
)

func run(t *testing.T, p *ir.Program, args []uint64, opts *irinterp.Options) irinterp.Result {
	t.Helper()
	r, err := irinterp.Run(p, args, opts)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestResultMatches(t *testing.T) {
	exact := irinterp.Result{Lo: 0xff, Bits: 8}
	if !exact.Matches(0x12ff, 7) || exact.Matches(0xfe, 0) {
		t.Fatal("exact results compare at their width")
	}
	nan := irinterp.Result{Lo: 0x7fc00000, Bits: 32, NaN: true}
	if !nan.Matches(0xffc00001, 0) || nan.Matches(0x3f800000, 0) {
		t.Fatal("NaN results match any NaN of their width")
	}
	wide := irinterp.Result{Lo: 1, Hi: 2, Bits: 128}
	if !wide.Matches(1, 2) || wide.Matches(1, 3) {
		t.Fatal("two-part results compare both words")
	}
	if !(irinterp.Result{Unspecified: true}).Matches(42, 42) {
		t.Fatal("unspecified results match anything")
	}
}

func TestNaNPayloads(t *testing.T) {
	// 0/0 is a NaN with a target-chosen payload; its bits are unspecified
	// once integer arithmetic reads them.
	p := &ir.Program{Instructions: []ir.Instruction{
		{Op: ir.OpParam, Bits: 64}, {Op: ir.OpFDiv, Bits: 64, A: 0, B: 0}, {Op: ir.OpAdd, A: 1, B: 0},
	}, Arity: 1, Result: 1}
	if r := run(t, p, []uint64{0}, nil); !r.NaN || r.Unspecified {
		t.Fatalf("0/0: %v", r)
	}
	if r := run(t, p, []uint64{math.Float64bits(2)}, nil); r.NaN || r.Lo != math.Float64bits(1) {
		t.Fatalf("2/2: %v", r)
	}
	p.Result = 2
	if r := run(t, p, []uint64{0}, nil); !r.Unspecified {
		t.Fatalf("integer use of a NaN: %v", r)
	}
}

// Unknown bits stay unknown through floating-point operations, whether the
// reference's stand-in bits for them happen to encode a NaN or a finite value.
func TestNaNDerivedBitsStayUnknownThroughFloatOperations(t *testing.T) {
	for _, test := range []struct {
		name   string
		change ir.Instruction // integer arithmetic on the NaN product's bits
	}{
		// 0x7fc00000+1 is still a NaN; a target's NaN may wrap to 0 instead.
		{"still NaN", ir.Instruction{Op: ir.OpAdd, Bits: 32, A: 1, B: 2}},
		// 0x7fc00000^0x7f800000 is a finite subnormal.
		{"finite", ir.Instruction{Op: ir.OpXor, Bits: 32, A: 1, B: 3}},
	} {
		p := &ir.Program{Instructions: []ir.Instruction{
			{Op: ir.OpParam, Bits: 32}, {Op: ir.OpFMul, Bits: 32, A: 0, B: 0},
			{Op: ir.OpConst, Bits: 32, Imm: 1}, {Op: ir.OpConst, Bits: 32, Imm: 0x7f800000},
			test.change, {Op: ir.OpFConvert, Bits: 64, A: 4},
		}, Arity: 1, Result: 5}
		if r := run(t, p, []uint64{0xffffffff}, nil); !r.Unspecified || r.NaN {
			t.Errorf("%s: %+v, want unspecified", test.name, r)
		}
	}
	// A NaN from exact operands is still a NaN.
	p := &ir.Program{Instructions: []ir.Instruction{
		{Op: ir.OpParam, Bits: 32}, {Op: ir.OpFMul, Bits: 32, A: 0, B: 0}, {Op: ir.OpFConvert, Bits: 64, A: 1},
	}, Arity: 1, Result: 2}
	if r := run(t, p, []uint64{0xffffffff}, nil); !r.NaN || r.Unspecified {
		t.Errorf("exact NaN: %+v", r)
	}
}

func TestCallsNeedHook(t *testing.T) {
	p := &ir.Program{Instructions: []ir.Instruction{
		{Op: ir.OpParam, Bits: 64}, {Op: ir.OpCallC, Bits: 32, Args: []int{0, 0}},
	}, Arity: 1, Result: 1}
	if _, err := irinterp.Run(p, []uint64{1}, nil); !errors.Is(err, irinterp.ErrUnsupported) {
		t.Fatalf("got %v, want ErrUnsupported", err)
	}
	hook := func(c irinterp.Call) (uint64, error) {
		if c.Op != ir.OpCallC || c.Target != 7 || len(c.Args) != 1 || c.Args[0] != 7 {
			t.Fatalf("call %+v", c)
		}
		return 0x1_0000_0002, nil
	}
	if r := run(t, p, []uint64{7}, &irinterp.Options{Call: hook}); r.Lo != 2 || r.Bits != 32 {
		t.Fatalf("got %v, want the hook result truncated to 32 bits", r)
	}
}

func TestStepLimit(t *testing.T) {
	// A loop that never exits.
	p := &ir.Program{Instructions: []ir.Instruction{{Op: ir.OpConst}}, Result: -1, Control: &ir.Control{
		Labels: []bool{true, true}, LabelScopes: []int{0, 0}, Params: []int{0, 0}, ParamBits: [][]uint8{{}, {}},
		Steps: []ir.ControlStep{
			{IsLabel: true, Label: 0}, {Term: ir.TermJump, To: 1, Start: 0, End: 1},
			{IsLabel: true, Label: 1}, {Term: ir.TermJump, To: 1, Start: 1, End: 1},
		},
	}}
	if _, err := irinterp.Run(p, nil, &irinterp.Options{MaxSteps: 1000}); !errors.Is(err, irinterp.ErrStepLimit) {
		t.Fatalf("got %v, want ErrStepLimit", err)
	}
}

func TestArgumentsTruncateToParameterWidth(t *testing.T) {
	p := &ir.Program{Instructions: []ir.Instruction{
		{Op: ir.OpParam, Bits: 16}, {Op: ir.OpCastUnsigned, Bits: 64, A: 0},
	}, Arity: 1, Result: 1}
	if r := run(t, p, []uint64{0x12345}, nil); r.Lo != 0x2345 {
		t.Fatalf("got %v", r)
	}
}
