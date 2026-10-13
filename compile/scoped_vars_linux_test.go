//go:build linux && (amd64 || 386 || arm64 || riscv64 || ppc64 || ppc64le)

package compile_test

import (
	"math"
	"reflect"
	"runtime"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/execution"
	"github.com/moriyoshi/nyanjit/ir"
)

func varFinish(t *testing.T, b *n.Builder) *ir.Program {
	t.Helper()
	p, e := b.Finish()
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func varExecute(t *testing.T, p *ir.Program, stack abi.StackPractice, args ...uintptr) uintptr {
	t.Helper()
	o := abi.CompileOptions{Stack: stack}
	if stack == abi.StackPlatform {
		o.StackBytesAvailable = 8192
	}
	c, e := compile.Compile(p, o)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	x, e := c.Call(args...)
	if e != nil {
		t.Fatal(e)
	}
	return x
}

func TestScopedVarLoopAndCaptures(t *testing.T) {
	for _, stack := range []abi.StackPractice{abi.StackPlatform, abi.StackNoUse} {
		b := n.NewBuilder()
		input := b.ParamNative()
		b.Enter()
		count := b.Var(input)
		sum := b.Var(b.ConstNative(0))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Jump(loop)
		b.Bind(loop)
		b.Branch(b.IsZero(b.Get(count)), done)
		b.Set(sum, b.Add(b.Get(sum), input))
		b.Set(count, b.Sub(b.Get(count), b.ConstNative(1)))
		b.Jump(loop)
		b.Bind(done)
		result := b.Get(sum)
		b.Leave(result)
		b.Return(result)
		if got := varExecute(t, varFinish(t, b), stack, 7); got != 49 {
			t.Fatalf("stack%d got%d", stack, got)
		}
	}
}
func TestScopedVarBothArmsAndSnapshot(t *testing.T) {
	b := n.NewBuilder()
	condition := b.ParamNative()
	v := b.Var(b.ConstNative(1))
	yes, no, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.Branch(b.IsZero(condition), yes, no)
	b.Bind(yes)
	b.Set(v, b.ConstNative(10))
	b.Jump(done)
	b.Bind(no)
	b.Set(v, b.ConstNative(20))
	b.Jump(done)
	b.Bind(done)
	old := b.Get(v)
	b.Set(v, b.ConstNative(5))
	b.Return(b.Add(old, b.Get(v)))
	p := varFinish(t, b)
	for _, stack := range []abi.StackPractice{abi.StackPlatform, abi.StackNoUse} {
		for _, input := range []uintptr{0, 1} {
			want := uintptr(15)
			if input != 0 {
				want = 25
			}
			if got := varExecute(t, p, stack, input); got != want {
				t.Fatalf("input%d stack%d got%d", input, stack, got)
			}
		}
	}
}
func TestScopedVarNestedLoopsAndOuterSet(t *testing.T) {
	b := n.NewBuilder()
	total := b.Var(b.ConstNative(0))
	b.Enter()
	outer := b.Var(b.ConstNative(4))
	loop, done := b.NewLabel(), b.NewLabel()
	b.Jump(loop)
	b.Bind(loop)
	b.Branch(b.IsZero(b.Get(outer)), done)
	b.Enter()
	inner := b.Var(b.ConstNative(3))
	term, exit := b.NewLabel(), b.NewLabel()
	b.Jump(term)
	b.Bind(term)
	b.Branch(b.IsZero(b.Get(inner)), exit)
	b.Set(total, b.Add(b.Get(total), b.Get(outer)))
	b.Set(inner, b.Sub(b.Get(inner), b.ConstNative(1)))
	b.Jump(term)
	b.Bind(exit)
	b.Leave()
	b.Set(outer, b.Sub(b.Get(outer), b.ConstNative(1)))
	b.Jump(loop)
	b.Bind(done)
	b.Leave()
	b.Return(b.Get(total))
	p := varFinish(t, b)
	for _, stack := range []abi.StackPractice{abi.StackPlatform, abi.StackNoUse} {
		if got := varExecute(t, p, stack); got != 30 {
			t.Fatalf("nested stack%d got%d", stack, got)
		}
	}
}
func TestScopedVarMixedLabelParams(t *testing.T) {
	b := n.NewBuilder()
	v := b.Var(b.ConstNative(3))
	label := b.NewLabel()
	b.JumpArgs(label, b.ConstNative(4))
	x := b.BindParamsBits(label, []uint8{n.NativeBits})
	b.Set(v, b.Add(b.Get(v), x[0]))
	b.Return(b.Get(v))
	if got := varExecute(t, varFinish(t, b), abi.StackPlatform); got != 7 {
		t.Fatal(got)
	}
}
func TestScopedVectorAndMaskVars(t *testing.T) {
	b := n.NewBuilder()
	shape := n.Vec(4, 32)
	v := b.Var(b.Splat(shape, b.ConstBits(32, 5)))
	m := b.Var(b.VCompare(b.Get(v), n.BranchEqual, b.Get(v)).Value())
	b.Enter()
	loop, done := b.NewLabel(), b.NewLabel()
	k := b.Var(b.ConstNative(2))
	b.Jump(loop)
	b.Bind(loop)
	b.Branch(b.IsZero(b.Get(k)), done)
	b.Set(v, b.InsertLane(b.Get(v), 2, b.ConstBits(32, 9)))
	b.Set(k, b.Sub(b.Get(k), b.ConstNative(1)))
	b.Jump(loop)
	b.Bind(done)
	b.Leave()
	b.Return(b.Add(b.CastNative(b.ExtractLane(b.Get(v), 2)), b.CastNative(b.MaskBits(b.Get(m).Mask()))))
	p := varFinish(t, b)
	for _, stack := range []abi.StackPractice{abi.StackPlatform, abi.StackNoUse} {
		if got := varExecute(t, p, stack); got != 24 {
			t.Fatalf("vector stack%d got%d", stack, got)
		}
	}
}

//go:noinline
func scopedGoCall(x uintptr) uint64 { runtime.GC(); return uint64(x) + 1 }
func TestScopedVarPressureCalls(t *testing.T) {
	for _, callGo := range []bool{false, true} {
		var address uintptr
		if callGo {
			address = reflect.ValueOf(scopedGoCall).Pointer()
		} else {
			var e error
			address, e = execution.RegisterForeignCallback(func(x uintptr) uint64 { runtime.GC(); return uint64(x) + 1 })
			if e != nil {
				t.Fatal(e)
			}
		}
		b := n.NewBuilder()
		outer := make([]n.Var, 10)
		for i := range outer {
			outer[i] = b.Var(b.ConstNative(uintptr(i + 1)))
		}
		var fp n.Var
		withFP := runtime.GOARCH != "386"
		if withFP {
			fp = b.Var(b.Float64(2))
		}
		b.Enter()
		inner := make([]n.Var, 10)
		for i := range inner {
			inner[i] = b.Var(b.ConstNative(uintptr(100 + i)))
		}
		k := b.Var(b.ConstNative(3))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Jump(loop)
		b.Bind(loop)
		b.Branch(b.IsZero(b.Get(k)), done)
		var called n.Value
		if callGo {
			called = b.CallGo(b.ConstNative(address), b.Get(k))
		} else {
			called = b.CallC(b.ConstNative(address), b.Get(k))
		}
		b.Set(outer[0], b.Add(b.Get(outer[0]), b.CastNative(called)))
		for _, v := range inner {
			b.Set(v, b.Add(b.Get(v), b.ConstNative(1)))
		}
		if withFP {
			b.Set(fp, b.FAdd(b.Get(fp), b.Float64(1)))
		}
		b.Set(k, b.Sub(b.Get(k), b.ConstNative(1)))
		b.Jump(loop)
		b.Bind(done)
		sum := b.Get(inner[0])
		b.Leave(sum)
		for _, v := range outer {
			sum = b.Add(sum, b.Get(v))
		}
		if withFP {
			bits := b.Get(fp)
			sum = b.Add(sum, b.Select(b.Xor(bits, b.Const(math.Float64bits(5))), b.ConstNative(0), b.ConstNative(1000)))
		}
		b.Return(sum)
		p := varFinish(t, b)
		c, e := compile.CompileGo(p, abi.CompileOptions{ABI: abi.Go, Stack: abi.StackPlatform, StackBytesAvailable: 640, AssumePointerFree: true})
		if e != nil {
			t.Fatal(e)
		}
		got, e := c.Call()
		c.Close()
		want := uintptr(103 + 55 + 9)
		if withFP {
			want += 1000
		}
		if e != nil || got != want {
			t.Fatalf("GoCall%v got%d,%v want%d", callGo, got, e, want)
		}
	}
}

func TestScopedWideVarSnapshot(t *testing.T) {
	// Basic Get/Set pairing works in both frame and workspace storage.
	b := n.NewBuilder()
	v := b.Var(b.ConstBits(128, 1))
	old := b.Get(v)
	b.Set(v, b.ConstBits(128, 5))
	b.Return(b.Add(b.CastNative(old), b.CastNative(b.Get(v))))
	p := varFinish(t, b)
	for _, stack := range []abi.StackPractice{abi.StackPlatform, abi.StackNoUse} {
		if got := varExecute(t, p, stack); got != 6 {
			t.Fatalf("wide snapshot stack%d got%d", stack, got)
		}
	}
	// Exercise a carry into the high half separately. The existing 386 wide
	// carry expansion needs a platform frame for its paired shifts.
	b = n.NewBuilder()
	v = b.Var(b.ConstBits(128, ^uint64(0)))
	old = b.Get(v)
	b.Set(v, b.Add(b.Get(v), b.ConstBits(128, 1)))
	b.Set(v, b.Add(b.Get(v), b.ConstBits(128, 5)))
	b.Return(b.Add(b.CastNative(old), b.CastNative(b.Get(v))))
	if got := varExecute(t, varFinish(t, b), abi.StackPlatform); got != 4 {
		t.Fatalf("wide carry snapshot got%d", got)
	}
}

func TestScopedVarWorkspacePressure(t *testing.T) {
	b := n.NewBuilder()
	outer := make([]n.Var, 16)
	for i := range outer {
		outer[i] = b.Var(b.ConstNative(uintptr(i + 1)))
	}
	withFP := runtime.GOARCH != "386"
	var fp n.Var
	if withFP {
		fp = b.Var(b.Float64(1.5))
	}
	b.Enter()
	inner := make([]n.Var, 16)
	for i := range inner {
		inner[i] = b.Var(b.ConstNative(uintptr(100 + i)))
	}
	k := b.Var(b.ConstNative(3))
	loop, exit := b.NewLabel(), b.NewLabel()
	b.Jump(loop)
	b.Bind(loop)
	b.Branch(b.IsZero(b.Get(k)), exit)
	for i, v := range inner {
		b.Set(v, b.Add(b.Get(v), b.ConstNative(1)))
		b.Set(outer[i], b.Add(b.Get(outer[i]), b.ConstNative(2)))
	}
	if withFP {
		b.Set(fp, b.FAdd(b.Get(fp), b.Float64(1)))
	}
	b.Set(k, b.Sub(b.Get(k), b.ConstNative(1)))
	b.Jump(loop)
	b.Bind(exit)
	total := b.Get(inner[0])
	for _, v := range inner[1:] {
		total = b.Add(total, b.Get(v))
	}
	b.Leave(total)
	for _, v := range outer {
		total = b.Add(total, b.Get(v))
	}
	if withFP {
		total = b.Add(total, b.Select(b.Xor(b.Get(fp), b.Const(math.Float64bits(4.5))), b.ConstNative(0), b.ConstNative(1000)))
	}
	b.Return(total)
	p := varFinish(t, b)
	report, e := compile.Analyze(p, abi.CompileOptions{Stack: abi.StackNoUse})
	if e != nil {
		t.Fatal(e)
	}
	if report.StackBytes != 0 || report.WorkspaceBytes < 32*n.NativeBits/8 || report.SpillStorage != abi.SpillWorkspace {
		t.Fatalf("workspace report:%+v", report)
	}
	want := uintptr(16*103 + 120 + 136 + 96)
	if withFP {
		want += 1000
	}
	if got := varExecute(t, p, abi.StackNoUse); got != want {
		t.Fatalf("workspace pressure got%d want%d", got, want)
	}
}

func TestScopedFPSnapshotAcrossSet(t *testing.T) {
	if runtime.GOARCH == "386" {
		t.Skip("386 has no FP backend")
	}
	for _, width := range []uint8{32, 64} {
		b := n.NewBuilder()
		constant := func(x float64) n.Value {
			if width == 32 {
				return b.Float32(float32(x))
			}
			return b.Float64(x)
		}
		v := b.Var(constant(1.25))
		old := b.Get(v)
		b.Set(v, b.FMul(b.Get(v), constant(2)))
		b.Return(b.CastNative(b.FAdd(old, b.Get(v))))
		want := uint64(math.Float64bits(3.75))
		if width == 32 {
			want = uint64(math.Float32bits(3.75))
		}
		p := varFinish(t, b)
		for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
			if got := varExecute(t, p, stack); uint64(got) != want {
				t.Fatalf("width %d stack %d got %#x want %#x", width, stack, got, want)
			}
		}
	}
}

func TestFPLabelEdgesAndCycles(t *testing.T) {
	if runtime.GOARCH == "386" {
		t.Skip("386 has no FP backend")
	}
	for _, width := range []uint8{32, 64} {
		b := n.NewBuilder()
		loop, body, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
		constant := func(x float64) n.Value {
			if width == 32 {
				return b.Float32(float32(x))
			}
			return b.Float64(x)
		}
		count := b.ParamNative()
		widths := []uint8{width, width, n.NativeBits}
		b.JumpArgs(loop, constant(1.25), constant(2.5), count)
		s := b.BindParamsBits(loop, widths)
		b.BranchArgs(b.IsZero(s[2]), done, []n.Value{s[0], s[1]}, body, s)
		s = b.BindParamsBits(body, widths)
		b.JumpArgs(loop, s[1], s[0], b.Sub(s[2], b.ConstNative(1)))
		s = b.BindParamsBits(done, []uint8{width, width})
		b.Return(b.CastNative(b.FSub(s[0], s[1])))
		p := varFinish(t, b)
		for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
			for _, count := range []uintptr{0, 1, 2, 3} {
				value := -1.25
				if count%2 == 1 {
					value = 1.25
				}
				want := math.Float64bits(value)
				if width == 32 {
					want = uint64(math.Float32bits(float32(value)))
				}
				if got := varExecute(t, p, stack, count); uint64(got) != want {
					t.Fatalf("width%d stack%d count%d got%#x want%#x", width, stack, count, got, want)
				}
			}
		}
	}
}

func TestVarSymbolInitializationKeepsRelocation(t *testing.T) {
	b := n.NewBuilder()
	v := b.Var(b.ConstNative(0))
	b.Return(b.Get(v))
	p := varFinish(t, b)
	// Symbol() has the legacy 64-bit width. Public IR also supports a native
	// word symbol on 386, so use that shape for this portable regression.
	p.Instructions[p.Variables[0].Initial[0]].Symbol = "var_initial"
	template, err := compile.EmitTemplate(p, abi.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(template.Relocations) != 1 || template.Relocations[0].Symbol != "var_initial" {
		t.Fatalf("symbol was replaced with numeric zero:%+v", template.Relocations)
	}
}
