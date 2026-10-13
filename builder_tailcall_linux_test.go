//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || loong64 || 386)

package nyanjit_test

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/irinterp"
	"github.com/moriyoshi/nyanjit/ir"
)

const tailNativeBits = uint8(unsafe.Sizeof(uintptr(0)) * 8)

// tailForeign is the Func of the mutually tail-calling functions: C ABI
// entries in a foreign thread context, with a small frame budget.
var tailForeign = compile.Func{ABI: abi.C, Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}}

// tailLeaf is the Func of the C leaves the tail calls reach. A platform
// stack makes them ordinary C functions, which preserve callee-saved
// registers.
var tailLeaf = compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}}

// buildTailStep builds f(n, acc, self, other): acc^tag when n is zero, else
// a tail call of other(n-1, acc*3+tag, other, self).
func buildTailStep(b *nyanjit.Builder, tag uint64) {
	n, acc, self, other := b.ParamNative(), b.ParamNative(), b.ParamNative(), b.ParamNative()
	done := b.NewLabel()
	b.Branch(b.IsZero(n), done)
	next := b.Add(b.Mul(acc, b.ConstNative(3)), b.ConstNative(uintptr(tag)))
	b.TailCallC(other, b.Sub(n, b.ConstNative(1)), next, other, self)
	b.Bind(done)
	b.Return(b.CastUnsigned(b.Xor(acc, b.ConstNative(uintptr(tag))), 64))
}

// tailStepWant computes buildTailStep's pair from a's side in Go.
func tailStepWant(n uint64, tags [2]uint64) uint64 {
	mask := ^uint64(0) >> (64 - tailNativeBits)
	acc, side := uint64(0), 0
	for ; n != 0; n-- {
		acc = (acc*3 + tags[side]) & mask
		side ^= 1
	}
	return (acc ^ tags[side]) & mask
}

func bindTail(t *testing.T, unit *compile.Unit) *execution.Code {
	t.Helper()
	template, err := unit.Template()
	if err != nil {
		t.Fatal(err)
	}
	code, err := template.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// TestTailCallMutualRecursion runs two generated foreign functions that
// tail-call each other 10^6 times, which overflows a thread stack unless
// each call reuses its caller's frame. Both tiers, through a bound Builder
// and through Finish and Lower.
func TestTailCallMutualRecursion(t *testing.T) {
	tags := [2]uint64{0x1234, 0x5a5a}
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
		if err != nil {
			t.Fatal(err)
		}
		for _, bound := range []bool{false, true} {
			var codes [2]*execution.Code
			var addresses [2]uintptr
			for side, tag := range tags {
				var unit *compile.Unit
				if bound {
					b := nyanjit.NewBuilderFor(target, tailForeign)
					buildTailStep(b, tag)
					unit, err = b.Lower()
				} else {
					b := nyanjit.NewBuilder()
					buildTailStep(b, tag)
					program, ferr := b.Finish()
					if ferr != nil {
						t.Fatal(ferr)
					}
					unit, err = compile.Lower(target, program, tailForeign)
				}
				if err != nil {
					t.Fatalf("tier %d bound %t: %v", tier, bound, err)
				}
				codes[side] = bindTail(t, unit)
				defer codes[side].Close()
				if addresses[side], err = codes[side].EntryAddress(); err != nil {
					t.Fatal(err)
				}
			}
			for _, n := range []uint64{0, 1, 2, 7, 1_000_000} {
				got, err := codes[0].Call(uintptr(n), 0, addresses[0], addresses[1])
				if want := tailStepWant(n, tags); err != nil || uint64(got) != want {
					t.Errorf("tier %d bound %t n=%d: got %#x, %v; want %#x", tier, bound, n, got, err, want)
				}
			}
		}
	}
}

// TestTailCallStraight tail-calls without control flow, into a C ABI
// function with fewer parameters, and is an exact call plus return for the
// interpreter.
func TestTailCallStraight(t *testing.T) {
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
		if err != nil {
			t.Fatal(err)
		}
		callee := nyanjit.NewBuilderFor(target, tailLeaf)
		x := callee.ParamNative()
		callee.Return(callee.CastUnsigned(callee.Mul(x, x), 64))
		unit, err := callee.Lower()
		if err != nil {
			t.Fatal(err)
		}
		code := bindTail(t, unit)
		defer code.Close()
		address, _ := code.EntryAddress()
		b := nyanjit.NewBuilder()
		fn, a, c := b.ParamNative(), b.ParamNative(), b.ParamNative()
		b.TailCallC(fn, b.Add(a, c))
		program, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		lowered, err := compile.Lower(target, program, tailForeign)
		if err != nil {
			t.Fatalf("tier %d: %v", tier, err)
		}
		caller := bindTail(t, lowered)
		defer caller.Close()
		got, err := caller.Call(address, 5, 6)
		if err != nil || got != 121 {
			t.Errorf("tier %d: got %d, %v; want 121", tier, got, err)
		}
		result, err := irinterp.Run(program, []uint64{0x1000, 5, 6}, &irinterp.Options{Call: func(c irinterp.Call) (uint64, error) {
			if c.Op != ir.OpCallC || c.Index != -1 || c.Target != 0x1000 || len(c.Args) != 1 {
				t.Errorf("interpreter call %+v", c)
			}
			return c.Args[0] * c.Args[0], nil
		}})
		if err != nil || result.Lo != 121 || result.Bits != 64 {
			t.Errorf("interpreter: %+v, %v", result, err)
		}
	}
}

// TestTailCallContexts rejects a tail call outside a C ABI foreign or
// external function with ErrUnsupported on both tiers.
func TestTailCallContexts(t *testing.T) {
	b := nyanjit.NewBuilder()
	b.TailCallC(b.ParamNative(), b.ParamNative())
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	platform := compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range []compile.Func{
			{ABI: abi.C, Context: compile.ContextNative, Stack: platform},
			{ABI: abi.C, Context: compile.ContextGoManaged, Stack: platform, AssumePointerFree: true},
			{ABI: abi.Go, Context: compile.ContextForeign, Stack: platform},
		} {
			if _, err := compile.Lower(target, program, f); !errors.Is(err, compile.ErrUnsupported) {
				t.Errorf("tier %d %+v: %v", tier, f, err)
			}
		}
		if _, err := compile.Lower(target, program, compile.Func{ABI: abi.C, Context: compile.ContextExternal, Stack: platform}); err != nil {
			t.Errorf("tier %d external: %v", tier, err)
		}
	}
}

// lowerTail lowers build for target and f and binds it.
func lowerTail(t *testing.T, target compile.Target, f compile.Func, build func(*nyanjit.Builder)) (*execution.Code, uintptr) {
	t.Helper()
	b := nyanjit.NewBuilderFor(target, f)
	build(b)
	unit, err := b.Lower()
	if err != nil {
		t.Fatal(err)
	}
	code := bindTail(t, unit)
	address, err := code.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	return code, address
}

// TestTailCallPreservesCallerRegisters tail-calls from a function that keeps
// values in callee-saved registers across an ordinary call: it restores them
// before the jump, so its caller's values survive.
func TestTailCallPreservesCallerRegisters(t *testing.T) {
	mask := ^uint64(0) >> (64 - tailNativeBits)
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
		if err != nil {
			t.Fatal(err)
		}
		leaf := tailLeaf
		g, gAddress := lowerTail(t, target, leaf, func(b *nyanjit.Builder) {
			x := b.ParamNative()
			b.Return(b.CastUnsigned(b.Mul(x, b.ConstNative(7)), 64))
		})
		defer g.Close()
		h, hAddress := lowerTail(t, target, leaf, func(b *nyanjit.Builder) {
			x, y := b.ParamNative(), b.ParamNative()
			b.Return(b.CastUnsigned(b.Sub(x, y), 64))
		})
		defer h.Close()
		f, fAddress := lowerTail(t, target, tailForeign, func(b *nyanjit.Builder) {
			gv, hv, a, c := b.ParamNative(), b.ParamNative(), b.ParamNative(), b.ParamNative()
			product := b.CallC(gv, a)
			b.TailCallC(hv, b.CastUnsigned(product, tailNativeBits), c)
		})
		defer f.Close()
		outer, _ := lowerTail(t, target, tailForeign, func(b *nyanjit.Builder) {
			fv, gv, hv, x := b.ParamNative(), b.ParamNative(), b.ParamNative(), b.ParamNative()
			v1 := b.Mul(x, b.ConstNative(3))
			v2 := b.Add(x, b.ConstNative(11))
			v3 := b.Xor(x, b.ConstNative(0x55))
			v4 := b.Sub(b.ConstNative(1000), x)
			r := b.CastUnsigned(b.CallC(fv, gv, hv, x, v1), tailNativeBits)
			b.Return(b.CastUnsigned(b.Add(b.Add(b.Add(r, v1), b.Add(v2, v3)), v4), 64))
		})
		defer outer.Close()
		for _, x := range []uint64{0, 5, 1234567} {
			got, err := outer.Call(fAddress, gAddress, hAddress, uintptr(x))
			want := (7*x + (x + 11) + (x ^ 0x55) + (1000 - x)) & mask
			if err != nil || uint64(got) != want {
				t.Errorf("tier %d x=%d: got %d, %v; want %d", tier, x, got, err, want)
			}
		}
	}
}

// TestTailCallFromLoop tail-calls out of a loop that carries a Var, with a
// value from the entry block: Var promotion and cross-block threading see the
// tail call's operands as uses.
func TestTailCallFromLoop(t *testing.T) {
	mask := ^uint64(0) >> (64 - tailNativeBits)
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
		if err != nil {
			t.Fatal(err)
		}
		h, hAddress := lowerTail(t, target, tailLeaf, func(b *nyanjit.Builder) {
			x, y := b.ParamNative(), b.ParamNative()
			b.Return(b.CastUnsigned(b.Sub(x, y), 64))
		})
		defer h.Close()
		f, _ := lowerTail(t, target, tailForeign, func(b *nyanjit.Builder) {
			n, hv := b.ParamNative(), b.ParamNative()
			i, acc := b.Var(b.ConstNative(0)), b.Var(b.ConstNative(0))
			loop, done := b.NewLabel(), b.NewLabel()
			b.Bind(loop)
			b.Branch(b.Compare(b.Get(i), ir.BranchEqual, n), done)
			x := b.Get(i)
			b.Set(acc, b.Add(b.Get(acc), b.Mul(x, x)))
			b.Set(i, b.Add(x, b.ConstNative(1)))
			b.Jump(loop)
			b.Bind(done)
			b.TailCallC(hv, b.Get(acc), n)
		})
		defer f.Close()
		for _, n := range []uint64{0, 1, 10, 1000} {
			want := uint64(0)
			for i := uint64(0); i < n; i++ {
				want += i * i
			}
			want = (want - n) & mask
			got, err := f.Call(uintptr(n), hAddress)
			if err != nil || uint64(got) != want {
				t.Errorf("tier %d n=%d: got %d, %v; want %d", tier, n, got, err, want)
			}
		}
	}
}

// TestTailCallBound tail-calls from a bound Builder whose function is one
// block, and from a hardened one.
func TestTailCallBound(t *testing.T) {
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
		if err != nil {
			t.Fatal(err)
		}
		h, hAddress := lowerTail(t, target, tailLeaf, func(b *nyanjit.Builder) {
			x, y := b.ParamNative(), b.ParamNative()
			b.Return(b.CastUnsigned(b.Sub(x, y), 64))
		})
		defer h.Close()
		for _, harden := range []bool{false, true} {
			f := tailForeign
			f.Harden = harden
			b := nyanjit.NewBuilderFor(target, f)
			hv, x := b.ParamNative(), b.ParamNative()
			b.TailCallC(hv, b.Mul(x, x), x)
			unit, err := b.Lower()
			if errors.Is(err, compile.ErrUnsupported) && harden {
				continue
			}
			if err != nil {
				t.Fatalf("tier %d harden %t: %v", tier, harden, err)
			}
			code := bindTail(t, unit)
			got, err := code.Call(hAddress, 9)
			code.Close()
			if err != nil || got != 72 {
				t.Errorf("tier %d harden %t: got %d, %v; want 72", tier, harden, got, err)
			}
		}
	}
}

func TestTailCallBuilderErrors(t *testing.T) {
	for name, build := range map[string]func(b *nyanjit.Builder){
		"nested scope": func(b *nyanjit.Builder) {
			x := b.ParamNative()
			b.Enter()
			b.TailCallC(x)
		},
		"narrow target": func(b *nyanjit.Builder) { b.TailCallC(b.ParamBits(16)) },
		"closed block": func(b *nyanjit.Builder) {
			x := b.ParamNative()
			b.TailCallC(x)
			b.TailCallC(x)
		},
	} {
		b := nyanjit.NewBuilder()
		build(b)
		if _, err := b.Finish(); !errors.Is(err, compile.ErrInvalidFunction) {
			t.Errorf("%s: %v", name, err)
		}
	}
	b := nyanjit.NewBuilder()
	x := b.ParamNative()
	args := make([]nyanjit.Value, abi.MaxArguments()+1)
	for i := range args {
		args[i] = x
	}
	b.TailCallC(x, args...)
	var limit *compile.LimitError
	if _, err := b.Finish(); !errors.As(err, &limit) || limit.Kind != compile.LimitArguments {
		t.Errorf("too many arguments: %v", err)
	}
}
