//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le || 386)

package varfuzz

import (
	"errors"
	"fmt"
	"github.com/moriyoshi/nyanjit/execution"
	"math"
	"runtime"
	"strings"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func reproRun(t *testing.T, name string, b *n.Builder, want uint64, opts ...abi.CompileOptions) {
	t.Helper()
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) == 0 {
		opts = []abi.CompileOptions{{ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 8192}}
	}
	for _, o := range opts {

		c, err := compile.Compile(p, o)
		if err != nil {
			t.Errorf("%s %+v: %v", name, o, err)
			continue
		}
		lo, hi, err := c.CallPair()
		c.Close()
		got := uint64(lo)
		if runtime.GOARCH == "386" {
			got |= uint64(hi) << 32
		}
		if err != nil || got != want {
			t.Errorf("%s stack=%v: got %#x want %#x (%v)", name, o.Stack, got, want, err)
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
	b.Branch(b.CompareImmediate(b.Get(k), n.BranchNotEqual, 0), l)
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
				b.Branch(b.CompareImmediate(b.Get(k), n.BranchNotEqual, 0), l)
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
	b.Branch(b.Compare(b.Get(i), n.BranchAboveEqual, b.Const(3)), x)
	b.Set(i, b.Add(b.Get(i), b.Const(1)))
	b.Jump(l)
	b.Bind(x)
	e := b.Add(b.Get(i), b.Get(acc))
	b.Leave(e)
	b.Return(e)
	reproRun(t, "nouse", b, 3, abi.CompileOptions{ABI: abi.C, Stack: abi.StackNoUse})
}

func TestReproNoUseVariants(t *testing.T) {
	{ // single Var, no scopes
		b := n.NewBuilder()
		i := b.Var(b.Const(0))
		l, x := b.NewLabel(), b.NewLabel()
		b.Jump(l)
		b.Bind(l)
		b.Branch(b.Compare(b.Get(i), n.BranchAboveEqual, b.Const(3)), x)
		b.Set(i, b.Add(b.Get(i), b.Const(1)))
		b.Jump(l)
		b.Bind(x)
		b.Return(b.Get(i))
		reproRun(t, "nouse-var", b, 3, abi.CompileOptions{ABI: abi.C, Stack: abi.StackNoUse})
	}
	{ // label params only
		b := n.NewBuilder()
		l, body, x := b.NewLabel(), b.NewLabel(), b.NewLabel()
		b.JumpArgs(l, b.Const(0))
		p := b.BindParams(l, 1)
		b.BranchArgs(b.Compare(p[0], n.BranchAboveEqual, b.Const(3)), x, p, body, p)
		q := b.BindParams(body, 1)
		b.JumpArgs(l, b.Add(q[0], b.Const(1)))
		r := b.BindParams(x, 1)
		b.Return(r[0])
		reproRun(t, "nouse-params", b, 3, abi.CompileOptions{ABI: abi.C, Stack: abi.StackNoUse})
	}
}

// An FP capture live across a call in a nested scope.
func TestReproFPCaptureAcrossCall(t *testing.T) {
	if runtime.GOARCH == "386" {
		t.Skip("386 FP lowering is unsupported")
	}
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
			o := abi.CompileOptions{ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 4096, AssumePointerFree: true}
			var c *execution.Code
			if mode == "go" || variant >= 2 {
				c, err = compile.CompileGo(p, o)
			} else {
				c, err = compile.CompileForeign(p, o)
			}
			if err != nil {
				l, _ := compile.EmitAssembly(p, abi.CompileOptions{Architecture: runtime.GOARCH, ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 4096})
				t.Errorf("variant %d %s: %v\n%s", variant, mode, err, l)
				continue
			}
			got, err := c.Call()
			if uint64(got) != want || err != nil {
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
			p = b.CompareImmediate(b.ConstBits(16, 0), n.BranchLessEqual, -8)
		case 1:
			p = b.CompareImmediate(b.ConstBits(32, 0), n.BranchLessEqual, -8)
		case 2:
			p = b.IsZero(b.ConstBits(32, 0))
		case 3:
			p = b.CompareImmediate(b.CastUnsigned(b.ParamNative(), 16), n.BranchLessEqual, -8)
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
		_, err = compile.EmitAssembly(pr, abi.CompileOptions{Architecture: "386", Stack: abi.StackPlatform, StackBytesAvailable: 1024})
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
			c, err := compile.Compile(p, abi.CompileOptions{Stack: abi.StackPlatform, StackBytesAvailable: 1024})
			if err != nil {
				t.Fatal(err)
			}
			for _, in := range []uint64{0xfffffffe, 0x7f, 0x80, 0x8000, 0x80000000, 0xffffffff} {
				in = mask(in, w)
				want := uint64(1)
				if sext(mask(in&m, w), w) < 0 {
					want = 2
				}
				lo, hi, _ := c.CallPair(uintptr(in))
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
			c, err := compile.Compile(p, abi.CompileOptions{Stack: st, StackBytesAvailable: 1024})
			if err != nil {
				t.Fatal(err)
			}
			got, _ := c.Call(41)
			c.Close()
			if got != 0 {
				l, _ := compile.EmitAssembly(p, abi.CompileOptions{Architecture: runtime.GOARCH, Stack: st, StackBytesAvailable: 1024})
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
	L1, L2, L3 := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.JumpArgs(L1, b.ConstBits(16, 1), b.ConstBits(8, 0))
	h := b.BindParamsBits(L1, []uint8{16, 8})
	b.BranchArgs(b.IsZero(h[0]), L3, nil, L2, h)
	q := b.BindParamsBits(L2, []uint8{16, 8})
	t9, t10 := q[0], q[1]
	b.Enter()
	L4, L5, L6 := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.JumpArgs(L4, b.ConstBits(64, 0))
	h = b.BindParamsBits(L4, []uint8{64})
	b.BranchArgs(b.IsZero(h[0]), L6, nil, L5, h)
	q = b.BindParamsBits(L5, []uint8{64})
	t12 := q[0]
	b.Enter()
	b.Set(v8, b.CastUnsigned(t10, 16))
	b.Set(v15, t3)
	t13 := b.Get(v1)
	b.Leave(b.Sub(t12, b.ConstBits(64, 1)), t13)
	b.JumpArgs(L4, b.ConstBits(64, 0))
	b.Bind(L6)
	t24 := b.Get(v1)
	b.Leave(b.Sub(t9, b.ConstBits(16, 1)), t24)
	b.JumpArgs(L1, b.ConstBits(16, 0), b.ConstBits(8, 0))
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
	o := abi.CompileOptions{Stack: abi.StackPlatform, StackBytesAvailable: 8192}
	c, err := compile.Compile(p, o)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	lo, hi, _ := c.CallPair(execution.Words("Lll", params[0], uint64(uint32(params[1])), uint64(uintptr(params[2])))...)
	got := execution.Wide(lo, hi)
	if got != want {

		t.Errorf("%s: got %#x want %#x", name, got, want)
	}
}

// 386: a call followed by a labeled loop with a 64-bit label parameter.
func TestRepro386CallLoop64(t *testing.T) {
	tg, _ := getTargets()
	for variant := 0; variant < 8; variant++ {
		b := n.NewBuilder()
		v := b.Var(b.Const(0))
		if variant&4 != 0 {
			b.CallC(b.ConstNative(tg.nativeC[1]), b.ConstNative(0))
		}
		L, B, X := b.NewLabel(), b.NewLabel(), b.NewLabel()
		w := uint8(64)
		if variant&1 != 0 {
			w = 32
		}
		b.JumpArgs(L, b.ConstBits(8, 1), b.ConstBits(w, 0))
		h := b.BindParamsBits(L, []uint8{8, w})
		b.BranchArgs(b.IsZero(h[0]), X, nil, B, h)
		q := b.BindParamsBits(B, []uint8{8, w})
		var e n.Value
		if variant&2 != 0 {
			b.Enter()
			e = b.Get(v)
			b.Leave(e)
		} else {
			e = b.Get(v)
		}
		b.Set(v, b.Add(e, b.Const(1)))
		b.JumpArgs(L, b.Sub(q[0], b.ConstBits(8, 1)), b.ConstBits(w, 0))
		b.Bind(X)
		b.Return(b.Get(v))
		p, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		_, err = compile.CompileGo(p, abi.CompileOptions{ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 4096, AssumePointerFree: true})
		t.Logf("variant w=%d scope=%v call=%v: %v", w, variant&2 != 0, variant&4 != 0, err)
	}
}

func TestReproPPCManagedCNative(t *testing.T) {
	tg, _ := getTargets()
	for _, f := range []abi.Flavor{abi.C, abi.Go} {
		p, _ := prog2(tg).Finish()
		o := abi.CompileOptions{ABI: f, Stack: abi.StackPlatform, StackBytesAvailable: 4096, AssumePointerFree: true}
		_, err := compile.CompileGo(p, o)
		_, aerr := compile.Analyze(p, abi.CompileOptions{ABI: f, ManagedCalls: true, Stack: abi.StackPlatform, StackBytesAvailable: 4096, AssumePointerFree: true})
		if aerr != nil {
			t.Fatal(aerr)
		}
		if err != nil {
			if errors.Is(err, compile.ErrBackendLimit) && strings.Contains(err.Error(), "managed frame needs") {
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
				b.Branch(b.Compare(b.Get(i), n.BranchAboveEqual, b.Const(3)), x)
				b.Set(i, b.Add(b.Get(i), b.Const(1)))
				b.Jump(l)
				b.Bind(x)
				b.Return(b.Get(i))
			} else {
				body := b.NewLabel()
				b.JumpArgs(l, b.Const(0))
				p := b.BindParams(l, 1)
				b.BranchArgs(b.Compare(p[0], n.BranchAboveEqual, b.Const(3)), x, p, body, p)
				q := b.BindParams(body, 1)
				b.JumpArgs(l, b.Add(q[0], b.Const(1)))
				r := b.BindParams(x, 1)
				b.Return(r[0])
			}
			reproRun(t, fmt.Sprintf("nouse abi=%v var=%v", f, useVar), b, 3, abi.CompileOptions{ABI: f, Stack: abi.StackNoUse})
		}
	}
}

// ppc64: nested Select of constants inside a scope fails register allocation.

func TestRepro185(t *testing.T) {
	p, err := prog185().Finish()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range crossArches {
		_, err := compile.EmitAssembly(p, abi.CompileOptions{Architecture: a, Stack: abi.StackPlatform, StackBytesAvailable: 8192})
		t.Logf("%s: %v", a, err)
	}
}

func TestRepro24(t *testing.T) {
	// interpreter-computed expectation for params (0x7f, 0xffb30bd1, 0xfffffffe)
	runProg(t, "24", prog24(), [3]uint64{0x7f, 0xffb30bd1, 0xfffffffe}, 0)
}

// Label-argument parallel copy (a swap cycle) uses a Var's home register as scratch.
func TestReproJumpArgsSwapClobbersVar(t *testing.T) {
	b := n.NewBuilder()
	v := b.Var(b.Const(1))
	L1, B1, X1 := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.JumpArgs(L1, b.ConstBits(32, 1))
	h := b.BindParamsBits(L1, []uint8{32})
	b.BranchArgs(b.IsZero(h[0]), X1, nil, B1, h)
	q := b.BindParamsBits(B1, []uint8{32})
	b.Enter()
	L2, B2, X2 := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.JumpArgs(L2, b.Const(0), q[0]) // inner loop runs 0 times
	h2 := b.BindParamsBits(L2, []uint8{64, 32})
	b.BranchArgs(b.IsZero(h2[0]), X2, nil, B2, h2)
	q2 := b.BindParamsBits(B2, []uint8{64, 32})
	b.Set(v, b.Add(b.Get(v), b.Const(100)))
	b.JumpArgs(L2, b.Sub(q2[0], b.Const(1)), q2[1])
	b.Bind(X2)
	k := b.Sub(q[0], b.ConstBits(32, 1))
	b.Leave(k)
	b.JumpArgs(L1, k)
	b.Bind(X1)
	b.Return(b.Get(v))
	reproRun(t, "swap", b, 1)
}

func TestRepro445(t *testing.T) {
	tg, _ := getTargets()
	params := [3]uint64{0x0, 0xd4351b, 0x8000000000000000}
	for _, f := range []abi.Flavor{abi.C, abi.Go} {
		p, _ := prog445(tg).Finish()
		c, err := compile.CompileGo(p, abi.CompileOptions{ABI: f, Stack: abi.StackPlatform, StackBytesAvailable: 4096, AssumePointerFree: true})
		if err != nil {
			if errors.Is(err, compile.ErrBackendLimit) && strings.Contains(err.Error(), "managed frame needs") {
				t.Logf("documented managed frame limit: %v", err)
				continue
			}
			t.Fatal(err)
		}
		lo, hi, _ := c.CallPair(execution.Words("Lll", params[0], uint64(uint32(params[1])), uint64(uintptr(params[2])))...)
		if got := execution.Wide(lo, hi); got != uint64(uintptr(params[2]))+0x1000 {
			t.Fatalf("abi=%v got %#x want 0x8000000000001000", f, got)
		}
	}
}

func TestReproX2(t *testing.T) {
	params := [3]uint64{0x8000000000000000, 0xffa960f2, 0x5}
	for _, mode := range []string{"Compile", "CompileGo"} {
		for _, f := range []abi.Flavor{abi.C, abi.Go} {
			p, _ := progX2().Finish()
			o := abi.CompileOptions{ABI: f, Stack: abi.StackPlatform, StackBytesAvailable: 8192, AssumePointerFree: true}
			var c *execution.Code
			var err error
			if mode == "Compile" {
				c, err = compile.Compile(p, o)
			} else {
				c, err = compile.CompileGo(p, o)
			}
			if err != nil {
				if errors.Is(err, compile.ErrBackendLimit) && strings.Contains(err.Error(), "managed frame needs") {
					t.Logf("documented managed frame limit: %v", err)
					continue
				}
				t.Fatal(err)
			}
			lo, hi, _ := c.CallPair(execution.Words("Lll", params[0], uint64(uint32(params[1])), uint64(uintptr(params[2])))...)
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
		if c.fp && runtime.GOARCH == "386" {
			continue
		}
		p := generateBytes(sweepBytes(c.index), int64(c.index), c.fp, true)
		checkProgram(t, p, paramsFor(int64(c.index)), 0, true)
		leaf := generateBytes(sweepBytes(c.index), int64(c.index), c.fp, false)
		checkProgram(t, leaf, paramsFor(int64(c.index)), 0, true)
	}
}
