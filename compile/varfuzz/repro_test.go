//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || loong64 || 386)

package varfuzz

import (
	"fmt"
	"math"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/ir"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func reproRun(t *testing.T, name string, b *n.Builder, want uint64, opts ...compile.Func) {
	t.Helper()
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	reproRunProgram(t, name, p, want, opts...)
}

// reproRunProgram runs a sealed program, from a Builder or raw IR.
func reproRunProgram(t *testing.T, name string, p *ir.SealedProgram, want uint64, opts ...compile.Func) {
	t.Helper()
	if len(opts) == 0 {
		opts = []compile.Func{{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}}}
	}
	for _, o := range opts {

		if o.ABI == abi.C && o.Stack.Practice == abi.StackPlatform {
			o.Context = compile.ContextForeign
		}
		c, err := n.Compile(p, compile.TargetSpec{}, o)
		if err != nil {
			t.Errorf("%s %+v: %v", name, o, err)
			continue
		}
		lo, hi, err := c.CallPair(execution.Words()...)
		c.Close()
		got := uint64(lo)
		if runtime.GOARCH == "386" {
			got |= uint64(hi) << 32
		}
		if err != nil || got != want {
			t.Errorf("%s stack=%v: got %#x want %#x (%v)", name, o.Stack.Practice, got, want, err)
		}
	}
}

// Leave reload conflict: outer var's register reused for an inner value and
// then written back to the outer var's slot.

func leaveReload(t *testing.T, no, ni int, swap bool) {
	b := n.NewBuilder()
	// enough outer vars to create pressure
	outer := make([]n.Var, no)
	for i := range outer {
		outer[i] = b.Var(b.Const(uint64(i + 1)))
	}
	b.Enter()
	x := b.Var(b.Const(100)) // rotated var
	y := b.Var(b.Const(200))
	inner := make([]n.Var, ni)
	for i := range inner {
		inner[i] = b.Var(b.Const(uint64(1000 + i)))
	}
	a0, a1 := b.Get(x), b.Get(y)
	if swap {
		b.Set(x, a1)
		b.Set(y, a0)
	}
	b.Enter()
	c := b.Var(b.ConstBits(16, 0))
	k := b.Var(b.ConstBits(16, 0))
	l := b.NewLabel()
	b.Set(k, b.ConstBits(16, 1))
	b.Jump(l)
	b.Bind(l)
	b.Set(k, b.Sub(b.Get(k), b.ConstBits(16, 1)))
	b.Branch(b.CompareImmediate(b.Get(k), ir.BranchNotEqual, 0), l)
	e := b.Add(b.CastUnsigned(b.Get(c), 64), b.CastUnsigned(b.Get(k), 64))
	b.Leave(e)
	sum := b.Add(e, b.Get(x))
	for _, v := range inner {
		sum = b.Add(sum, b.Get(v))
	}
	sum = b.Add(sum, b.Mul(b.Get(y), b.Const(3)))
	b.Leave(sum)
	for _, v := range outer {
		sum = b.Add(sum, b.Get(v))
	}
	b.Return(sum)
	want := uint64(200 + ni*1000 + ni*(ni-1)/2 + 300 + no*(no+1)/2)
	if !swap {
		want = 100 + uint64(ni*1000+ni*(ni-1)/2) + 600 + uint64(no*(no+1)/2)
	}
	reproRun(t, fmt.Sprintf("leave-reload outer=%d inner=%d swap=%v", no, ni, swap), b, want)
}

// Minimal: outer vars spilled at Enter of a nested scope; at Leave the exported
// Gets of inner vars are shuffled through registers that the parallel reload
// of the outer homes also targets.
func TestReproLeaveReloadSmall(t *testing.T) {
	for no := 1; no <= 30; no++ {
		for loop := 0; loop < 2; loop++ {
			b := n.NewBuilder()
			outer := make([]n.Var, no)
			for i := range outer {
				outer[i] = b.Var(b.Const(uint64(i + 1)))
			}
			b.Enter()
			c := b.Var(b.Const(0))
			k := b.Var(b.Const(0))
			if loop == 1 {
				l := b.NewLabel()
				b.Set(k, b.Const(1))
				b.Jump(l)
				b.Bind(l)
				b.Set(k, b.Sub(b.Get(k), b.Const(1)))
				b.Branch(b.CompareImmediate(b.Get(k), ir.BranchNotEqual, 0), l)
			}
			e := b.Add(b.Get(c), b.Get(k))
			b.Leave(e)
			sum := e
			for _, v := range outer {
				sum = b.Add(sum, b.Get(v))
			}
			b.Return(sum)
			reproRun(t, fmt.Sprintf("small outer=%d loop=%d", no, loop), b, uint64(no*(no+1)/2))
		}
	}
}

// StackNoUse: a loop whose counter compare constant is spilled to workspace.
func TestReproNoUseWorkspaceBase(t *testing.T) {
	b := n.NewBuilder()
	acc := b.Var(b.Const(0))
	b.Enter()
	i := b.Var(b.Const(0))
	l, x := b.NewLabel(), b.NewLabel()
	b.Jump(l)
	b.Bind(l)
	b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, b.Const(3)), x)
	b.Set(i, b.Add(b.Get(i), b.Const(1)))
	b.Jump(l)
	b.Bind(x)
	e := b.Add(b.Get(i), b.Get(acc))
	b.Leave(e)
	b.Return(e)
	reproRun(t, "nouse", b, 3, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackNoUse}})
}

func TestReproNoUseVariants(t *testing.T) {
	{ // single Var, no scopes
		b := n.NewBuilder()
		i := b.Var(b.Const(0))
		l, x := b.NewLabel(), b.NewLabel()
		b.Jump(l)
		b.Bind(l)
		b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, b.Const(3)), x)
		b.Set(i, b.Add(b.Get(i), b.Const(1)))
		b.Jump(l)
		b.Bind(x)
		b.Return(b.Get(i))
		reproRun(t, "nouse-var", b, 3, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackNoUse}})
	}
	{ // block params only, in raw IR
		reproRunProgram(t, "nouse-params", rawCountTo3(t), 3, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackNoUse}})
	}
}

// rawCountTo3 counts to 3 with the counter as a block parameter of the loop
// header, the body and the exit, each edge passing it on.
func rawCountTo3(t *testing.T) *ir.SealedProgram {
	t.Helper()
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	loop, body, exit := pb.NewBlock(ir.Scalar(64)), pb.NewBlock(ir.Scalar(64)), pb.NewBlock(ir.Scalar(64))
	pb.Jump(entry, loop, pb.Append(entry, ir.Instruction{Op: ir.OpConst}))
	i := pb.BlockParam(loop, 0)
	three := pb.Append(loop, ir.Instruction{Op: ir.OpConst, Imm: 3})
	pb.Branch(loop, &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchAboveEqual, Value: i, Other: three, WithValue: true},
		exit, []int{i}, body, []int{i})
	j := pb.BlockParam(body, 0)
	pb.Jump(body, loop, pb.Append(body, ir.Instruction{Op: ir.OpAdd, A: j, B: pb.Append(body, ir.Instruction{Op: ir.OpConst, Imm: 1})}))
	pb.Return(exit, pb.BlockParam(exit, 0))
	p, err := pb.Seal()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// An FP capture live across a call in a nested scope.
func TestReproFPCaptureAcrossCall(t *testing.T) {
	tg, err := getTargets()
	if err != nil {
		t.Fatal(err)
	}
	for variant := 0; variant < 4; variant++ {
		b := n.NewBuilder()
		v := b.Var(b.Const(5))
		f := b.Float64(1)
		b.Enter()
		var r n.Value
		if variant&1 == 0 {
			r = b.FAdd(f, b.Float64(1)) // capture used before the call
		}
		called := b.CallC(b.ConstNative(tg.nativeC[1]), b.ConstNative(0))
		if variant&1 == 1 {
			r = b.FAdd(f, b.Float64(1)) // and after
		}
		b.Set(v, b.Add(b.Get(v), b.Add(called, r)))
		e := b.Get(v)
		b.Leave(e)
		b.Return(e)
		p, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		want := uint64(5) + calleeFormula([]uint64{0}) + math.Float64bits(2)
		for _, mode := range []string{"foreign", "go"} {
			if variant >= 2 && mode == "go" {
				continue
			}
			o := compile.Func{ABI: abi.C, Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}, AssumePointerFree: true}
			if mode == "go" || variant >= 2 {
				o.Context = compile.ContextGoManaged
			}
			c, err := n.Compile(p, compile.TargetSpec{}, o)
			if managedUnsupported(err) {
				continue
			}
			if err != nil {
				l, _ := lowerListing(p, compile.TargetSpec{Architecture: runtime.GOARCH}, compile.Func{ABI: abi.C, Context: compile.ContextExternal, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
				t.Errorf("variant %d %s: %v\n%s", variant, mode, err, l)
				continue
			}
			lo, hi, err := c.CallPair(execution.Words()...)
			got := execution.Wide(lo, hi)
			if got != want || err != nil {
				t.Errorf("variant %d %s: got %#x want %#x %v", variant, mode, got, want, err)
			}
		}
	}
}

func mix(b *n.Builder, h, x n.Value) n.Value {
	return b.Add(b.RotateLeft(b.Mul(b.Xor(h, x), b.Const(0x9E3779B97F4A7C15)), 13), x)
}

func TestRepro386Wide(t *testing.T) {
	for variant := 0; variant < 4; variant++ {
		b := n.NewBuilder()
		v := b.Var(b.Const(7)) // 64-bit
		k := b.Var(b.ConstBits(32, 3))
		L, X := b.NewLabel(), b.NewLabel()
		b.Jump(L)
		b.Bind(L)
		b.Branch(b.IsZero(b.Get(k)), X)
		if variant&1 != 0 {
			b.Enter()
			A, B := b.NewLabel(), b.NewLabel()
			b.Branch(b.IsZero(b.Get(k)), A)
			b.Jump(B)
			b.Bind(A)
			b.Jump(B)
			b.Bind(B)
			e := b.Get(v)
			b.Leave(e)
			b.Set(v, b.Add(e, b.Const(1)))
		} else {
			b.Set(v, b.Add(b.Get(v), b.Const(1)))
		}
		b.Set(k, b.Sub(b.Get(k), b.ConstBits(32, 1)))
		b.Jump(L)
		b.Bind(X)
		b.Return(b.Get(v))
		reproRun(t, fmt.Sprintf("386wide %d", variant), b, 10)
	}
}

func TestRepro386ConstBranch(t *testing.T) {
	for variant := 0; variant < 4; variant++ {
		b := n.NewBuilder()
		v := b.Var(b.ConstBits(32, 7))
		var p n.Predicate
		switch variant {
		case 0:
			p = b.CompareImmediate(b.ConstBits(16, 0), ir.BranchLessEqual, -8)
		case 1:
			p = b.CompareImmediate(b.ConstBits(32, 0), ir.BranchLessEqual, -8)
		case 2:
			p = b.IsZero(b.ConstBits(32, 0))
		case 3:
			p = b.CompareImmediate(b.CastUnsigned(b.ParamNative(), 16), ir.BranchLessEqual, -8)
		}
		A, B := b.NewLabel(), b.NewLabel()
		b.Branch(p, A)
		b.Set(v, b.ConstBits(32, 1))
		b.Jump(B)
		b.Bind(A)
		b.Set(v, b.ConstBits(32, 2))
		b.Jump(B)
		b.Bind(B)
		b.Return(b.Get(v))
		want := uint64(1)
		if variant == 2 {
			want = 2
		}
		pr, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		_, err = lowerListing(pr, compile.TargetSpec{Architecture: "386"}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1024}})
		t.Logf("variant %d: %v", variant, err)
		_ = want
	}
}

// IsNegative of a narrow AND result branches on flags of a wider ANDS.
func TestReproNarrowAndSignFlag(t *testing.T) {
	for _, w := range []uint8{8, 16, 32} {
		for _, m := range []uint64{0xffffffff, 0xff, 0x80, 0x8000, 0x80000000} {
			b := n.NewBuilder()
			x := b.ParamBits(w)
			r := b.Var(b.ConstBits(32, 1))
			yes, done := b.NewLabel(), b.NewLabel()
			b.Branch(b.IsNegative(b.And(x, b.ConstBits(w, m))), yes)
			b.Jump(done)
			b.Bind(yes)
			b.Set(r, b.ConstBits(32, 2))
			b.Jump(done)
			b.Bind(done)
			b.Return(b.CastUnsigned(b.Get(r), 64))
			p, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			c, err := n.Compile(p, compile.TargetSpec{}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1024}})
			if err != nil {
				t.Fatal(err)
			}
			for _, in := range []uint64{0xfffffffe, 0x7f, 0x80, 0x8000, 0x80000000, 0xffffffff} {
				in = mask(in, w)
				want := uint64(1)
				if sext(mask(in&m, w), w) < 0 {
					want = 2
				}
				lo, hi, _ := c.CallPair(execution.Words(uintptr(in))...)
				got := uint64(lo)
				if runtime.GOARCH == "386" {
					got |= uint64(hi) << 32
				}
				if got != want {
					t.Errorf("w=%d mask=%#x x=%#x: got %d want %d", w, m, in, got, want)
				}
			}
			c.Close()
		}
	}
}

// Set of a capture in a block that never executes.
func TestReproDeadSetOfCapture(t *testing.T) {
	for variant := 0; variant < 4; variant++ {
		b := n.NewBuilder()
		x := b.ParamNative()
		if variant&2 != 0 {
			x = b.Add(x, b.ConstNative(1))
		}
		v := b.Var(b.ConstNative(0))
		b.Enter()
		k := b.Var(b.ConstNative(0))
		L, X := b.NewLabel(), b.NewLabel()
		b.Jump(L)
		b.Bind(L)
		b.Branch(b.IsZero(b.Get(k)), X)
		if variant&1 != 0 {
			b.Enter()
			b.Set(v, x)
			b.Leave()
		} else {
			b.Set(v, x)
		}
		b.Set(k, b.Sub(b.Get(k), b.ConstNative(1)))
		b.Jump(L)
		b.Bind(X)
		b.Leave()
		b.Return(b.Get(v))
		p, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range []abi.StackPractice{abi.StackPlatform} {
			c, err := n.Compile(p, compile.TargetSpec{}, compile.Func{Stack: compile.StackPolicy{Practice: st, BytesAvailable: 1024}})
			if err != nil {
				t.Fatal(err)
			}
			got, _ := c.Call(execution.Words(41)...)
			c.Close()
			if got != 0 {
				l, _ := lowerListing(p, compile.TargetSpec{Architecture: runtime.GOARCH}, compile.Func{Stack: compile.StackPolicy{Practice: st, BytesAvailable: 1024}})
				t.Errorf("variant %d: got %d want 0\n%s", variant, got, l)
			}
		}
	}
}

func seed622(variant int) (*n.Builder, func(p0, p2 uint64) uint64) {
	b := n.NewBuilder()
	b.ParamBits(64)
	b.ParamBits(32)
	t3 := b.ParamNative()
	v1 := b.Var(b.ConstBits(64, 0))
	nOuter := 7
	if variant&1 != 0 {
		nOuter = 0
	}
	outer := []n.Var{}
	for i := 0; i < nOuter; i++ {
		outer = append(outer, b.Var(b.ConstBits(32, 0)))
	}
	b.Enter()
	v8 := b.Var(b.ConstBits(16, 0))
	v15 := b.Var(b.ConstBits(64, 0))
	nInner := 9
	if variant&2 != 0 {
		nInner = 0
	}
	inner := []n.Var{}
	for i := 0; i < nInner; i++ {
		inner = append(inner, b.Var(b.ConstBits(32, 0)))
	}
	// The loops' state (k1, x1) and k4 lives in Vars of the scopes that
	// hold the loops.
	k1, x1 := b.Var(b.ConstBits(16, 0)), b.Var(b.ConstBits(8, 0))
	L1, L2, L3 := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.Set(k1, b.ConstBits(16, 1))
	b.Set(x1, b.ConstBits(8, 0))
	b.Jump(L1)
	b.Bind(L1)
	t9, t10 := b.Get(k1), b.Get(x1)
	b.Branch(b.IsZero(t9), L3)
	b.Jump(L2)
	b.Bind(L2)
	b.Enter()
	k4 := b.Var(b.ConstBits(64, 0))
	L4, L5, L6 := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.Set(k4, b.ConstBits(64, 0))
	b.Jump(L4)
	b.Bind(L4)
	t12 := b.Get(k4)
	b.Branch(b.IsZero(t12), L6)
	b.Jump(L5)
	b.Bind(L5)
	b.Enter()
	b.Set(v8, b.CastUnsigned(t10, 16))
	b.Set(v15, t3)
	t13 := b.Get(v1)
	b.Leave(b.Sub(t12, b.ConstBits(64, 1)), t13)
	b.Set(k4, b.ConstBits(64, 0))
	b.Jump(L4)
	b.Bind(L6)
	t24 := b.Get(v1)
	b.Leave(b.Sub(t9, b.ConstBits(16, 1)), t24)
	b.Set(k1, b.ConstBits(16, 0))
	b.Set(x1, b.ConstBits(8, 0))
	b.Jump(L1)
	b.Bind(L3)
	r := b.Add(b.CastUnsigned(b.Get(v8), 64), b.Get(v15))
	for _, v := range inner {
		r = b.Add(r, b.CastUnsigned(b.Get(v), 64))
	}
	b.Leave(r)
	for _, v := range outer {
		r = b.Add(r, b.CastUnsigned(b.Get(v), 64))
	}
	b.Return(r)
	return b, nil
}

func runProg(t *testing.T, name string, b *n.Builder, params [3]uint64, want uint64) {
	t.Helper()
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	c, err := n.Compile(p, compile.TargetSpec{}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	lo, hi, _ := c.CallPair(execution.Words(params[0], uint32(params[1]), uintptr(params[2]))...)
	got := execution.Wide(lo, hi)
	if got != want {

		t.Errorf("%s: got %#x want %#x", name, got, want)
	}
}

// 386: a call followed by a labeled loop with 64-bit loop state, formerly a
// label parameter and now a Var.
func TestRepro386CallLoop64(t *testing.T) {
	tg, _ := getTargets()
	for variant := 0; variant < 8; variant++ {
		b := n.NewBuilder()
		v := b.Var(b.Const(0))
		w := uint8(64)
		if variant&1 != 0 {
			w = 32
		}
		k, x := b.Var(b.ConstBits(8, 0)), b.Var(b.ConstBits(w, 0))
		if variant&4 != 0 {
			b.CallC(b.ConstNative(tg.nativeC[1]), b.ConstNative(0))
		}
		L, B, X := b.NewLabel(), b.NewLabel(), b.NewLabel()
		b.Set(k, b.ConstBits(8, 1))
		b.Set(x, b.ConstBits(w, 0))
		b.Jump(L)
		b.Bind(L)
		q := []n.Value{b.Get(k), b.Get(x)}
		b.Branch(b.IsZero(q[0]), X)
		b.Jump(B)
		b.Bind(B)
		var e n.Value
		if variant&2 != 0 {
			b.Enter()
			e = b.Get(v)
			b.Leave(e)
		} else {
			e = b.Get(v)
		}
		b.Set(v, b.Add(e, b.Const(1)))
		b.Set(k, b.Sub(q[0], b.ConstBits(8, 1)))
		b.Set(x, b.ConstBits(w, 0))
		b.Jump(L)
		b.Bind(X)
		b.Return(b.Get(v))
		p, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		_, err = n.Compile(p, compile.TargetSpec{}, compile.Func{ABI: abi.C, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}, AssumePointerFree: true})
		t.Logf("variant w=%d scope=%v call=%v: %v", w, variant&2 != 0, variant&4 != 0, err)
	}
}

func TestReproPPCManagedCNative(t *testing.T) {
	tg, _ := getTargets()
	for _, f := range []abi.Flavor{abi.C, abi.Go} {
		p, _ := prog2(tg).Finish()
		o := compile.Func{ABI: f, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}, AssumePointerFree: true}
		_, err := n.Compile(p, compile.TargetSpec{}, o)
		_, aerr := lowerReport(p, compile.TargetSpec{}, o)
		if aerr != nil {
			t.Fatal(aerr)
		}
		if managedUnsupported(err) {
			continue
		}
		if err != nil {
			if managedFrameLimit(err) {
				t.Logf("documented managed frame limit: %v", err)
				continue
			}
			t.Fatal(err)
		}
	}
}

func TestReproNoUseGoABI(t *testing.T) {
	for _, f := range []abi.Flavor{abi.C, abi.Go} {
		for _, useVar := range []bool{false, true} {

			b := n.NewBuilder()
			l, x := b.NewLabel(), b.NewLabel()
			if useVar {
				i := b.Var(b.Const(0))
				b.Jump(l)
				b.Bind(l)
				b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, b.Const(3)), x)
				b.Set(i, b.Add(b.Get(i), b.Const(1)))
				b.Jump(l)
				b.Bind(x)
				b.Return(b.Get(i))
			} else {
				// Block parameters remain a raw-IR feature.
				reproRunProgram(t, fmt.Sprintf("nouse abi=%v params", f), rawCountTo3(t), 3, compile.Func{ABI: f, Stack: compile.StackPolicy{Practice: abi.StackNoUse}})
				continue
			}
			reproRun(t, fmt.Sprintf("nouse abi=%v var=%v", f, useVar), b, 3, compile.Func{ABI: f, Stack: compile.StackPolicy{Practice: abi.StackNoUse}})
		}
	}
}

// ppc64le: nested Select of constants inside a scope fails register allocation.

func TestRepro185(t *testing.T) {
	p, err := prog185().Finish()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range crossArches {
		_, err := lowerListing(p, compile.TargetSpec{Architecture: a}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}})
		t.Logf("%s: %v", a, err)
	}
}

func TestRepro24(t *testing.T) {
	// interpreter-computed expectation for params (0x7f, 0xffb30bd1, 0xfffffffe)
	runProg(t, "24", prog24(), [3]uint64{0x7f, 0xffb30bd1, 0xfffffffe}, 0)
}

// Label-argument parallel copy (a swap cycle) used a Var's home register as
// scratch. The Builder form now carries the loop state in Vars; the raw-IR
// forms below keep the block-parameter copies.
func TestReproJumpArgsSwapClobbersVar(t *testing.T) {
	b := n.NewBuilder()
	v := b.Var(b.Const(1))
	k1 := b.Var(b.ConstBits(32, 0))
	L1, B1, X1 := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.Set(k1, b.ConstBits(32, 1))
	b.Jump(L1)
	b.Bind(L1)
	h := b.Get(k1)
	b.Branch(b.IsZero(h), X1)
	b.Jump(B1)
	b.Bind(B1)
	b.Enter()
	i2, j2 := b.Var(b.Const(0)), b.Var(b.ConstBits(32, 0))
	L2, B2, X2 := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.Set(i2, b.Const(0)) // inner loop runs 0 times
	b.Set(j2, h)
	b.Jump(L2)
	b.Bind(L2)
	q2 := []n.Value{b.Get(i2), b.Get(j2)}
	b.Branch(b.IsZero(q2[0]), X2)
	b.Jump(B2)
	b.Bind(B2)
	b.Set(v, b.Add(b.Get(v), b.Const(100)))
	b.Set(i2, b.Sub(q2[0], b.Const(1)))
	b.Set(j2, q2[1])
	b.Jump(L2)
	b.Bind(X2)
	k := b.Sub(h, b.ConstBits(32, 1))
	b.Leave(k)
	b.Set(k1, k)
	b.Jump(L1)
	b.Bind(X1)
	b.Return(b.Get(v))
	reproRun(t, "swap", b, 1)

	// The same nest in raw IR, with v threaded through every block as a
	// parameter alongside the loop state.
	for _, inner := range []uint64{0, 2} {
		pb := ir.NewProgramBuilder()
		entry := pb.Entry()
		i64, i32 := ir.Scalar(64), ir.Scalar(32)
		l1, b1, x1 := pb.NewBlock(i32, i64), pb.NewBlock(i32, i64), pb.NewBlock(i64)
		l2, b2, x2 := pb.NewBlock(i64, i32, i64), pb.NewBlock(i64, i32, i64), pb.NewBlock(i64)
		pb.Jump(entry, l1, pb.Append(entry, ir.Instruction{Op: ir.OpConst, Imm: 1, Bits: 32}), pb.Append(entry, ir.Instruction{Op: ir.OpConst, Imm: 1}))
		k, kv := pb.BlockParam(l1, 0), pb.BlockParam(l1, 1)
		pb.Branch(l1, &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchEqual, Value: k}, x1, []int{kv}, b1, []int{k, kv})
		q, qv := pb.BlockParam(b1, 0), pb.BlockParam(b1, 1)
		pb.Jump(b1, l2, pb.Append(b1, ir.Instruction{Op: ir.OpConst, Imm: inner}), q, qv)
		i, j, iv := pb.BlockParam(l2, 0), pb.BlockParam(l2, 1), pb.BlockParam(l2, 2)
		pb.Branch(l2, &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchEqual, Value: i}, x2, []int{iv}, b2, []int{i, j, iv})
		bi, bj, bv := pb.BlockParam(b2, 0), pb.BlockParam(b2, 1), pb.BlockParam(b2, 2)
		next := pb.Append(b2, ir.Instruction{Op: ir.OpSub, A: bi, B: pb.Append(b2, ir.Instruction{Op: ir.OpConst, Imm: 1})})
		pb.Jump(b2, l2, next, bj, pb.Append(b2, ir.Instruction{Op: ir.OpAdd, A: bv, B: pb.Append(b2, ir.Instruction{Op: ir.OpConst, Imm: 100})}))
		kk := pb.Append(x2, ir.Instruction{Op: ir.OpSub, A: q, B: pb.Append(x2, ir.Instruction{Op: ir.OpConst, Imm: 1, Bits: 32}), Bits: 32})
		pb.Jump(x2, l1, kk, pb.BlockParam(x2, 0))
		pb.Return(x1, pb.BlockParam(x1, 0))
		p, err := pb.Seal()
		if err != nil {
			t.Fatal(err)
		}
		reproRunProgram(t, fmt.Sprintf("swap raw inner=%d", inner), p, 1+100*inner)
	}
	for _, rounds := range []uint64{0, 1, 2, 3, 6} {
		reproRunProgram(t, fmt.Sprintf("swap cycle rounds=%d", rounds), rawSwapCycle(t, rounds), swapCycleWant(rounds))
	}
}

// rawSwapCycle passes a true swap cycle on its back edge: loop(a, b, c, n)
// jumps to loop(b, c, a, n-1), with values from the entry live across the
// loop.
func rawSwapCycle(t *testing.T, rounds uint64) *ir.SealedProgram {
	t.Helper()
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	i64 := ir.Scalar(64)
	loop, body, exit := pb.NewBlock(i64, i64, i64, i64), pb.NewBlock(), pb.NewBlock()
	c := func(block ir.Block, v uint64) int { return pb.Append(block, ir.Instruction{Op: ir.OpConst, Imm: v}) }
	x, y, z := c(entry, 3), c(entry, 50), c(entry, 700)
	live := pb.Append(entry, ir.Instruction{Op: ir.OpMul, A: x, B: y})
	pb.Jump(entry, loop, x, y, z, c(entry, rounds))
	a, bb, cc, k := pb.BlockParam(loop, 0), pb.BlockParam(loop, 1), pb.BlockParam(loop, 2), pb.BlockParam(loop, 3)
	pb.Branch(loop, &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchEqual, Value: k}, exit, nil, body, nil)
	pb.Jump(body, loop, bb, cc, a, pb.Append(body, ir.Instruction{Op: ir.OpSub, A: k, B: c(body, 1)}))
	// a*1 + b*1000 + c*1000000 + live
	r := pb.Append(exit, ir.Instruction{Op: ir.OpAdd, A: a, B: pb.Append(exit, ir.Instruction{Op: ir.OpMul, A: bb, B: c(exit, 1000)})})
	r = pb.Append(exit, ir.Instruction{Op: ir.OpAdd, A: r, B: pb.Append(exit, ir.Instruction{Op: ir.OpMul, A: cc, B: c(exit, 1000000)})})
	pb.Return(exit, pb.Append(exit, ir.Instruction{Op: ir.OpAdd, A: r, B: live}))
	p, err := pb.Seal()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func swapCycleWant(rounds uint64) uint64 {
	a, b, c := uint64(3), uint64(50), uint64(700)
	for range rounds {
		a, b, c = b, c, a
	}
	return a + b*1000 + c*1000000 + 150
}

func TestRepro445(t *testing.T) {
	tg, _ := getTargets()
	params := [3]uint64{0x0, 0xd4351b, 0x8000000000000000}
	for _, f := range []abi.Flavor{abi.C, abi.Go} {
		p, _ := prog445(tg).Finish()
		c, err := n.Compile(p, compile.TargetSpec{}, compile.Func{ABI: f, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}, AssumePointerFree: true})
		if managedUnsupported(err) {
			continue
		}
		if err != nil {
			if managedFrameLimit(err) {
				t.Logf("documented managed frame limit: %v", err)
				continue
			}
			t.Fatal(err)
		}
		lo, hi, _ := c.CallPair(execution.Words(params[0], uint32(params[1]), uintptr(params[2]))...)
		want := mixStep(0, uint64(uintptr(params[2])))
		if got := execution.Wide(lo, hi); got != want {
			t.Fatalf("abi=%v got %#x want %#x", f, got, want)
		}
	}
}

func TestReproX2(t *testing.T) {
	params := [3]uint64{0x8000000000000000, 0xffa960f2, 0x5}
	for _, mode := range []string{"Compile", "CompileGo"} {
		for _, f := range []abi.Flavor{abi.C, abi.Go} {
			p, _ := progX2().Finish()
			o := compile.Func{ABI: f, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}, AssumePointerFree: true}
			if mode == "CompileGo" {
				o.Context = compile.ContextGoManaged
			}
			c, err := n.Compile(p, compile.TargetSpec{}, o)
			if managedUnsupported(err) {
				continue
			}
			if err != nil {
				if managedFrameLimit(err) {
					t.Logf("documented managed frame limit: %v", err)
					continue
				}
				t.Fatal(err)
			}
			lo, hi, _ := c.CallPair(execution.Words(params[0], uint32(params[1]), uintptr(params[2]))...)
			if got := execution.Wide(lo, hi); got != 0 {
				t.Fatalf("%s abi=%v got %#x want 0", mode, f, got)
			}
		}
	}
}

// The Go compiler ordinarily does not allocate R15, but runtime assembly can
// overwrite it. Force GC in the first outbound Go call with a live R15 home.
func TestManagedCallsPreserveHomesAcrossGC(t *testing.T) {
	p := generateBytes(sweepBytes(70), 70, false, true)
	prog, src, err := finish(p)
	if err != nil {
		t.Fatal(err)
	}
	want, bad := Interpret(p, paramsFor(70))
	if bad != "" {
		t.Fatal(bad)
	}
	for _, cfg := range configs(p) {
		if cfg.name != "Go/Go/Platform+Func" {
			continue
		}
		gcCounter.Store(210)
		out := runConfig(prog, cfg, paramsFor(70))
		if out.skipped {
			t.Skip("managed regression exceeds this architecture's frame limit")
		}
		if out.err != nil || out.got != want {
			t.Fatalf("got %#x want %#x err=%v\n%s", out.got, want, out.err, src)
		}
	}
}

func TestLiveOperandsWithoutSeparateResultRegister(t *testing.T) {
	for _, c := range []struct {
		index uint64
		fp    bool
	}{{23, false}, {47, false}, {205, true}} {
		p := generateBytes(sweepBytes(c.index), int64(c.index), c.fp, true)
		checkProgram(t, p, paramsFor(int64(c.index)), 0, true)
		leaf := generateBytes(sweepBytes(c.index), int64(c.index), c.fp, false)
		checkProgram(t, leaf, paramsFor(int64(c.index)), 0, true)
	}
}

// Var home occupancy: a baseline Get reads the Var's content value itself,
// so a value that is a Var's content can outlive a Set of the Var. Writing
// a part's unchanged content back to its home must keep that content
// registered as the home's occupant; otherwise a later write-back of other
// content overwrites the home while the old content (here the entry value
// of v3, by then v6's content) still lives only there. pressure makes the
// allocator evict the swapped values before their last use.
func varHomeOccupantProgram(pressure int) *n.Builder {
	b := n.NewBuilder()
	x := b.Param()
	_, _ = b.ParamBits(32), b.ParamNative()
	v3 := b.Var(b.Const(3))
	v6 := b.Var(b.Add(x, b.Const(6)))
	L := b.NewLabel()
	b.Jump(L)
	b.Bind(L)
	b.Set(v3, b.Get(v3))
	a, c := b.Get(v6), b.Get(v3)
	b.Set(v6, c)
	b.Set(v3, a)
	live := make([]n.Value, pressure)
	for k := range live {
		live[k] = b.Mul(x, b.Const(uint64(2*k+3)))
	}
	h := x
	for k := len(live) - 1; k >= 0; k-- {
		h = mix(b, h, live[k])
	}
	b.Return(mix(b, mix(b, h, b.Get(v3)), b.Get(v6)))
	return b
}

// varHomeOccupant386 is the reduced sweep index 1001, which exposed the bug
// with 386's few registers: rotations of Vars that live in their homes.
func varHomeOccupant386() *n.Builder {
	b := n.NewBuilder()
	_, _, _ = b.ParamBits(64), b.ParamBits(32), b.ParamNative()
	v1 := b.Var(b.ConstBits(64, 0x0))
	v2 := b.Var(b.ConstBits(16, 0x0))
	v3 := b.Var(b.ConstBits(8, 0x1))
	v5 := b.Var(b.ConstBits(16, 0x0))
	v6 := b.Var(b.ConstBits(8, 0x0))
	v7 := b.Var(b.SignExtend(b.Select(b.Get(v3), b.ConstBits(16, 0x0), b.ConstBits(16, 0x0)), 16))
	v8 := b.Var(b.ConstBits(16, 0x0))
	v12 := b.Var(b.ConstBits(16, 0x0))
	v14 := b.Var(b.ConstBits(64, 0x0))
	rotate := func(x, y n.Var) {
		r0, r1 := b.Get(x), b.Get(y)
		b.Set(x, r1)
		b.Set(y, r0)
	}
	rotate(v3, v6)
	L1, L2 := b.NewLabel(), b.NewLabel()
	b.Set(v14, b.ConstBits(64, 0))
	b.Jump(L1)
	b.Bind(L1)
	b.Branch(b.IsZero(b.Get(v14)), L2)
	rotate(v12, v8)
	b.Set(v14, b.Sub(b.Get(v14), b.ConstBits(64, 1)))
	b.Jump(L1)
	b.Bind(L2)
	b.Set(v3, b.Get(v3))
	rotate(v6, v3)
	b.Set(v5, b.And(b.ConstBits(16, 0x0), b.Add(b.ConstBits(16, 0x0), b.Get(v2))))
	h := mix(b, b.Get(v1), b.Get(v1))
	for _, v := range []n.Value{b.CastUnsigned(b.Get(v2), 64), b.CastUnsigned(b.Get(v3), 64), b.CastUnsigned(b.Get(v5), 64),
		b.CastUnsigned(b.Get(v6), 64), b.Get(v7), b.CastUnsigned(b.Get(v8), 64), b.CastUnsigned(b.Get(v12), 64), b.Get(v14)} {
		h = mix(b, h, v)
	}
	b.Return(h)
	return b
}

func TestReproVarHomeOccupant(t *testing.T) {
	type program struct {
		name   string
		b      *n.Builder
		params [3]uint64
	}
	var programs []program
	for pressure := 0; pressure <= 40; pressure += 4 {
		programs = append(programs, program{fmt.Sprintf("pressure-%d", pressure), varHomeOccupantProgram(pressure), [3]uint64{0x1234}})
	}
	programs = append(programs, program{"386-sweep-1001", varHomeOccupant386(), [3]uint64{0xffffffffffffffff, 0xa74af6, 0xfffffffe}})
	for _, pr := range programs {
		p, err := pr.b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		want, err := interpretIR(p, pr.params)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range []compile.Func{
			{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}},
			{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackNoUse}},
		} {
			for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
				o := runConfig(p, Config{opt: f, tier: tier}, pr.params)
				if o.skipped {
					// A tier limit (386's workspace without a stack), not a result.
					t.Logf("%s tier %d stack %v: %s", pr.name, tier, f.Stack.Practice, o.reason)
					continue
				}
				if o.err != nil || !want.Matches(o.got, 0) {
					t.Errorf("%s tier %d stack %v: got %#x want %v (%v)", pr.name, tier, f.Stack.Practice, o.got, want, o.err)
				}
			}
		}
	}
}
