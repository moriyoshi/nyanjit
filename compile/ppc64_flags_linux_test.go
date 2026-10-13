//go:build linux && ppc64le

package compile_test

import (
	"fmt"
	"math/bits"
	"slices"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// ppcCPUSpec selects a POWER level; the unspecified level selects the
// default target CPU.
func ppcCPUSpec(level abi.PPC64CPU) compile.TargetSpec {
	if level == abi.PPC64CPUUnspecified {
		return compile.TargetSpec{}
	}
	return compile.TargetSpec{CPU: abi.CPUProfile{Level: level.String()}}
}

func ppcFlagCode(t *testing.T, b *nyanjit.Builder, predicate nyanjit.Predicate, cpu abi.PPC64CPU) (*execution.Code, string, abi.EmissionReport) {
	t.Helper()
	yes, no := b.NewLabel(), b.NewLabel()
	b.Branch(predicate, yes)
	b.Jump(no)
	b.Bind(yes)
	b.Return(b.Const(1))
	b.Bind(no)
	b.Return(b.Const(0))
	program := finishPPC(t, b)
	spec := ppcCPUSpec(cpu)
	options := compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 512}}
	for _, ins := range program.Instructions {
		if ins.Op == ir.OpCallC {
			options.Context = compile.ContextForeign
		}
	}
	listing, err := lowerListing(program, spec, options)
	if err != nil {
		t.Fatal(err)
	}
	template, err := lowerTemplate(program, spec, options)
	if err != nil {
		t.Fatal(err)
	}
	code, err := template.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = code.Close() })
	return code, listing, template.Report().Snapshot()
}

func assertNoPPCCompare(t *testing.T, listing string) {
	t.Helper()
	if strings.Contains(listing, ": cmp") {
		t.Fatalf("predicate did not reuse flags:\n%s", listing)
	}
}

func TestPPC64CRResultPredicates(t *testing.T) {
	cpu, _ := ppc64.HostCPU()
	for _, width := range []uint8{8, 16, 32, 64} {
		mask := ^uint64(0) >> (64 - width)
		for _, op := range []string{"add", "sub", "mul", "and", "or", "xor", "add-immediate", "sub-immediate"} {
			for _, test := range []struct {
				condition ir.BranchCondition
				want      func(uint64, int64) bool
			}{
				{ir.BranchEqual, func(x uint64, _ int64) bool { return x == 0 }},
				{ir.BranchNotEqual, func(x uint64, _ int64) bool { return x != 0 }},
				{ir.BranchLess, func(_ uint64, x int64) bool { return x < 0 }},
				{ir.BranchGreaterEqual, func(_ uint64, x int64) bool { return x >= 0 }},
				{ir.BranchLessEqual, func(_ uint64, x int64) bool { return x <= 0 }},
				{ir.BranchGreater, func(_ uint64, x int64) bool { return x > 0 }},
			} {
				t.Run(fmt.Sprintf("%d/%s/%d", width, op, test.condition), func(t *testing.T) {
					b := nyanjit.NewBuilder()
					x, y := b.ParamBits(width), b.ParamBits(width)
					var result nyanjit.Value
					eval := func(x, y uint64) uint64 { return 0 }
					switch op {
					case "add":
						result, eval = b.Add(x, y), func(x, y uint64) uint64 { return x + y }
					case "sub":
						result, eval = b.Sub(x, y), func(x, y uint64) uint64 { return x - y }
					case "mul":
						result, eval = b.Mul(x, y), func(x, y uint64) uint64 { return x * y }
					case "and":
						result, eval = b.And(x, y), func(x, y uint64) uint64 { return x & y }
					case "or":
						result, eval = b.Or(x, y), func(x, y uint64) uint64 { return x | y }
					case "xor":
						result, eval = b.Xor(x, y), func(x, y uint64) uint64 { return x ^ y }
					case "add-immediate":
						result, eval = b.Add(x, b.ConstBits(width, 7)), func(x, _ uint64) uint64 { return x + 7 }
					case "sub-immediate":
						result, eval = b.Sub(x, b.ConstBits(width, 7)), func(x, _ uint64) uint64 { return x - 7 }
					}
					code, listing, report := ppcFlagCode(t, b, b.CompareImmediate(result, test.condition, 0), cpu)
					assertNoPPCCompare(t, listing)
					if !report.FlagsClobbered || !slices.Contains(report.ClobberedSpecialRegisters, "CR0") {
						t.Fatalf("missing arithmetic CR effect: %+v", report)
					}
					for _, args := range [][2]uint64{{0, 0}, {1, 1}, {7, 9}, {mask, 1}, {mask, mask}, {mask >> 1, 1}, {1 << (width - 1), 3}} {
						result := eval(args[0]&mask, args[1]&mask) & mask
						signed := int64(result<<(64-width)) >> (64 - width)
						want := uint64(0)
						if test.want(result, signed) {
							want = 1
						}
						got, err := code.Call(uintptr(args[0]), uintptr(args[1]))
						if err != nil || uint64(got) != want {
							t.Fatalf("%#v: result=%#x got=%d,%v; want %d\n%s", args, result, got, err, want, listing)
						}
					}
				})
			}
		}
	}
}

func TestPPC64XERCarryAndBorrowPredicates(t *testing.T) {
	cpu, _ := ppc64.HostCPU()
	for _, width := range []uint8{8, 16, 32, 64} {
		mask := ^uint64(0) >> (64 - width)
		for _, sub := range []bool{false, true} {
			for _, negate := range []bool{false, true} {
				b := nyanjit.NewBuilder()
				x, y := b.ParamBits(width), b.ParamBits(width)
				result := b.Add(x, y)
				if sub {
					// Build only the tested arithmetic; a preceding unused
					// add must not be relied on for flags.
					b = nyanjit.NewBuilder()
					x, y = b.ParamBits(width), b.ParamBits(width)
					result = b.Sub(x, y)
				}
				predicate := b.Carry(result)
				if negate {
					predicate = predicate.Not()
				}
				code, listing, report := ppcFlagCode(t, b, predicate, cpu)
				reused := sub || width == 64 || width == 32 && cpu >= abi.PPC64Power9
				if reused {
					assertNoPPCCompare(t, listing)
					if !slices.Contains(report.ClobberedSpecialRegisters, "XER") || !slices.Contains(report.ClobberedSpecialRegisters, "CR7") {
						t.Fatalf("missing XER/CR7 effects: %+v", report)
					}
					if cpu >= abi.PPC64Power9 && !strings.Contains(listing, "mcrxrx cr7") {
						t.Fatalf("POWER9 carry did not use MCRXRX:\n%s", listing)
					}
					if cpu == abi.PPC64Power8 && !strings.Contains(listing, "mfxer ") {
						t.Fatalf("POWER8 carry did not preserve XER:\n%s", listing)
					}
				}
				for _, args := range [][2]uint64{{0, 0}, {0, 1}, {1, 0}, {1, 1}, {7, 9}, {mask, 1}, {mask, mask}, {mask >> 1, 1}, {1 << (width - 1), 3}} {
					x, y := args[0]&mask, args[1]&mask
					matches := (x+y)&mask < x
					if sub {
						matches = x < y
					}
					if negate {
						matches = !matches
					}
					want := uint64(0)
					if matches {
						want = 1
					}
					got, err := code.Call(uintptr(args[0]), uintptr(args[1]))
					if err != nil || uint64(got) != want {
						t.Fatalf("width %d sub=%t negate=%t args=%#v: %d,%v; want %d\n%s", width, sub, negate, args, got, err, want, listing)
					}
				}
			}
		}
	}
}

func TestPPC64ComposedZeroSignCarryPredicates(t *testing.T) {
	cpu, _ := ppc64.HostCPU()
	for _, width := range []uint8{32, 64} {
		if width == 32 && cpu == abi.PPC64Power8 {
			continue // Full CA is not 32-bit addition carry on POWER8.
		}
		mask := ^uint64(0) >> (64 - width)
		for _, carryFirst := range []bool{false, true} {
			b := nyanjit.NewBuilder()
			x, y := b.ParamBits(width), b.ParamBits(width)
			sum := b.Add(x, y)
			carry, zero, sign := b.Carry(sum), b.IsZero(sum), b.IsNegative(sum)
			predicate := zero.Or(sign).And(carry)
			if carryFirst {
				predicate = carry.And(zero.Or(sign))
			}
			code, listing, _ := ppcFlagCode(t, b, predicate, cpu)
			assertNoPPCCompare(t, listing)
			for _, args := range [][2]uint64{{0, 0}, {mask, 1}, {mask, mask}, {1 << (width - 1), 1 << (width - 1)}, {1, 2}, {mask - 1, 1}} {
				x, y := args[0]&mask, args[1]&mask
				sum := (x + y) & mask
				want := uint64(0)
				if sum < x && (sum == 0 || sum&(1<<(width-1)) != 0) {
					want = 1
				}
				if got, err := code.Call(uintptr(args[0]), uintptr(args[1])); err != nil || uint64(got) != want {
					t.Fatalf("width %d carryFirst=%t %#v: %d,%v; want %d\n%s", width, carryFirst, args, got, err, want, listing)
				}
			}
		}
	}
}

func TestPPC64DecrementLoopReusesCR0(t *testing.T) {
	cpu, _ := ppc64.HostCPU()
	// Raw IR: the countdown is a block parameter.
	r := newRawIR()
	pb := r.pb
	entry := pb.Entry()
	n := pb.Param(entry, ir.Scalar(64))
	loop, done := pb.NewBlock(ir.Scalar(64)), pb.NewBlock()
	pb.Jump(entry, loop, n)
	next := r.op(loop, ir.OpSub, 64, pb.BlockParam(loop, 0), r.constant(loop, 64, 1))
	pb.Branch(loop, &ir.PredicateExpr{Kind: ir.PredicateNot, Left: rawIsZero(next)}, loop, []int{next}, done, nil)
	pb.Return(done, r.constant(done, 64, 42))
	program := r.seal().Clone()
	spec, options := ppcCPUSpec(cpu), compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 512}}
	listing, err := lowerListing(program, spec, options)
	if err != nil {
		t.Fatal(err)
	}
	assertNoPPCCompare(t, listing)
	if !strings.Contains(listing, "addic.") || !strings.Contains(listing, "bne ") {
		t.Fatalf("countdown loop does not branch on arithmetic CR0:\n%s", listing)
	}
	code, err := nyanjit.Compile(program, spec, options)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, n := range []uint64{1, 2, 17, 1000} {
		if got, err := code.Call(uintptr(n)); err != nil || got != 42 {
			t.Fatalf("loop(%d)=%d,%v", n, got, err)
		}
	}
}

func TestPPC64FlagsInvalidateAfterShift(t *testing.T) {
	cpu, _ := ppc64.HostCPU()
	b := nyanjit.NewBuilder()
	x, y := b.Param(), b.Param()
	sum := b.Add(x, y)
	// SRADI changes XER.CA without establishing a useful carry predicate.
	shifted := b.ShiftRightArithmetic(sum, 1)
	// Retain the shift through an independent part of the predicate.
	predicate := b.Carry(sum).And(b.IsZero(shifted))
	code, listing, _ := ppcFlagCode(t, b, predicate, cpu)
	if !strings.Contains(listing, ": cmp") {
		t.Fatalf("shift left stale carry available:\n%s", listing)
	}
	for _, args := range [][2]uint64{{^uint64(0), 1}, {1, 2}, {^uint64(0), ^uint64(0)}} {
		sum, carry := bits.Add64(args[0], args[1], 0)
		want := uint64(0)
		if carry != 0 && int64(sum)>>1 == 0 {
			want = 1
		}
		if got, err := code.Call(uintptr(args[0]), uintptr(args[1])); err != nil || uint64(got) != want {
			t.Fatalf("shifted flags %#v: %d,%v; want %d", args, got, err, want)
		}
	}
}

func TestPPC64ImmediateBorrowWidthRules(t *testing.T) {
	cpu, _ := ppc64.HostCPU()
	for _, width := range []uint8{8, 16, 32, 64} {
		mask := ^uint64(0) >> (64 - width)
		for _, immediate := range []uint64{0, 1, 7, mask, mask - 6, 1 << (width - 1)} {
			b := nyanjit.NewBuilder()
			x := b.ParamBits(width)
			result := b.Sub(x, b.ConstBits(width, immediate))
			code, listing, _ := ppcFlagCode(t, b, b.Carry(result), cpu)
			if immediate != 0 && (immediate <= 7 || width == 64 || width == 32 && cpu >= abi.PPC64Power9) {
				assertNoPPCCompare(t, listing)
			}
			for _, value := range []uint64{0, 1, 7, immediate, immediate - 1, mask, mask - 1, 1 << (width - 1)} {
				want := uint64(0)
				if value&mask < immediate {
					want = 1
				}
				if got, err := code.Call(uintptr(value)); err != nil || uint64(got) != want {
					t.Fatalf("width %d sub(%#x,%#x) carry=%d,%v; want %d\n%s", width, value, immediate, got, err, want, listing)
				}
			}
		}
	}
}

func TestPPC64CallInvalidatesArithmeticFlags(t *testing.T) {
	cpu, _ := ppc64.HostCPU()
	// The C leaf deliberately changes both CR0 and XER, independent of JIT args.
	native := ppcNativeCode(t,
		ppc64.Instruction{Op: ppc64.Addi, A: 3, Imm: 42},
		ppc64.Instruction{Op: ppc64.Addic, A: 3, B: 3},
		ppc64.Instruction{Op: ppc64.Cmpli, B: 3, Bits: 64},
		ppc64.Instruction{Op: ppc64.Blr})
	memory, err := execution.Map(native)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = execution.Unmap(memory) })
	effect, err := abi.PreflightCall(abi.CallSpec{ABI: abi.C, ArgumentCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	b := nyanjit.NewBuilder()
	x, y := b.Param(), b.Param()
	sum := b.Add(x, y)
	b.CallCWithEffect(b.Const(uint64(execution.Entry(memory))), effect, sum)
	code, listing, _ := ppcFlagCode(t, b, b.Carry(sum).And(b.IsZero(sum)), cpu)
	if !strings.Contains(listing, ": cmp") {
		t.Fatalf("C call left stale arithmetic flags:\n%s", listing)
	}
	for _, args := range [][2]uint64{{0, 0}, {^uint64(0), 1}, {^uint64(0), ^uint64(0)}, {1, 2}} {
		sum, carry := bits.Add64(args[0], args[1], 0)
		want := uint64(0)
		if carry != 0 && sum == 0 {
			want = 1
		}
		if got, err := code.Call(uintptr(args[0]), uintptr(args[1])); err != nil || uint64(got) != want {
			t.Fatalf("post-call flags %#v=%d,%v; want %d\n%s", args, got, err, want, listing)
		}
	}
}

func TestPPC64LogicalOperationPreservesCarryPredicate(t *testing.T) {
	cpu, _ := ppc64.HostCPU()
	b := nyanjit.NewBuilder()
	x, y, maskValue := b.Param(), b.Param(), b.Param()
	sum := b.Add(x, y)
	logical := b.And(sum, maskValue)
	code, listing, _ := ppcFlagCode(t, b, b.Carry(sum).And(b.IsZero(logical)), cpu)
	assertNoPPCCompare(t, listing)
	for _, args := range [][3]uint64{{^uint64(0), 1, 7}, {^uint64(0), ^uint64(0), 1}, {1, 2, 0}, {^uint64(0), 2, 3}} {
		sum, carry := bits.Add64(args[0], args[1], 0)
		want := uint64(0)
		if carry != 0 && sum&args[2] == 0 {
			want = 1
		}
		if got, err := code.Call(uintptr(args[0]), uintptr(args[1]), uintptr(args[2])); err != nil || uint64(got) != want {
			t.Fatalf("logical carry %#v=%d,%v; want %d\n%s", args, got, err, want, listing)
		}
	}
}

func TestPPC64TruncatingSignExtensionDoesNotReuseOldZero(t *testing.T) {
	cpu, _ := ppc64.HostCPU()
	b := nyanjit.NewBuilder()
	x, y := b.Param(), b.Param()
	sum := b.Add(x, y)
	extended := b.SignExtend(sum, 8)
	code, listing, _ := ppcFlagCode(t, b, b.IsZero(sum).Not().And(b.IsZero(extended)), cpu)
	if !strings.Contains(listing, ": cmp") {
		t.Fatalf("truncation falsely aliased zero predicates:\n%s", listing)
	}
	for _, args := range [][2]uint64{{0, 0}, {255, 1}, {1, 2}, {256, 0}, {^uint64(0), 1}} {
		sum := args[0] + args[1]
		want := uint64(0)
		if sum != 0 && int8(sum) == 0 {
			want = 1
		}
		if got, err := code.Call(uintptr(args[0]), uintptr(args[1])); err != nil || uint64(got) != want {
			t.Fatalf("truncated predicate %#v=%d,%v; want %d\n%s", args, got, err, want, listing)
		}
	}
}

func TestPPC64ComparePreservesIndependentCarry(t *testing.T) {
	cpu, _ := ppc64.HostCPU()
	b := nyanjit.NewBuilder()
	x, y, threshold := b.Param(), b.Param(), b.Param()
	sum := b.Add(x, y)
	predicate := b.Compare(x, ir.BranchBelow, threshold).And(b.Carry(sum))
	code, listing, _ := ppcFlagCode(t, b, predicate, cpu)
	if strings.Count(listing, ": cmp") != 1 {
		t.Fatalf("independent comparison lost surviving XER carry:\n%s", listing)
	}
	for _, args := range [][3]uint64{{^uint64(0) - 1, 7, ^uint64(0)}, {1, 2, 5}, {0, ^uint64(0), 1}, {^uint64(0), 1, 7}} {
		_, carry := bits.Add64(args[0], args[1], 0)
		want := uint64(0)
		if args[0] < args[2] && carry != 0 {
			want = 1
		}
		if got, err := code.Call(uintptr(args[0]), uintptr(args[1]), uintptr(args[2])); err != nil || uint64(got) != want {
			t.Fatalf("independent carry %#v=%d,%v; want %d\n%s", args, got, err, want, listing)
		}
	}
}
