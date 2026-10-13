package nyanjit_test

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// narrowSignCase compares x+1 (32-bit arithmetic) with zero after an optional
// conversion. Predicates read the register image: a 32-bit value is
// zero-extended at the register width, so on 64-bit targets the sign flag of
// the 32-bit addition does not decide it.
type narrowSignCase struct {
	conversion string // "none", "sext32" (SignExtend Bits 32), "zext64", "sext64"
	condition  ir.BranchCondition
	forward    bool // pass the value to 64-bit consumers on both edges
}

func (c narrowSignCase) String() string {
	return fmt.Sprintf("%s cond%d forward=%v", c.conversion, c.condition, c.forward)
}

func (c narrowSignCase) build() (*ir.SealedProgram, error) {
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	x := pb.Param(entry, ir.Scalar(32))
	one := pb.Append(entry, ir.Instruction{Op: ir.OpConst, Bits: 32, Imm: 1})
	v := pb.Append(entry, ir.Instruction{Op: ir.OpAdd, Bits: 32, A: x, B: one})
	bits := uint8(32)
	switch c.conversion {
	case "sext32":
		v = pb.Append(entry, ir.Instruction{Op: ir.OpSignExtend, Bits: 32, A: v, Imm: 32})
	case "zext64":
		v, bits = pb.Append(entry, ir.Instruction{Op: ir.OpCastUnsigned, Bits: 64, A: v}), 64
	case "sext64":
		v, bits = pb.Append(entry, ir.Instruction{Op: ir.OpSignExtend, Bits: 64, A: v, Imm: 32}), 64
	}
	predicate := &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: c.condition, Value: v}
	if !c.forward {
		yes, no := pb.NewBlock(), pb.NewBlock()
		pb.Branch(entry, predicate, yes, nil, no, nil)
		pb.Return(yes, pb.Append(yes, ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: 1}))
		pb.Return(no, pb.Append(no, ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: 0}))
		return pb.Seal()
	}
	yes, no := pb.NewBlock(ir.Scalar(bits)), pb.NewBlock(ir.Scalar(bits))
	pb.Branch(entry, predicate, yes, []int{v}, no, []int{v})
	p := pb.BlockParam(yes, 0)
	wide := pb.Append(yes, ir.Instruction{Op: ir.OpCastUnsigned, Bits: 64, A: p})
	mark := pb.Append(yes, ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: 1 << 32})
	pb.Return(yes, pb.Append(yes, ir.Instruction{Op: ir.OpAdd, Bits: 64, A: wide, B: mark}))
	q := pb.BlockParam(no, 0)
	pb.Return(no, pb.Append(no, ir.Instruction{Op: ir.OpCastUnsigned, Bits: 64, A: q}))
	return pb.Seal()
}

// want computes the result for a target with registerBits-wide registers.
func (c narrowSignCase) want(x uint32, registerBits int) uint64 {
	sum := x + 1
	image := uint64(sum)
	if c.conversion == "sext64" {
		image = uint64(int64(int32(sum)))
	}
	signed := int64(image)
	if registerBits == 32 && (c.conversion == "none" || c.conversion == "sext32") {
		signed = int64(int32(sum)) // a one-word value compares at 32 bits
	}
	var taken bool
	switch c.condition {
	case ir.BranchLess:
		taken = signed < 0
	case ir.BranchLessEqual:
		taken = signed <= 0
	case ir.BranchGreater:
		taken = signed > 0
	default:
		taken = signed >= 0
	}
	switch {
	case !c.forward && taken:
		return 1
	case !c.forward:
		return 0
	case taken:
		return image + 1<<32
	}
	return image
}

func TestNarrowSignedZeroComparisons(t *testing.T) {
	registerBits := 64
	if runtime.GOARCH == "386" {
		registerBits = 32
	}
	for _, conversion := range []string{"none", "sext32", "zext64", "sext64"} {
		for _, condition := range []ir.BranchCondition{ir.BranchLess, ir.BranchLessEqual, ir.BranchGreater, ir.BranchGreaterEqual} {
			for _, forward := range []bool{false, true} {
				c := narrowSignCase{conversion, condition, forward}
				program, err := c.build()
				if err != nil {
					t.Fatalf("%v: %v", c, err)
				}
				for _, arch := range []string{"amd64", "arm64", "386", "riscv64", "loong64", "ppc64le"} {
					target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch})
					if err != nil {
						t.Fatal(err)
					}
					unit, err := compile.Lower(target, program, compile.Func{ABI: abi.C})
					if err == nil {
						_, err = unit.Listing()
					}
					if err != nil {
						t.Errorf("%v on %s: %v", c, arch, err)
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
					code, err := nyanjit.Compile(program, compile.TargetSpec{}, f)
					if err != nil {
						t.Fatalf("%v %+v: %v", c, f, err)
					}
					for _, x := range []uint32{0, 1, 0x7ffffffe, 0x7fffffff, 0x80000000, 0xfffffffe, 0xffffffff} {
						lo, hi, err := code.CallPair(execution.Words(x)...)
						if got, want := execution.Wide(lo, hi), c.want(x, registerBits); err != nil || got != want {
							t.Errorf("%v ABI%d stack%d: f(%#x) = %#x, %v; want %#x", c, f.ABI, f.Stack.Practice, x, execution.Wide(lo, hi), err, c.want(x, registerBits))
						}
					}
					code.Close()
				}
			}
		}
	}
}
