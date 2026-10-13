//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"runtime"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// hardenCase is a function run hardened at both tiers: each call's
// arguments, and its expected result.
type hardenCase struct {
	name    string
	build   func(*nyanjit.Builder)
	calls   [][]any
	want    func(args []any) uint64
	control bool // has labels: rejected where there is no speculation barrier
}

// hardenFunc runs on a thread stack, whose frame limit both tiers' frames
// fit in on every target.
var hardenFunc = compile.Func{ABI: abi.C, Context: compile.ContextForeign, Harden: true, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}}

func mask(w uint8) uint64 { return ^uint64(0) >> (64 - w) }

// narrowArg passes a w-bit argument as Words expects it.
func narrowArg(w uint8, x uint64) any {
	switch {
	case w == 64:
		return x
	case builder.NativeBits == 32:
		return uintptr(x)
	case w == 32:
		return uint32(x)
	case w == 16:
		return uint16(x)
	}
	return uint8(x)
}

func argWord(a any) uint64 {
	switch x := a.(type) {
	case uint64:
		return x
	case uint32:
		return uint64(x)
	case uint16:
		return uint64(x)
	case uint8:
		return uint64(x)
	case uintptr:
		return uint64(x)
	}
	panic(fmt.Sprintf("argument %T", a))
}

func hardenCases() []hardenCase {
	var cs []hardenCase
	inputs := []uint64{0, 1, 0x12345678, 0x8000000000000001, ^uint64(0)}
	for _, w := range []uint8{8, 16, 32, 64} {
		k := uint64(0x87a5c3e10f2d4b69) & mask(w)
		var calls [][]any
		for _, x := range inputs {
			calls = append(calls, []any{narrowArg(w, x&mask(w))})
		}
		cs = append(cs, hardenCase{
			name: fmt.Sprintf("constants%d", w),
			build: func(b *nyanjit.Builder) {
				x := b.ParamBits(w)
				b.Return(b.Add(b.Xor(x, b.ConstBits(w, k)), b.Sub(b.ConstBits(w, k>>1), b.ConstBits(w, 0))))
			},
			calls: calls,
			want:  func(a []any) uint64 { return ((argWord(a[0]) ^ k) + k>>1) & mask(w) },
		})
		for _, count := range []uint8{1, w / 2, w - 1} {
			cs = append(cs, hardenCase{
				name: fmt.Sprintf("shifts%d-%d", w, count),
				build: func(b *nyanjit.Builder) {
					x := b.ParamBits(w)
					v := b.Xor(b.ShiftLeft(x, count), b.ShiftRight(x, count))
					v = b.Add(v, b.ShiftRightArithmetic(x, count))
					v = b.Xor(v, b.Add(b.RotateLeft(x, count), b.ShiftLeft(b.RotateRight(x, count), 1)))
					b.Return(v)
				},
				calls: calls,
				want: func(a []any) uint64 {
					x, m := argWord(a[0]), mask(w)
					sign := uint64(int64(x<<(64-w))>>(64-w)>>count) & m
					rotl := (x<<count | x>>(w-count)) & m
					rotr := (x>>count | x<<(w-count)) & m
					v := (x << count & m) ^ x>>count
					v = (v + sign) & m
					return v ^ (rotl+(rotr<<1&m))&m
				},
			})
		}
	}
	for _, w := range []uint8{32, 64} {
		cs = append(cs, hardenCase{
			name: fmt.Sprintf("float%d", w),
			build: func(b *nyanjit.Builder) {
				x := b.ParamBits(w)
				c := b.Float64(1.25)
				if w == 32 {
					c = b.Float32(1.25)
				}
				b.Return(b.FAdd(b.FMul(x, c), c))
			},
			calls: [][]any{{narrowArg(w, f2bits(w, 2))}, {narrowArg(w, f2bits(w, -3.5))}},
			want: func(a []any) uint64 {
				x := argWord(a[0])
				if w == 32 {
					return uint64(math.Float32bits(math.Float32frombits(uint32(x))*1.25 + 1.25))
				}
				return math.Float64bits(math.Float64frombits(x)*1.25 + 1.25)
			},
		})
	}
	for _, scale := range []uint64{2, 8, 65537} {
		cs = append(cs, hardenCase{
			name: fmt.Sprintf("scaled%d", scale),
			build: func(b *nyanjit.Builder) {
				base, index := b.Param(), b.Param()
				b.Return(b.Add(base, b.Mul(index, b.Const(scale))))
			},
			calls: [][]any{{uint64(1), uint64(2)}, {uint64(0x12345678ffffffff), uint64(0xdeadbeefcabba032)}, {^uint64(0), ^uint64(0)}},
			want:  func(a []any) uint64 { return argWord(a[0]) + argWord(a[1])*scale },
		})
	}
	for _, constant := range []uint64{0, 1, 4095, 4096, 1 << 31, 1 << 32, 1 << 63, ^uint64(0)} {
		for _, condition := range []ir.BranchCondition{ir.BranchEqual, ir.BranchLess, ir.BranchBelow, ir.BranchAboveEqual, ir.BranchGreater, ir.BranchBelowEqual} {
			var calls [][]any
			for _, x := range []uint64{0, constant - 1, constant, constant + 1, ^uint64(0)} {
				calls = append(calls, []any{x})
			}
			cs = append(cs, hardenCase{
				name: fmt.Sprintf("compare%d-%#x", condition, constant),
				build: func(b *nyanjit.Builder) {
					x := b.Param()
					yes := b.NewLabel()
					b.Branch(b.CompareImmediate(x, condition, int64(constant)), yes)
					b.Return(b.Const(0))
					b.Bind(yes)
					b.Return(b.Const(1))
				},
				calls: calls,
				want: func(a []any) uint64 {
					if compareReference(condition, argWord(a[0]), constant) {
						return 1
					}
					return 0
				},
				control: true,
			})
		}
	}
	cs = append(cs, hardenCase{
		name: "loop-vars",
		build: func(b *nyanjit.Builder) {
			n := b.Param()
			i, acc := b.Var(b.Const(0)), b.Var(b.Const(0x9e3779b97f4a7c15))
			loop, done := b.NewLabel(), b.NewLabel()
			b.Bind(loop)
			b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, n), done)
			x := b.Get(i)
			b.Set(acc, b.Add(b.RotateLeft(b.Get(acc), 7), b.Xor(b.Mul(x, b.Const(0x100000001b3)), b.ShiftRight(x, 3))))
			b.Set(i, b.Add(b.Get(i), b.Const(1)))
			b.Jump(loop)
			b.Bind(done)
			b.Return(b.Get(acc))
		},
		calls: [][]any{{uint64(0)}, {uint64(1)}, {uint64(100)}},
		want: func(a []any) uint64 {
			acc := uint64(0x9e3779b97f4a7c15)
			for i := uint64(0); i < argWord(a[0]); i++ {
				acc = bits.RotateLeft64(acc, 7) + (i*0x100000001b3 ^ i>>3)
			}
			return acc
		},
		control: true,
	})
	// Bounds checks in a loop under register pressure, so the checked
	// index lives in registers of either bank or in slots.
	data := make([]uint64, 8)
	for i := range data {
		data[i] = uint64(i)*0x9e3779b97f4a7c15 + 1
	}
	const vars = 20
	cs = append(cs, hardenCase{
		name: "checked-loop",
		build: func(b *nyanjit.Builder) {
			base, n, count := b.ParamNative(), b.ParamNative(), b.ParamNative()
			i := b.Var(b.ConstNative(0))
			a := make([]nyanjit.Var, vars)
			for k := range a {
				a[k] = b.Var(b.Const(uint64(k) * 0x100000001b3))
			}
			loop, bad, next, done := b.NewLabel(), b.NewLabel(), b.NewLabel(), b.NewLabel()
			b.Bind(loop)
			b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, n), done)
			x := b.Load64(b.IndexedAddress(base, b.Get(i), count, 8, bad))
			for k := range a {
				b.Set(a[k], b.Add(b.Get(a[k]), b.Xor(x, b.Get(a[(k+1)%vars]))))
			}
			b.Jump(next)
			b.Bind(bad)
			b.Set(a[0], b.Add(b.Get(a[0]), b.Const(1)))
			b.Bind(next)
			b.Set(i, b.Add(b.Get(i), b.ConstNative(1)))
			b.Jump(loop)
			b.Bind(done)
			v := b.Get(a[0])
			for _, x := range a[1:] {
				v = b.Xor(v, b.Get(x))
			}
			b.Return(v)
		},
		calls: [][]any{
			{uintptr(unsafe.Pointer(&data[0])), uintptr(0), uintptr(8)},
			{uintptr(unsafe.Pointer(&data[0])), uintptr(12), uintptr(8)},
			{uintptr(unsafe.Pointer(&data[0])), uintptr(5), uintptr(0)},
		},
		want: func(args []any) uint64 {
			n, count := argWord(args[1]), argWord(args[2])
			var a [vars]uint64
			for k := range a {
				a[k] = uint64(k) * 0x100000001b3
			}
			for i := uint64(0); i < n; i++ {
				if i >= count {
					a[0]++
					continue
				}
				x := data[i]
				for k := range a {
					a[k] += x ^ a[(k+1)%vars]
				}
			}
			v := a[0]
			for _, x := range a[1:] {
				v ^= x
			}
			return v
		},
		control: true,
	})
	return cs
}

func f2bits(w uint8, x float64) uint64 {
	if w == 32 {
		return uint64(math.Float32bits(float32(x)))
	}
	return math.Float64bits(x)
}

// Both tiers compute the same results for hardened functions, and accept or
// reject them alike.
func TestBaselineHardenMatchesOptimized(t *testing.T) {
	for _, c := range hardenCases() {
		b := nyanjit.NewBuilder()
		c.build(b)
		p, err := b.Finish()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
			code, err := nyanjit.Compile(p, compile.TargetSpec{Tier: tier}, hardenFunc)
			if c.control && !hardensControl() {
				if !errors.Is(err, compile.ErrUnsupported) {
					t.Errorf("%s tier %d: hardened control flow accepted: %v", c.name, tier, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("%s tier %d: %v", c.name, tier, err)
			}
			for _, args := range c.calls {
				lo, hi, err := code.CallPair(execution.Words(args...)...)
				got := execution.Wide(lo, hi)
				if want := c.want(args); err != nil || got != want {
					t.Errorf("%s tier %d %v: got %#x want %#x err %v", c.name, tier, args, got, want, err)
				}
			}
			code.Close()
		}
	}
}

// boundsCheck builds a checked uint64 load at index width w: data[index]
// when index < count, otherwise 0xdead.
func boundsCheck(t *testing.T, w uint8, dataIndex bool) *ir.SealedProgram {
	t.Helper()
	b := nyanjit.NewBuilder()
	bad := b.NewLabel()
	if dataIndex {
		view := nyanjit.SliceParam[uint64](b)
		index := b.ParamNative()
		b.Return(view.Index(index, bad).Load())
	} else {
		base, index, count := b.ParamBits(w), b.ParamBits(w), b.ParamBits(w)
		b.Return(b.Load64(b.IndexedAddress(base, index, count, 8, bad)))
	}
	b.Bind(bad)
	b.Return(b.Const(0xdead))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Hardened bounds checks give the same results at both tiers: in-bounds
// elements, and the failure edge for out-of-range, wrapped and nil inputs.
func TestBaselineHardenCheckedIndex(t *testing.T) {
	data := []uint64{0xaabbccdd, 0x12345678, 0x9999}
	for _, w := range []uint8{builder.NativeBits, 64} {
		p := boundsCheck(t, w, false)
		for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
			code, err := nyanjit.Compile(p, compile.TargetSpec{Tier: tier}, hardenFunc)
			if !hardensControl() {
				if !errors.Is(err, compile.ErrUnsupported) {
					t.Errorf("tier %d: hardened control flow accepted: %v", tier, err)
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range []struct{ index, count, want uint64 }{
				{0, 2, data[0]}, {1, 2, data[1]}, {2, 2, 0xdead}, {0, 0, 0xdead}, {^uint64(0), 2, 0xdead}, {1 << 32, 2, 0xdead}, {2, 3, data[2]},
			} {
				base := uint64(uintptr(unsafe.Pointer(&data[0])))
				args := execution.Words(uintptr(base), uintptr(c.index), uintptr(c.count))
				if w == 64 {
					args = execution.Words(base, c.index, c.count)
				} else if uint64(uintptr(c.index)) != c.index {
					continue
				}
				lo, hi, err := code.CallPair(args...)
				if err != nil || execution.Wide(lo, hi) != c.want {
					t.Errorf("width %d tier %d index %#x count %d: got %#x want %#x err %v", w, tier, c.index, c.count, execution.Wide(lo, hi), c.want, err)
				}
			}
			lo, hi, err := code.CallPair(make([]uintptr, 3*int(w/builder.NativeBits))...)
			if err != nil || execution.Wide(lo, hi) != 0xdead {
				t.Errorf("width %d tier %d nil: %#x %v", w, tier, execution.Wide(lo, hi), err)
			}
			code.Close()
		}
		runtime.KeepAlive(data)
	}
	p := boundsCheck(t, builder.NativeBits, true)
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		code, err := nyanjit.Compile(p, compile.TargetSpec{Tier: tier}, hardenFunc)
		if !hardensControl() {
			if !errors.Is(err, compile.ErrUnsupported) {
				t.Errorf("Data.Index tier %d: hardened control flow accepted: %v", tier, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, slice := range [][]uint64{nil, {}, {11, 22}} {
			for _, index := range []uintptr{0, 1, 2, ^uintptr(0)} {
				lo, hi, err := code.CallPair(execution.Words(&slice, index)...)
				want := uint64(0xdead)
				if index < uintptr(len(slice)) {
					want = slice[index]
				}
				if err != nil || execution.Wide(lo, hi) != want {
					t.Errorf("Data.Index tier %d index %d len %d: got %#x want %#x err %v", tier, index, len(slice), execution.Wide(lo, hi), want, err)
				}
			}
		}
		code.Close()
	}
}

// With its bounds branch removed, so that every call takes the in-bounds
// path as a mistrained predictor would, a hardened baseline bounds check
// loads element zero for an out-of-range index: the clamp follows the
// comparison's flags, not the branch.
func TestBaselineHardenClampsMistrainedBranch(t *testing.T) {
	if !hardensControl() {
		t.Skip("control hardening unsupported")
	}
	data := make([]uint64, 16)
	for i := range data {
		data[i] = 100 + uint64(i)
	}
	for _, w := range []uint8{builder.NativeBits, 64} {
		p := boundsCheck(t, w, false)
		target, err := compile.NewTarget(compile.TargetSpec{Tier: compile.TierBaseline})
		if err != nil {
			t.Fatal(err)
		}
		u, err := compile.Lower(target, p, hardenFunc)
		if err != nil {
			t.Fatal(err)
		}
		template, err := u.Template()
		if err != nil {
			t.Fatal(err)
		}
		code, err := template.Bytes(nil)
		if err != nil {
			t.Fatal(err)
		}
		if !removeBoundsBranch(code) {
			listing, _ := u.Listing()
			t.Fatalf("width %d: no bounds branch to remove:\n%s", w, listing)
		}
		words := 3 * int(w/builder.NativeBits)
		report := u.Report()
		patched := execution.NewTemplate(code, report, nil, execution.CodeOptions{
			ABI: abi.C, Foreign: true, Arity: 3, ArgumentWords: words, ResultWords: 1, ParamBits: []uint8{w, w, w},
			StackBytes: report.StackBytes() + report.CallStackBytes(),
		}, goregister.Frame{})
		c, err := patched.Bind(nil)
		if err != nil {
			t.Fatal(err)
		}
		base := uint64(uintptr(unsafe.Pointer(&data[0])))
		for _, test := range []struct{ index, want uint64 }{{1, data[1]}, {9, data[0]}, {2, data[0]}} {
			args := execution.Words(uintptr(base), uintptr(test.index), uintptr(2))
			if w == 64 {
				args = execution.Words(base, test.index, uint64(2))
			}
			lo, hi, err := c.CallPair(args...)
			if err != nil || execution.Wide(lo, hi) != test.want {
				t.Errorf("width %d index %d: got %d want %d err %v", w, test.index, execution.Wide(lo, hi), test.want, err)
			}
		}
		runtime.KeepAlive(data)
		c.Close()
	}
}

// removeBoundsBranch replaces the first conditional branch of a baseline
// bounds check, which leaves for the failure block, with a no-op of its size.
func removeBoundsBranch(code []byte) bool {
	switch runtime.GOARCH {
	case "amd64", "386":
		for i := 0; i+6 <= len(code); i++ {
			if code[i] == 0x0f && code[i+1] == 0x83 { // JAE rel32
				copy(code[i:], []byte{0x66, 0x0f, 0x1f, 0x44, 0x00, 0x00})
				return true
			}
		}
	case "arm64":
		for i := 0; i+4 <= len(code); i += 4 {
			if w := binary.LittleEndian.Uint32(code[i:]); w&0xff000010 == 0x54000000 { // B.cond
				binary.LittleEndian.PutUint32(code[i:], 0xd503201f)
				return true
			}
		}
	case "ppc64le":
		for i := 0; i+4 <= len(code); i += 4 {
			if w := binary.LittleEndian.Uint32(code[i:]); w>>26 == 16 { // BC
				binary.LittleEndian.PutUint32(code[i:], 0x60000000)
				return true
			}
		}
	}
	return false
}

// A hardened baseline template places each binding's entry at a random
// offset in its mapping.
func TestBaselineHardenEntryPlacement(t *testing.T) {
	b := nyanjit.NewBuilder()
	b.Return(b.Add(b.Param(), b.Const(1)))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	template, err := lowerTemplate(p, compile.TargetSpec{Tier: compile.TierBaseline}, compile.Func{ABI: abi.C, Harden: true})
	if err != nil {
		t.Fatal(err)
	}
	offsets := map[uintptr]bool{}
	for range 8 {
		c, err := template.Bind(nil)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := c.EntryAddress()
		if err != nil {
			t.Fatal(err)
		}
		offsets[entry&4095] = true
		if got, err := c.Call(execution.Words(uint64(41))...); err != nil || got != 42 {
			t.Fatal(got, err)
		}
		c.Close()
	}
	if len(offsets) < 2 {
		t.Errorf("entry offsets %v: placement is not randomized", offsets)
	}
}
