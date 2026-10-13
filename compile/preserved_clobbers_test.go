package compile_test

import (
	"errors"
	"math/rand"
	"regexp"
	"slices"
	"strings"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// sequentialVarLoops is the reduced F14 program (security audit 2026-10-25):
// enough live values that cross-block values move through spill slots, and
// two Var-counted loops in sequence, so the first loop's exit label and the
// second loop's header label alias one block.
func sequentialVarLoops() (*ir.SealedProgram, error) {
	b := n.NewBuilder()
	base, x := b.ParamBits(64), b.ParamBits(64)
	b.ParamBits(64)
	load := func(off uint64) n.Value { return b.Xor(b.Load64(b.Add(base, b.Const(off))), x) }
	narrow := func(v n.Value, w uint8) n.Value { return b.CastUnsigned(v, w) }
	counter := func(a n.Value) n.Value { return b.Add(b.And(a, b.Const(3)), b.Const(1)) }
	v := map[int]n.Value{}
	v[0], v[1], v[2], v[3], v[4], v[5] = load(168), load(200), load(448), load(96), load(32), load(40)
	v[6], v[7] = counter(v[1]), counter(v[1])
	vars := []n.Var{
		b.Var(v[6]), b.Var(v[7]), b.Var(v[2]), b.Var(v[7]), b.Var(narrow(v[5], 16)),
		b.Var(v[6]), b.Var(narrow(v[6], 32)), b.Var(narrow(v[2], 8)), b.Var(narrow(v[2], 8)), b.Var(narrow(v[5], 32)),
	}
	for i, off := range []uint64{0, 8, 16, 24, 32, 48, 56, 56, 72, 72, 80} {
		v[8+i] = load(off)
	}
	b.Enter()
	b.Var(v[14])
	b.Set(vars[8], narrow(v[17], 8))
	b.Set(vars[9], narrow(v[14], 32))
	b.Leave()
	v[23], v[24] = load(96), load(104)
	loop := func(counter n.Var, body func()) {
		top, exit := b.NewLabel(), b.NewLabel()
		b.Bind(top)
		b.Branch(b.IsZero(b.Get(counter)), exit)
		body()
		b.Set(counter, b.Sub(b.Get(counter), b.Const(1)))
		b.Jump(top)
		b.Bind(exit)
	}
	mask62 := b.Const(^uint64(1 << 62))
	loop(vars[0], func() {})
	loop(vars[1], func() {
		b.SignExtend(v[16], 32)
		b.FAdd(b.And(v[3], mask62), b.And(v[0], mask62))
		load(40)
	})
	h := b.Const(0)
	for _, id := range []int{1, 2, 6, 8, 9, 10, 11, 12, 14, 15, 17, 18, 23, 24} {
		h = b.Xor(b.Mul(h, b.Const(31)), v[id])
	}
	l1, l2 := b.NewLabel(), b.NewLabel()
	// h and the narrowed values reach both targets by dominance.
	p := b.Compare(v[7], ir.BranchLess, v[13])
	q1, q2 := narrow(v[4], 32), narrow(v[13], 32)
	b.Branch(p, l1)
	b.Jump(l2)
	b.Bind(l1)
	b.Return(b.Xor(h, b.CastUnsigned(q1, 64)))
	b.Bind(l2)
	b.Return(b.Add(h, b.SignExtend(b.CastUnsigned(q2, 64), 32)))
	return b.Finish()
}

// aliasedLabelCrossBlock binds two labels at one position; only the first is
// a jump target, and the block uses an entry value across the edge.
func aliasedLabelCrossBlock() (*ir.SealedProgram, error) {
	b := n.NewBuilder()
	x := b.ParamBits(64)
	y := b.Mul(x, x)
	l0, l1 := b.NewLabel(), b.NewLabel()
	b.Jump(l0)
	b.Bind(l0)
	b.Bind(l1)
	b.Return(b.Add(y, b.Const(1)))
	return b.Finish()
}

// sequentialVarLoopsFloat is the audit's reduced control program whose
// edges and predicates wrote X27 after the prologue's saves were planned.
func sequentialVarLoopsFloat() (*ir.SealedProgram, error) {
	b := n.NewBuilder()
	base, x := b.ParamBits(64), b.ParamBits(64)
	b.ParamBits(64)
	load := func(off uint64) n.Value { return b.Xor(b.Load64(b.Add(base, b.Const(off))), x) }
	narrow := func(v n.Value, w uint8) n.Value { return b.CastUnsigned(v, w) }
	counter := func(a n.Value) n.Value { return b.Add(b.And(a, b.Const(3)), b.Const(1)) }
	mask62 := b.Const(^uint64(1 << 62))
	v := map[int]n.Value{}
	v[0], v[1], v[2], v[3], v[4], v[5] = load(168), load(200), load(448), load(96), load(32), load(40)
	v[6], v[7] = counter(v[1]), counter(v[1])
	vars := []n.Var{
		b.Var(v[6]), b.Var(v[7]), b.Var(v[2]), b.Var(v[7]), b.Var(narrow(v[5], 16)),
		b.Var(v[6]), b.Var(narrow(v[6], 32)), b.Var(narrow(v[2], 8)), b.Var(narrow(v[2], 8)), b.Var(narrow(v[5], 32)),
	}
	for i, off := range []uint64{0, 8, 16, 24, 32, 48, 56} {
		v[8+i] = load(off)
	}
	v[15] = b.FMul(b.And(v[8], mask62), b.And(v[14], mask62))
	v[16], v[17] = load(72), load(72)
	b.Enter()
	b.Var(v[14])
	b.Set(vars[8], narrow(v[17], 8))
	b.Leave()
	v[23], v[25] = load(104), load(120)
	loop := func(counter n.Var, body func()) {
		top, exit := b.NewLabel(), b.NewLabel()
		b.Bind(top)
		b.Branch(b.IsZero(b.Get(counter)), exit)
		body()
		b.Set(counter, b.Sub(b.Get(counter), b.Const(1)))
		b.Jump(top)
		b.Bind(exit)
	}
	loop(vars[0], func() {})
	loop(vars[1], func() { load(24) })
	h := b.Const(0)
	for _, id := range []int{0, 1, 2, 3, 5, 6, 8, 9, 10, 11, 12, 14, 15, 16, 17, 23, 25} {
		h = b.Xor(b.Mul(h, b.Const(31)), v[id])
	}
	l1, l2 := b.NewLabel(), b.NewLabel()
	// h and the narrowed values reach both targets by dominance.
	p := b.Compare(v[7], ir.BranchLess, v[13])
	q1, q2 := narrow(v[4], 32), narrow(v[13], 32)
	b.Branch(p, l1)
	b.Jump(l2)
	b.Bind(l1)
	b.Return(b.Xor(h, b.CastUnsigned(q1, 64)))
	b.Bind(l2)
	b.Return(b.Add(h, b.SignExtend(b.CastUnsigned(q2, 64), 32)))
	return b.Finish()
}

// floatCopyUnderPressure is the audit's reduced straight-line program whose
// FP-to-GPR copy landed in X23 without a save or a clobber report.
func floatCopyUnderPressure() (*ir.SealedProgram, error) {
	b := n.NewBuilder()
	base, x, out := b.ParamBits(64), b.ParamBits(64), b.ParamBits(64)
	load := func(off uint64) n.Value { return b.Xor(b.Load64(b.Add(base, b.Const(off))), x) }
	narrow := func(v n.Value, w uint8) n.Value { return b.CastUnsigned(v, w) }
	mask62 := b.Const(^uint64(1 << 62))
	v := map[int]n.Value{}
	for i, off := range []uint64{352, 256, 336, 352, 32, 320, 48, 56} {
		v[i] = load(off)
	}
	b.Var(v[4])
	result := b.Var(narrow(v[2], 32))
	b.Var(narrow(v[3], 32))
	b.Var(narrow(v[6], 16))
	b.Var(narrow(v[6], 32))
	b.Var(narrow(v[5], 16))
	b.Var(narrow(v[7], 16))
	b.Var(narrow(v[4], 8))
	v[8] = load(0)
	v[10] = b.Sub(v[4], v[1])
	v[11] = b.FMul(b.And(v[8], mask62), b.And(v[5], mask62))
	v[12] = load(24)
	v[15] = b.ShiftRightBy(v[11], b.And(v[10], b.Const(127)))
	v[17], v[18], v[19], v[20] = load(80), load(88), load(96), load(112)
	b.Enter()
	b.CastUnsigned(b.Add(narrow(v[20], 8), narrow(v[19], 8)), 64)
	b.ShiftRight(v[6], 51)
	b.Leave()
	v[28] = b.FMul(b.And(v[18], mask62), b.And(v[19], mask62))
	b.Set(result, narrow(b.ShiftLeftBy(v[15], b.And(v[1], b.Const(127))), 32))
	load(184)
	b.RotateLeftBy(v[8], b.And(v[17], b.Const(63)))
	b.Store64(b.Add(out, b.Const(0xd*8)), v[28])
	h := b.Const(0)
	for _, id := range []int{0, 4, 10, 11, 12} {
		h = b.Xor(b.Mul(h, b.Const(31)), v[id])
	}
	b.Return(h)
	return b.Finish()
}

// randomPressureProgram builds a random program in the shape the
// security audit's register-preservation harness found clobbers with: many
// live loads, narrow Vars, a scope, counted loops whose bodies compute,
// float products consumed as integers, and a branch to two returns.
func randomPressureProgram(r *rand.Rand) (*ir.SealedProgram, error) {
	b := n.NewBuilder()
	base, x := b.ParamBits(64), b.ParamBits(64)
	mask62 := b.Const(^uint64(1 << 62))
	load := func() n.Value { return b.Xor(b.Load64(b.Add(base, b.Const(uint64(r.Intn(64))*8))), x) }
	widths := []uint8{8, 16, 32, 64}
	narrow := func(v n.Value, w uint8) n.Value {
		if w == 64 {
			return v
		}
		return b.CastUnsigned(v, w)
	}
	var live []n.Value
	for range 12 + r.Intn(16) {
		live = append(live, load())
	}
	pick := func(values []n.Value) n.Value { return values[r.Intn(len(values))] }
	compute := func(values []n.Value) n.Value {
		a, c := pick(values), pick(values)
		switch r.Intn(6) {
		case 0:
			return b.Add(a, c)
		case 1:
			return b.Mul(a, c)
		case 2:
			return b.FMul(b.And(a, mask62), b.And(c, mask62))
		case 3:
			return b.FAdd(b.And(a, mask62), b.And(c, mask62))
		case 4:
			w := widths[r.Intn(3)]
			return b.CastUnsigned(b.Add(narrow(a, w), narrow(c, w)), 64)
		}
		return b.Xor(a, b.ShiftRight(c, uint8(1+r.Intn(62))))
	}
	type variable struct {
		v n.Var
		w uint8
	}
	var vars []variable
	for range 4 + r.Intn(8) {
		w := widths[r.Intn(4)]
		vars = append(vars, variable{b.Var(narrow(pick(live), w)), w})
	}
	var counters []n.Var
	for range 1 + r.Intn(3) {
		counters = append(counters, b.Var(b.Add(b.And(pick(live), b.Const(3)), b.Const(1))))
	}
	if r.Intn(2) == 0 {
		b.Enter()
		b.Var(pick(live))
		v := vars[r.Intn(len(vars))]
		b.Set(v.v, narrow(pick(live), v.w))
		b.Leave()
	}
	for _, counter := range counters {
		top, exit := b.NewLabel(), b.NewLabel()
		b.Bind(top)
		b.Branch(b.IsZero(b.Get(counter)), exit)
		inner := append([]n.Value(nil), live...)
		for range r.Intn(4) {
			inner = append(inner, compute(inner))
		}
		if v := vars[r.Intn(len(vars))]; r.Intn(2) == 0 {
			b.Set(v.v, narrow(pick(inner), v.w))
		}
		b.Set(counter, b.Sub(b.Get(counter), b.Const(1)))
		b.Jump(top)
		b.Bind(exit)
	}
	for range r.Intn(8) {
		live = append(live, compute(live))
	}
	h := b.Const(0)
	for _, v := range live {
		h = b.Xor(b.Mul(h, b.Const(31)), v)
	}
	for _, v := range vars {
		h = b.Add(h, b.CastUnsigned(b.Get(v.v), 64))
	}
	l1, l2 := b.NewLabel(), b.NewLabel()
	// h and the narrowed values reach both targets by dominance. The picks
	// keep their original order.
	p := b.Compare(pick(live), ir.BranchLess, pick(live))
	q1 := narrow(pick(live), 32)
	q2 := narrow(pick(live), 32)
	b.Branch(p, l1)
	b.Jump(l2)
	b.Bind(l1)
	b.Return(b.Xor(h, b.CastUnsigned(q1, 64)))
	b.Bind(l2)
	b.Return(b.Add(h, b.CastUnsigned(q2, 64)))
	return b.Finish()
}

// A C entry may clobber only what a C call clobbers: every other register it
// writes must be saved and restored (security audit 2026-10-25, F15).
func TestCEntriesReportOnlyCallerSavedClobbers(t *testing.T) {
	programs := []func() (*ir.SealedProgram, error){sequentialVarLoops, sequentialVarLoopsFloat, floatCopyUnderPressure}
	r := rand.New(rand.NewSource(1))
	for range 120 {
		programs = append(programs, func() (*ir.SealedProgram, error) { return randomPressureProgram(r) })
	}
	for _, build := range programs {
		p, err := build()
		if err != nil {
			t.Fatal(err)
		}
		for _, arch := range []string{"amd64", "arm64", "riscv64", "loong64", "ppc64le"} {
			effect, err := abi.PreflightCall(abi.CallSpec{Architecture: arch, Kind: abi.FunctionCall, ABI: abi.C})
			if err != nil {
				t.Fatal(err)
			}
			target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch})
			if err != nil {
				t.Fatal(err)
			}
			for name, f := range map[string]compile.Func{
				"StackNoUse":    {ABI: abi.C},
				"StackPlatform": {ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}},
				"Foreign":       {ABI: abi.C, Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}},
			} {
				unit, err := compile.Lower(target, p, f)
				if errors.Is(err, compile.ErrStackRequired) || errors.Is(err, compile.ErrBackendLimit) {
					continue
				}
				if err != nil {
					t.Fatalf("%s %s: %v", arch, name, err)
				}
				for _, r := range unit.Report().ClobberedRegisters() {
					if !slices.Contains(effect.ClobberedRegisters, r) {
						t.Errorf("%s %s: clobbers C callee-saved %s", arch, name, r)
					}
				}
			}
		}
	}
}

// arm64 listings name registers, so a preserved register any instruction
// uses must be restored before every return.
func TestARM64EntriesRestoreEveryPreservedRegister(t *testing.T) {
	target, err := compile.NewTarget(compile.TargetSpec{Architecture: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	preserved := regexp.MustCompile(`\bX(19|2[0-7])\b`)
	for _, build := range []func() (*ir.SealedProgram, error){sequentialVarLoops, sequentialVarLoopsFloat, floatCopyUnderPressure} {
		p, err := build()
		if err != nil {
			t.Fatal(err)
		}
		for name, f := range map[string]compile.Func{
			"StackNoUse":    {ABI: abi.C},
			"StackPlatform": {ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}},
		} {
			unit, err := compile.Lower(target, p, f)
			if err != nil {
				t.Fatal(err)
			}
			listing, err := unit.Listing()
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(listing, "\n")
			used := map[string]bool{}
			for _, line := range lines {
				for _, r := range preserved.FindAllString(line, -1) {
					used[r] = true
				}
			}
			for i, line := range lines {
				if !strings.HasSuffix(line, "  ret") {
					continue
				}
				restored := map[string]bool{}
				for j := i - 1; j >= 0 && (strings.Contains(lines[j], "  ldr ") || strings.Contains(lines[j], "  ldp ") || strings.Contains(lines[j], "  ldur ") || strings.Contains(lines[j], "  add SP")); j-- {
					for _, r := range preserved.FindAllString(lines[j], -1) {
						restored[r] = true
					}
				}
				for r := range used {
					if !restored[r] {
						t.Errorf("%s: return at line %d does not restore %s\n%s", name, i, r, listing)
					}
				}
			}
		}
	}
}
