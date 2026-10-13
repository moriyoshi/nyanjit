//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le)

package compile_test

import (
	"math"
	"testing"
	"unsafe"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

type rvCase struct {
	name  string
	build func(b *n.Builder)
	args  []uintptr
	want  uintptr
}

func rvRun(t *testing.T, c rvCase) {
	for _, stack := range []abi.StackPractice{abi.StackPlatform, abi.StackNoUse} {

		b := n.NewBuilder()
		c.build(b)
		p, e := b.Finish()
		if e != nil {
			t.Errorf("%s: finish %v", c.name, e)
			return
		}
		o := abi.CompileOptions{Stack: stack}
		if stack == abi.StackPlatform {
			o.StackBytesAvailable = 8192
		}
		f, e := compile.Compile(p, o)
		if e != nil {
			t.Errorf("%s stack%d: compile %v", c.name, stack, e)
			continue
		}
		got, e := f.Call(c.args...)
		f.Close()
		if e != nil {
			t.Errorf("%s stack%d: call %v", c.name, stack, e)
			continue
		}
		if got != c.want {
			t.Errorf("%s stack%d: got %#x want %#x", c.name, stack, got, c.want)
		}
	}
}

func TestScopedVariablesAcrossControlFlow(t *testing.T) {
	for _, c := range rvCases {

		rvRun(t, c)
	}
}

func subRevWant(x, y uint64) uintptr {
	v, w := x, y
	for k := 0; k < 3; k++ {
		v = w - v
		w = w*3 - w
		if v >= 64 {
			v = 0
			w2 := uint64(0)
			v = w2
		} else {
			v = w << v
		}
	}
	return uintptr(v + w)
}

func manyVars(nv int) func(b *n.Builder) {
	return func(b *n.Builder) {
		x := b.ParamNative()
		vs := []n.Var{}
		for i := 0; i < nv; i++ {
			vs = append(vs, b.Var(b.Add(x, b.ConstNative(uintptr(i)))))
		}
		t := b.ConstNative(0)
		for _, v := range vs {
			t = b.Xor(b.Mul(t, b.ConstNative(31)), b.Get(v))
		}
		b.Return(t)
	}
}
func manyVarsWant(nv int, x uint64) uintptr {
	t := uint64(0)
	for i := 0; i < nv; i++ {
		t = t*31 ^ (x + uint64(i))
	}
	return uintptr(t)
}

func manyVals(nv int) func(b *n.Builder) {
	return func(b *n.Builder) {
		x := b.ParamNative()
		vs := []n.Value{}
		for i := 0; i < nv; i++ {
			vs = append(vs, b.Add(x, b.ConstNative(uintptr(i))))
		}
		t := b.ConstNative(0)
		for _, v := range vs {
			t = b.Xor(b.Mul(t, b.ConstNative(31)), v)
		}
		b.Return(t)
	}
}

func captureClobber(outer, inner int) func(b *n.Builder) {
	return func(b *n.Builder) {
		x := b.ParamNative()
		b.Enter()
		vs := []n.Var{}
		for i := 0; i < outer; i++ {
			vs = append(vs, b.Var(b.Add(x, b.ConstNative(uintptr(i*3)))))
		}
		g := b.Get(vs[0])
		b.Enter()
		ws := []n.Var{}
		for i := 0; i < inner; i++ {
			ws = append(ws, b.Var(b.Add(x, b.ConstNative(uintptr(100+i)))))
		}
		a := b.Var(b.Add(g, b.ConstNative(1)))
		c := b.Var(b.Add(g, b.ConstNative(2)))
		t := b.Mul(b.Get(a), b.Get(c))
		for _, w := range ws {
			t = b.Xor(b.Mul(t, b.ConstNative(31)), b.Get(w))
		}
		b.Leave(t)
		for _, v := range vs {
			t = b.Xor(b.Mul(t, b.ConstNative(31)), b.Get(v))
		}
		b.Leave(t)
		b.Return(t)
	}
}
func captureClobberWant(outer, inner int, x uint64) uintptr {
	t := (x + 1) * (x + 2)
	for i := 0; i < inner; i++ {
		t = t*31 ^ (x + 100 + uint64(i))
	}
	for i := 0; i < outer; i++ {
		t = t*31 ^ (x + uint64(i*3))
	}
	return uintptr(t)
}

var rvCases = []rvCase{
	{"subRev", func(b *n.Builder) {
		x := b.ParamNative()
		y := b.ParamNative()
		b.Enter()
		v := b.Var(x)
		w := b.Var(y)
		k := b.Var(b.ConstNative(3))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Jump(loop)
		b.Bind(loop)
		b.Branch(b.IsZero(b.Get(k)), done)
		b.Set(v, b.Sub(b.Get(w), b.Get(v)))
		b.Set(w, b.Sub(b.Mul(b.Get(w), b.ConstNative(3)), b.Get(w)))
		b.Set(v, b.ShiftLeftBy(b.Get(w), b.Get(v)))
		b.Set(k, b.Sub(b.Get(k), b.ConstNative(1)))
		b.Jump(loop)
		b.Bind(done)
		r := b.Add(b.Get(v), b.Get(w))
		b.Leave(r)
		b.Return(r)
	}, []uintptr{1, 2}, subRevWant(1, 2)},
	{"fpLabelParamHome", func(b *n.Builder) {
		x := b.ParamNative()
		v := b.Var(b.Float64(8.5))
		then, other, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
		b.Branch(b.IsZero(x), then, other)
		b.Bind(then)
		b.JumpArgs(done, b.FAdd(b.Float64(1), b.Float64(2)))
		b.Bind(other)
		b.JumpArgs(done, b.ConstBits(64, 5))
		ps := b.BindParamsBits(done, []uint8{64})
		b.Return(b.CastNative(b.Xor(b.FAdd(b.Get(v), b.Float64(0)), ps[0])))
	}, []uintptr{0}, uintptr(math.Float64bits(8.5) ^ math.Float64bits(3))},
	{"fpLabelParamHomeLoop", func(b *n.Builder) {
		x := b.ParamNative()
		v := b.Var(b.Float64(8.5))
		b.Enter()
		k := b.Var(b.ConstNative(1))
		loop, exit := b.NewLabel(), b.NewLabel()
		b.Jump(loop)
		b.Bind(loop)
		b.Branch(b.IsZero(b.Get(k)), exit)
		then, other, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
		b.Branch(b.IsZero(x), then, other)
		b.Bind(then)
		b.JumpArgs(done, b.FAdd(b.Float64(1), b.Float64(2)))
		b.Bind(other)
		b.JumpArgs(done, b.ConstBits(64, 5))
		b.BindParamsBits(done, []uint8{64})
		b.Set(k, b.Sub(b.Get(k), b.ConstNative(1)))
		b.Jump(loop)
		b.Bind(exit)
		b.Leave()
		b.Return(b.CastNative(b.FAdd(b.Get(v), b.Float64(0))))
	}, []uintptr{0}, uintptr(math.Float64bits(8.5))},
	{"cap4_4", captureClobber(4, 4), []uintptr{5}, captureClobberWant(4, 4, 5)},
	{"cap10_10", captureClobber(10, 10), []uintptr{5}, captureClobberWant(10, 10, 5)},
	{"cap14_14", captureClobber(14, 14), []uintptr{5}, captureClobberWant(14, 14, 5)},
	{"cap20_20", captureClobber(20, 20), []uintptr{5}, captureClobberWant(20, 20, 5)},
	{"cap30_30", captureClobber(30, 30), []uintptr{5}, captureClobberWant(30, 30, 5)},
	{"fpGetJumpArg32", func(b *n.Builder) {
		x := b.ParamNative()
		v := b.Var(b.CastUnsigned(x, 32))
		b.Set(v, b.FAdd(b.Get(v), b.Float32(1)))
		l := b.NewLabel()
		c := b.Get(v)
		b.Set(v, b.FAdd(c, b.Float32(10)))
		b.JumpArgs(l, c)
		ps := b.BindParamsBits(l, []uint8{32})
		b.Return(b.Add(b.ShiftLeft(b.CastNative(ps[0]), 32), b.CastNative(b.Get(v))))
	}, []uintptr{uintptr(math.Float32bits(1.5))}, uintptr(uint64(math.Float32bits(2.5))<<32 | uint64(math.Float32bits(12.5)))},
	{"fpGetLeaveExport32", func(b *n.Builder) {
		x := b.ParamNative()
		v := b.Var(b.CastUnsigned(x, 32))
		b.Enter()
		k := b.Var(b.ConstNative(2))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Jump(loop)
		b.Bind(loop)
		b.Branch(b.IsZero(b.Get(k)), done)
		b.Set(v, b.FAdd(b.Get(v), b.Float32(1)))
		b.Set(k, b.Sub(b.Get(k), b.ConstNative(1)))
		b.Jump(loop)
		b.Bind(done)
		c := b.Get(v)
		b.Set(v, b.Float32(100))
		b.Leave(c)
		b.Return(b.Add(b.ShiftLeft(b.CastNative(c), 32), b.CastNative(b.Get(v))))
	}, []uintptr{uintptr(math.Float32bits(1.5))}, uintptr(uint64(math.Float32bits(3.5))<<32 | uint64(math.Float32bits(100)))},
	{"fpSwap64Loop", func(b *n.Builder) {
		b.Enter()
		a := b.Var(b.Float64(1))
		c := b.Var(b.Float64(2))
		k := b.Var(b.ConstNative(3))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Jump(loop)
		b.Bind(loop)
		b.Branch(b.IsZero(b.Get(k)), done)
		t0 := b.Get(a)
		b.Set(a, b.FAdd(b.Get(c), b.Float64(0)))
		b.Set(c, b.FMul(t0, b.Float64(1)))
		b.Set(k, b.Sub(b.Get(k), b.ConstNative(1)))
		b.Jump(loop)
		b.Bind(done)
		r := b.FSub(b.Get(a), b.Get(c))
		b.Leave(r)
		b.Return(b.CastNative(b.ShiftRight(r, 32)))
	}, nil, uintptr(math.Float64bits(1) >> 32)},
	{"wideSwapLoop", func(b *n.Builder) {
		b.Enter()
		a := b.Var(b.ConstBits(128, 5))
		c := b.Var(b.Add(b.ConstBits(128, 7), b.ShiftLeft(b.ConstBits(128, 1), 64)))
		k := b.Var(b.ConstNative(3))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Jump(loop)
		b.Bind(loop)
		b.Branch(b.IsZero(b.Get(k)), done)
		t0 := b.Get(a)
		b.Set(a, b.Get(c))
		b.Set(c, t0)
		b.Set(k, b.Sub(b.Get(k), b.ConstNative(1)))
		b.Jump(loop)
		b.Bind(done)
		r := b.Get(a)
		b.Leave(r)
		b.Return(b.Add(b.CastNative(b.ExtractBits(r, 64, 64)), b.CastNative(b.ExtractBits(r, 0, 64))))
	}, nil, 8},
	{"vecSwapLoop", func(b *n.Builder) {
		shape := n.Vec(4, 32)
		b.Enter()
		a := b.Var(b.Splat(shape, b.ConstBits(32, 5)))
		c := b.Var(b.Splat(shape, b.ConstBits(32, 9)))
		k := b.Var(b.ConstNative(3))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Jump(loop)
		b.Bind(loop)
		b.Branch(b.IsZero(b.Get(k)), done)
		t0 := b.Get(a)
		b.Set(a, b.Get(c))
		b.Set(c, t0)
		b.Set(k, b.Sub(b.Get(k), b.ConstNative(1)))
		b.Jump(loop)
		b.Bind(done)
		r := b.Add(b.Mul(b.CastNative(b.ExtractLane(b.Get(a), 1)), b.ConstNative(16)), b.CastNative(b.ExtractLane(b.Get(c), 3)))
		b.Leave(r)
		b.Return(r)
	}, nil, 9*16 + 5},
	{"vals16", manyVals(16), []uintptr{5}, manyVarsWant(16, 5)},
	{"vals17", manyVals(17), []uintptr{5}, manyVarsWant(17, 5)},
	{"vals20", manyVals(20), []uintptr{5}, manyVarsWant(20, 5)},
	{"many15", manyVars(15), []uintptr{5}, manyVarsWant(15, 5)},
	{"many17", manyVars(17), []uintptr{5}, manyVarsWant(17, 5)},
	{"many8", manyVars(8), []uintptr{5}, manyVarsWant(8, 5)},
	{"many12", manyVars(12), []uintptr{5}, manyVarsWant(12, 5)},
	{"many16", manyVars(16), []uintptr{5}, manyVarsWant(16, 5)},
	{"many24", manyVars(24), []uintptr{5}, manyVarsWant(24, 5)},
	{"getSetUseOld", func(b *n.Builder) {
		x := b.ParamNative()
		v := b.Var(x)
		c := b.Get(v)
		b.Set(v, b.Add(c, b.ConstNative(1)))
		b.Return(b.Add(b.Mul(c, b.ConstNative(16)), b.Get(v)))
	}, []uintptr{5}, 5*16 + 6},
	{"swap", func(b *n.Builder) {
		x := b.ParamNative()
		y := b.ParamNative()
		a := b.Var(x)
		c := b.Var(y)
		t0 := b.Get(a)
		b.Set(a, b.Get(c))
		b.Set(c, t0)
		b.Return(b.Add(b.Mul(b.Get(a), b.ConstNative(16)), b.Get(c)))
	}, []uintptr{1, 2}, 2*16 + 1},
	{"swapLoop", func(b *n.Builder) {
		x := b.ParamNative()
		y := b.ParamNative()
		b.Enter()
		a := b.Var(x)
		c := b.Var(y)
		k := b.Var(b.ConstNative(3))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Jump(loop)
		b.Bind(loop)
		b.Branch(b.IsZero(b.Get(k)), done)
		t0 := b.Get(a)
		b.Set(a, b.Get(c))
		b.Set(c, t0)
		b.Set(k, b.Sub(b.Get(k), b.ConstNative(1)))
		b.Jump(loop)
		b.Bind(done)
		r := b.Add(b.Mul(b.Get(a), b.ConstNative(16)), b.Get(c))
		b.Leave(r)
		b.Return(r)
	}, []uintptr{1, 2}, 2*16 + 1},
	{"fibLoop", func(b *n.Builder) {
		cnt := b.ParamNative()
		b.Enter()
		prev := b.Var(b.ConstNative(0))
		cur := b.Var(b.ConstNative(1))
		k := b.Var(cnt)
		loop, done := b.NewLabel(), b.NewLabel()
		b.Jump(loop)
		b.Bind(loop)
		b.Branch(b.IsZero(b.Get(k)), done)
		c := b.Get(cur)
		b.Set(cur, b.Add(c, b.Get(prev)))
		b.Set(prev, c)
		b.Set(k, b.Sub(b.Get(k), b.ConstNative(1)))
		b.Jump(loop)
		b.Bind(done)
		r := b.Get(prev)
		b.Leave(r)
		b.Return(r)
	}, []uintptr{10}, 55},
	{"snapshotAcrossNestedSet", func(b *n.Builder) {
		x := b.ParamNative()
		v := b.Var(x)
		c := b.Get(v)
		b.Enter()
		b.Set(v, b.Add(c, b.ConstNative(5)))
		b.Leave()
		b.Return(b.Add(b.Mul(c, b.ConstNative(256)), b.Get(v)))
	}, []uintptr{3}, 3*256 + 8},
	{"snapshotAcrossNestedLoopSet", func(b *n.Builder) {
		x := b.ParamNative()
		v := b.Var(x)
		c := b.Get(v)
		b.Enter()
		k := b.Var(b.ConstNative(3))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Jump(loop)
		b.Bind(loop)
		b.Branch(b.IsZero(b.Get(k)), done)
		b.Set(v, b.Add(b.Get(v), c))
		b.Set(k, b.Sub(b.Get(k), b.ConstNative(1)))
		b.Jump(loop)
		b.Bind(done)
		b.Leave()
		b.Return(b.Get(v))
	}, []uintptr{3}, 12},
	{"getAsJumpArg", func(b *n.Builder) {
		x := b.ParamNative()
		v := b.Var(x)
		l := b.NewLabel()
		c := b.Get(v)
		b.Set(v, b.Add(c, b.ConstNative(1)))
		b.JumpArgs(l, c)
		ps := b.BindParamsBits(l, []uint8{n.NativeBits})
		b.Return(b.Add(b.Mul(ps[0], b.ConstNative(256)), b.Get(v)))
	}, []uintptr{3}, 3*256 + 4},
	{"getAsLeaveExport", func(b *n.Builder) {
		x := b.ParamNative()
		v := b.Var(x)
		b.Enter()
		c := b.Get(v)
		b.Set(v, b.Add(c, b.ConstNative(1)))
		b.Leave(c)
		b.Return(b.Add(b.Mul(c, b.ConstNative(256)), b.Get(v)))
	}, []uintptr{3}, 3*256 + 4},
	{"innerGetExportAfterSetInLoop", func(b *n.Builder) {
		x := b.ParamNative()
		b.Enter()
		v := b.Var(x)
		k := b.Var(b.ConstNative(2))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Jump(loop)
		b.Bind(loop)
		b.Branch(b.IsZero(b.Get(k)), done)
		b.Set(v, b.Add(b.Get(v), b.ConstNative(10)))
		b.Set(k, b.Sub(b.Get(k), b.ConstNative(1)))
		b.Jump(loop)
		b.Bind(done)
		c := b.Get(v)
		b.Set(v, b.ConstNative(0))
		b.Leave(c)
		b.Return(c)
	}, []uintptr{3}, 23},
	{"coalesceProducerStillLive", func(b *n.Builder) {
		x := b.ParamNative()
		v := b.Var(x)
		y := b.Add(b.Get(v), b.ConstNative(1))
		b.Set(v, y)
		b.Set(v, b.Add(b.Get(v), b.ConstNative(10)))
		b.Return(b.Add(b.Mul(y, b.ConstNative(256)), b.Get(v)))
	}, []uintptr{3}, 4*256 + 14},
	{"setSameValueTwoVars", func(b *n.Builder) {
		x := b.ParamNative()
		a := b.Var(x)
		c := b.Var(x)
		y := b.Add(b.Get(a), b.ConstNative(1))
		b.Set(a, y)
		b.Set(c, y)
		b.Set(a, b.Add(b.Get(a), b.ConstNative(10)))
		b.Return(b.Add(b.Mul(b.Get(c), b.ConstNative(256)), b.Get(a)))
	}, []uintptr{3}, 4*256 + 14},
	{"oneBranchInnerSet", func(b *n.Builder) {
		x := b.ParamNative()
		cond := b.ParamNative()
		v := b.Var(x)
		w := b.Var(b.ConstNative(7))
		b.Enter()
		yes, done := b.NewLabel(), b.NewLabel()
		b.Branch(b.IsZero(cond), done)
		b.Bind(yes)
		b.Set(v, b.Add(b.Get(v), b.Get(w)))
		b.Jump(done)
		b.Bind(done)
		b.Leave()
		b.Return(b.Get(v))
	}, []uintptr{3, 1}, 10},
	{"capturedUseAfterOuterSet", func(b *n.Builder) {
		x := b.ParamNative()
		v := b.Var(x)
		c := b.Get(v)
		b.Enter()
		loop, done := b.NewLabel(), b.NewLabel()
		k := b.Var(b.ConstNative(2))
		b.Jump(loop)
		b.Bind(loop)
		b.Branch(b.IsZero(b.Get(k)), done)
		b.Set(v, b.Add(b.Get(v), b.ConstNative(100)))
		b.Set(k, b.Sub(b.Get(k), b.ConstNative(1)))
		b.Jump(loop)
		b.Bind(done)
		r := b.Add(b.Mul(c, b.ConstNative(1000)), b.Get(v))
		b.Leave(r)
		b.Return(r)
	}, []uintptr{3}, 3*1000 + 203},
	{"floatMixedInt", func(b *n.Builder) {
		x := b.ParamNative()
		v := b.Var(b.Float64(1.5))
		b.Set(v, b.FAdd(b.Get(v), b.Float64(1)))
		r := b.Add(b.Get(v), b.CastUnsigned(x, 64)) // integer use of float bits
		b.Return(b.CastNative(b.ShiftRight(r, 32)))
	}, []uintptr{0}, uintptr(math.Float64bits(2.5) >> 32)},
	{"float32FromLoadLoop", func(b *n.Builder) {
		p := b.ParamNative()
		b.Enter()
		v := b.Var(b.CastUnsigned(p, 32))
		k := b.Var(b.ConstNative(3))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Jump(loop)
		b.Bind(loop)
		b.Branch(b.IsZero(b.Get(k)), done)
		b.Set(v, b.FAdd(b.Get(v), b.Float32(1)))
		b.Set(k, b.Sub(b.Get(k), b.ConstNative(1)))
		b.Jump(loop)
		b.Bind(done)
		r := b.Get(v)
		b.Leave(r)
		b.Return(b.CastNative(r))
	}, []uintptr{uintptr(math.Float32bits(1.5))}, uintptr(math.Float32bits(4.5))},
	{"float32FromLoadLoopArgs", func(b *n.Builder) {
		p := b.ParamNative()
		loop, body, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
		b.JumpArgs(loop, b.LoadBits(p, 32), b.ConstNative(3))
		s := b.BindParamsBits(loop, []uint8{32, n.NativeBits})
		b.BranchArgs(b.IsZero(s[1]), done, []n.Value{s[0]}, body, s)
		s = b.BindParamsBits(body, []uint8{32, n.NativeBits})
		b.JumpArgs(loop, b.FAdd(s[0], b.Float32(1)), b.Sub(s[1], b.ConstNative(1)))
		r := b.BindParamsBits(done, []uint8{32})
		b.Return(b.CastNative(r[0]))
	}, []uintptr{f32ptr}, uintptr(math.Float32bits(4.5))},
	{"float32ParamSetFP", func(b *n.Builder) {
		x := b.ParamNative()
		v := b.Var(b.Float32(0))
		b.Set(v, b.CastUnsigned(x, 32))
		b.Set(v, b.FMul(b.Get(v), b.Float32(2)))
		b.Return(b.CastNative(b.Get(v)))
	}, []uintptr{uintptr(math.Float32bits(1.25))}, uintptr(math.Float32bits(2.5))},
	{"float32IntSetInLoop", func(b *n.Builder) {
		x := b.ParamNative()
		b.Enter()
		v := b.Var(b.Float32(0))
		k := b.Var(b.ConstNative(2))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Jump(loop)
		b.Bind(loop)
		b.Branch(b.IsZero(b.Get(k)), done)
		b.Set(v, b.CastUnsigned(x, 32))
		b.Set(v, b.FAdd(b.Get(v), b.Get(v)))
		b.Set(k, b.Sub(b.Get(k), b.ConstNative(1)))
		b.Jump(loop)
		b.Bind(done)
		r := b.Get(v)
		b.Leave(r)
		b.Return(b.CastNative(r))
	}, []uintptr{uintptr(math.Float32bits(1.25))}, uintptr(math.Float32bits(2.5))},
	{"floatHomeStoredInt", func(b *n.Builder) {
		p := b.ParamNative()
		v := b.Var(b.Float32(1))
		b.Set(v, b.FAdd(b.Get(v), b.Float32(2)))
		b.Store32(p, b.Get(v))
		b.Return(b.CastNative(b.LoadBits(p, 32)))
	}, []uintptr{f32scratch}, uintptr(math.Float32bits(3))},
	{"floatHomeCompare", func(b *n.Builder) {
		v := b.Var(b.Float32(1))
		b.Set(v, b.FAdd(b.Get(v), b.Float32(2)))
		l, d := b.NewLabel(), b.NewLabel()
		b.Branch(b.Compare(b.Get(v), n.BranchEqual, b.ConstBits(32, uint64(math.Float32bits(3)))), l, d)
		b.Bind(l)
		b.Return(b.ConstNative(1))
		b.Bind(d)
		b.Return(b.ConstNative(2))
	}, nil, 1},
}

var f32buf = [4]float32{1.5, 0, 0, 0}
var f32input = [4]byte{0, 0, 0xc0, 0x3f} // little-endian Float32(1.5)
var f32ptr = uintptr(unsafe.Pointer(&f32input[0]))
var f32scratch = uintptrOf(&f32buf[2])

func uintptrOf(p *float32) uintptr { return uintptr(unsafe.Pointer(p)) }
