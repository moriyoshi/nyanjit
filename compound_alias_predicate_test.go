package nyanjit_test

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// aliasPredicateCase branches on a compound predicate over two IR values
// naming the same register image: sum = x op 1 and copy = CastUnsigned64(sum).
// The arithmetic flags decide both comparisons, but hardened code compares
// explicitly and needs both operands.
type aliasPredicateCase struct {
	op        ir.Opcode
	shape     string // "and", "or", "not" (Not(Or)), "notleft" (Or(Not, ...))
	condition ir.BranchCondition
	forward   bool // pass sum on the yes edge and copy on the no edge
}

func (c aliasPredicateCase) String() string {
	return fmt.Sprintf("op%d %s cond%d forward=%v", c.op, c.shape, c.condition, c.forward)
}

func (c aliasPredicateCase) build() (*ir.SealedProgram, error) {
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	x := pb.Param(entry, ir.Scalar(64))
	one := pb.Append(entry, ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: 1})
	sum := pb.Append(entry, ir.Instruction{Op: c.op, Bits: 64, A: x, B: one})
	copied := pb.Append(entry, ir.Instruction{Op: ir.OpCastUnsigned, Bits: 64, A: sum})
	compare := func(v int) *ir.PredicateExpr {
		return &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: c.condition, Value: v}
	}
	var predicate *ir.PredicateExpr
	switch c.shape {
	case "and":
		predicate = &ir.PredicateExpr{Kind: ir.PredicateAnd, Left: compare(copied), Right: compare(sum)}
	case "or":
		predicate = &ir.PredicateExpr{Kind: ir.PredicateOr, Left: compare(copied), Right: compare(sum)}
	case "not":
		predicate = &ir.PredicateExpr{Kind: ir.PredicateNot, Left: &ir.PredicateExpr{Kind: ir.PredicateOr, Left: compare(copied), Right: compare(sum)}}
	default:
		predicate = &ir.PredicateExpr{Kind: ir.PredicateOr, Left: &ir.PredicateExpr{Kind: ir.PredicateNot, Left: compare(copied)}, Right: compare(sum)}
	}
	if !c.forward {
		yes, no := pb.NewBlock(), pb.NewBlock()
		pb.Branch(entry, predicate, yes, nil, no, nil)
		pb.Return(yes, pb.Append(yes, ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: 1}))
		pb.Return(no, pb.Append(no, ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: 0}))
		return pb.Seal()
	}
	yes, no := pb.NewBlock(ir.Scalar(64)), pb.NewBlock(ir.Scalar(64))
	pb.Branch(entry, predicate, yes, []int{sum}, no, []int{copied})
	mark := pb.Append(yes, ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: 1 << 40})
	pb.Return(yes, pb.Append(yes, ir.Instruction{Op: ir.OpAdd, Bits: 64, A: pb.BlockParam(yes, 0), B: mark}))
	pb.Return(no, pb.BlockParam(no, 0))
	return pb.Seal()
}

func (c aliasPredicateCase) want(x uint64) uint64 {
	var value uint64
	switch c.op {
	case ir.OpAdd:
		value = x + 1
	case ir.OpSub:
		value = x - 1
	default:
		value = x ^ 1
	}
	signed := int64(value)
	var holds bool
	switch c.condition {
	case ir.BranchLess:
		holds = signed < 0
	case ir.BranchLessEqual:
		holds = signed <= 0
	case ir.BranchGreater:
		holds = signed > 0
	default:
		holds = signed >= 0
	}
	// Both comparisons read the same value.
	taken := holds
	switch c.shape {
	case "not":
		taken = !holds
	case "notleft":
		taken = true // !holds || holds
	}
	switch {
	case !c.forward && taken:
		return 1
	case !c.forward:
		return 0
	case taken:
		return value + 1<<40
	}
	return value
}

func aliasPredicateCases() []aliasPredicateCase {
	var cases []aliasPredicateCase
	for _, op := range []ir.Opcode{ir.OpAdd, ir.OpSub, ir.OpXor} {
		for _, shape := range []string{"and", "or", "not", "notleft"} {
			for _, condition := range []ir.BranchCondition{ir.BranchLess, ir.BranchLessEqual, ir.BranchGreater, ir.BranchGreaterEqual} {
				for _, forward := range []bool{false, true} {
					cases = append(cases, aliasPredicateCase{op, shape, condition, forward})
				}
			}
		}
	}
	return cases
}

// TestCompoundPredicateOverAliases lowers every case on all six targets with
// and without hardening, and runs it natively with both ABIs and stack
// practices. Targets that cannot harden control flow report ErrUnsupported.
func TestCompoundPredicateOverAliases(t *testing.T) {
	inputs := []uint64{0, 1, 2, 0x7ffffffffffffffe, 0x7fffffffffffffff, 0x8000000000000000, 0xfffffffffffffffe, 0xffffffffffffffff}
	for _, c := range aliasPredicateCases() {
		program, err := c.build()
		if err != nil {
			t.Fatalf("%v: %v", c, err)
		}
		for _, harden := range []bool{false, true} {
			for _, arch := range []string{"amd64", "arm64", "386", "riscv64", "loong64", "ppc64le"} {
				target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch})
				if err != nil {
					t.Fatal(err)
				}
				unit, err := compile.Lower(target, program, compile.Func{ABI: abi.C, Harden: harden})
				if err == nil {
					_, err = unit.Listing()
				}
				if err != nil && !(harden && errors.Is(err, compile.ErrUnsupported)) {
					t.Errorf("%v harden=%v on %s: %v", c, harden, arch, err)
				}
			}
			if runtime.GOOS != "linux" {
				continue
			}
			for _, f := range []compile.Func{
				{ABI: abi.C},
				{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 240}},
				{ABI: abi.Go},
				{ABI: abi.Go, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 240}},
			} {
				f.Harden = harden
				code, err := nyanjit.Compile(program, compile.TargetSpec{}, f)
				if harden && errors.Is(err, compile.ErrUnsupported) {
					continue
				}
				if err != nil {
					t.Errorf("%v %+v: %v", c, f, err)
					continue
				}
				for _, x := range inputs {
					lo, hi, err := code.CallPair(execution.Words(x)...)
					if got, want := execution.Wide(lo, hi), c.want(x); err != nil || got != want {
						t.Errorf("%v %+v: f(%#x) = %#x, %v; want %#x", c, f, x, got, err, want)
					}
				}
				code.Close()
			}
		}
	}
}

// TestCompoundPredicateOverAliasesKeepsFlagBranches checks that ordinary code
// still decides both comparisons from the addition's flags.
func TestCompoundPredicateOverAliasesKeepsFlagBranches(t *testing.T) {
	program, err := aliasPredicateCase{ir.OpAdd, "or", ir.BranchLess, false}.build()
	if err != nil {
		t.Fatal(err)
	}
	for arch, compare := range map[string]string{"amd64": "cmp", "arm64": "cmp"} {
		target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch})
		if err != nil {
			t.Fatal(err)
		}
		for _, harden := range []bool{false, true} {
			unit, err := compile.Lower(target, program, compile.Func{ABI: abi.C, Harden: harden})
			if err != nil {
				t.Fatalf("%s harden=%v: %v", arch, harden, err)
			}
			listing, err := unit.Listing()
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(strings.ToLower(listing), compare); got != harden {
				t.Errorf("%s harden=%v: listing has %s = %v\n%s", arch, harden, compare, got, listing)
			}
		}
	}
}
