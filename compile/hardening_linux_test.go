//go:build linux && (amd64 || arm64 || 386 || riscv64 || loong64 || ppc64le)

package compile_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// hardensControl reports whether Harden accepts control flow on this host.
// riscv64 and loong64 define no speculation barrier.
func hardensControl() bool { return runtime.GOARCH != "riscv64" && runtime.GOARCH != "loong64" }

func TestHardenScalarAndFloatBits(t *testing.T) {
	for _, w := range []uint8{8, 16, 32, 64} {
		for _, floating := range []bool{false, true} {
			if floating && w < 32 {
				continue
			}
			b := nyanjit.NewBuilder()
			x := b.ParamBits(w)
			k := uint64(0x87a5c3e10f2d4b69) & (^uint64(0) >> (64 - w))
			var want func(uint64) uint64
			if floating {
				c := b.Float64(1.25)
				if w == 32 {
					c = b.Float32(1.25)
				}
				b.Return(b.FAdd(b.FMul(x, c), c))
				want = func(x uint64) uint64 {
					if w == 32 {
						return uint64(math.Float32bits(math.Float32frombits(uint32(x))*1.25 + 1.25))
					}
					return math.Float64bits(math.Float64frombits(x)*1.25 + 1.25)
				}
			} else {
				b.Return(b.Add(b.Xor(x, b.ConstBits(w, k)), b.ConstBits(w, k)))
				want = func(x uint64) uint64 { return ((x ^ k) + k) & (^uint64(0) >> (64 - w)) }
			}
			p, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			c, err := nyanjit.Compile(p, compile.TargetSpec{}, compile.Func{ABI: abi.C, Harden: true})
			if err != nil {
				t.Fatalf("width%d fp%v: %v", w, floating, err)
			}
			for _, x := range []uint64{0, 1, 0x12345678, 0xffffffffffffffff} {
				if floating {
					x = math.Float64bits(2)
					if w == 32 {
						x = uint64(math.Float32bits(2))
					}
				}
				x &= ^uint64(0) >> (64 - w)
				lo, hi, err := c.CallPair(execution.Words(x)...)
				// Words passes logical uint64 on 386 as two words; narrow parameters
				// occupy one word instead.
				if builder.NativeBits == 32 && w <= 32 {
					lo, hi, err = c.CallPair(uintptr(x))
				}
				if err != nil || execution.Wide(lo, hi) != want(x) {
					t.Fatalf("width%d fp%v x%x: %x/%x %v", w, floating, x, execution.Wide(lo, hi), want(x), err)
				}
			}
			c.Close()
		}
	}
}

func TestHardenCheckedIndex(t *testing.T) {
	for _, width := range []uint8{builder.NativeBits, 64} {
		b := nyanjit.NewBuilder()
		a, i, count := b.ParamBits(width), b.ParamBits(width), b.ParamBits(width)
		bad := b.NewLabel()
		address := b.IndexedAddress(a, i, count, 8, bad)
		b.Return(b.Load64(address))
		b.Bind(bad)
		b.Return(b.Const(0xdead))
		p, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		template, err := lowerTemplate(p, compile.TargetSpec{}, compile.Func{ABI: abi.C, Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}, Harden: true})
		if !hardensControl() {
			if !errors.Is(err, compile.ErrUnsupported) {
				t.Fatalf("unsupported hardening control accepted: %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		c, err := template.Bind(nil)
		if err != nil {
			t.Fatal(err)
		}
		data := []uint64{0xaabbccdd, 0x12345678}
		for _, test := range []struct{ index, count, want uint64 }{{0, 2, data[0]}, {1, 2, data[1]}, {2, 2, 0xdead}, {0, 0, 0xdead}, {^uint64(0), 2, 0xdead}} {
			var args []uintptr
			if width == builder.NativeBits {
				args = execution.Words(&data[0], uintptr(test.index), uintptr(test.count))
			} else {
				args = execution.Words(uint64(uintptr(unsafe.Pointer(&data[0]))), test.index, test.count)
			}
			lo, hi, err := c.CallPair(args...)
			if err != nil || execution.Wide(lo, hi) != test.want {
				t.Fatalf("width%d index%x count%d: got%x want%x err%v", width, test.index, test.count, execution.Wide(lo, hi), test.want, err)
			}
			runtime.KeepAlive(data)
		}
		// A zero-length nil slice takes the failure edge without accessing memory.
		var args []uintptr
		if width == builder.NativeBits {
			args = []uintptr{0, 0, 0}
		} else {
			args = execution.Words(uint64(0), uint64(0), uint64(0))
		}
		lo, hi, err := c.CallPair(args...)
		if err != nil || execution.Wide(lo, hi) != 0xdead {
			t.Fatal(lo, hi, err)
		}
		c.Close()
	}
}

func TestHardenControlEmitsClampAndBarrier(t *testing.T) {
	if !hardensControl() {
		t.Skip("control hardening unsupported")
	}
	b := nyanjit.NewBuilder()
	a, i, count := b.ParamNative(), b.ParamNative(), b.ParamNative()
	bad := b.NewLabel()
	b.Return(b.LoadBits(b.IndexedAddress(a, i, count, 1, bad), 8))
	b.Bind(bad)
	b.Return(b.ConstBits(8, 0))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	template, err := lowerTemplate(p, compile.TargetSpec{}, compile.Func{Context: compile.ContextExternal, Harden: true, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
	if err != nil {
		t.Fatal(err)
	}
	code, err := template.Bytes(nil)
	if err != nil {
		t.Fatal(err)
	}
	barrier, clamp := []byte{0x0f, 0xae, 0xe8}, []byte{0x0f, 0x43, 0xc1}
	switch runtime.GOARCH {
	case "arm64":
		barrier = []byte{0x9f, 0x3f, 0x03, 0xd5, 0xdf, 0x3f, 0x03, 0xd5}
		clamp = []byte{0x00, 0x30, 0x81, 0x9a}
	case "ppc64le":
		barrier = []byte{0x00, 0x00, 0xff, 0x63} // ori r31, r31, 0
		clamp = []byte{0x1e, 0x20, 0x63, 0x7c}   // isel r3, r3, r4, lt
	}
	if bytes.Count(code, barrier) < 2 || !bytes.Contains(code, clamp) {
		t.Fatalf("missing bounds dependency/barriers: %x", code)
	}
}

func TestHardenVectorBitsAndScope(t *testing.T) {
	for _, vectors := range []bool{false, true} {
		b := nyanjit.NewBuilder()
		pointer := b.ParamNative()
		data := []byte{0x13, 0x37, 0x42, 0x24, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
		if vectors {
			v := b.ConstVector(ir.Vec(4, 32), data)
			b.VStore(pointer, b.Xor(v, b.Splat(ir.Vec(4, 32), b.ConstBits(32, 0x12345678))))
			b.Return(b.Const(7))
		} else {
			b.Enter()
			x := b.Var(b.Const(0x3141592653589793))
			b.Set(x, b.Add(b.Get(x), b.Const(0x2718281828459045)))
			result := b.Get(x)
			b.Leave(result)
			b.Return(result)
		}
		p, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		c, err := nyanjit.Compile(p, compile.TargetSpec{}, compile.Func{Harden: true})
		if err != nil {
			t.Fatal(err)
		}
		output := new([16]byte)
		lo, hi, err := c.CallPair(execution.Words(output)...)
		if err != nil {
			t.Fatal(err)
		}
		want := uint64(0x3141592653589793 + 0x2718281828459045)
		if vectors {
			want = 7
			for k := 0; k < 4; k++ {
				x := binary.LittleEndian.Uint32(data[k*4:]) ^ 0x12345678
				if got := binary.LittleEndian.Uint32(output[k*4:]); got != x {
					t.Fatalf("lane%d=%x want%x", k, got, x)
				}
			}
		}
		if execution.Wide(lo, hi) != want {
			t.Fatal(execution.Wide(lo, hi), want)
		}
		c.Close()
	}
}

// The generated frame's registration must start at the randomized body, not
// at its trap prefix. Func retains that mapping after Close and through GC.
func TestHardenManagedFuncLifetime(t *testing.T) {
	b := nyanjit.NewBuilder()
	x := b.Param()
	b.Return(b.Add(x, b.Const(0x3141592653589793)))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	c, err := nyanjit.Compile(p, compile.TargetSpec{}, compile.Func{ABI: abi.Go, Context: compile.ContextGoManaged, Harden: true, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 256}, AssumePointerFree: true})
	if errors.Is(err, execution.ErrUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	var f func(uint64) uint64
	err = c.Func(&f)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	if got := f(17); got != 17+0x3141592653589793 {
		t.Fatal(got)
	}
}

func TestHardenDataIndex(t *testing.T) {
	b := nyanjit.NewBuilder()
	view := nyanjit.SliceParam[uint64](b)
	index := b.ParamNative()
	bad := b.NewLabel()
	b.Return(view.Index(index, bad).Load())
	b.Bind(bad)
	b.Return(b.Const(0xdead))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	template, err := lowerTemplate(p, compile.TargetSpec{}, compile.Func{Harden: true, ABI: abi.C, Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
	if !hardensControl() {
		if !errors.Is(err, compile.ErrUnsupported) {
			t.Fatal(err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	c, err := template.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, slice := range [][]uint64{nil, {}, {11, 22}} {
		for _, idx := range []uintptr{0, 1, 2, ^uintptr(0)} {
			got, hi, err := c.CallPair(execution.Words(&slice, idx)...)
			want := uint64(0xdead)
			if idx < uintptr(len(slice)) {
				want = slice[idx]
			}
			if err != nil || execution.Wide(got, hi) != want {
				t.Fatalf("index%d len%d result%x want%x %v", idx, len(slice), execution.Wide(got, hi), want, err)
			}
		}
	}
}

func hardeningGoCallback(x uint64) uint64 {
	var pcs [32]uintptr
	count := runtime.Callers(0, pcs[:])
	found := false
	for _, pc := range pcs[:count] {
		if f := runtime.FuncForPC(pc); f != nil && f.Name() == "nyanjit.generated" {
			found = true
		}
	}
	runtime.GC()
	if !found {
		return 0
	}
	return x + 7
}

func TestHardenManagedCallbackUnwind(t *testing.T) {
	b := nyanjit.NewBuilder()
	x := b.Param()
	b.Return(b.CallGo(b.ConstNative(reflect.ValueOf(hardeningGoCallback).Pointer()), x))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	c, err := nyanjit.Compile(p, compile.TargetSpec{}, compile.Func{ABI: abi.Go, Context: compile.ContextGoManaged, Harden: true, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640}, AssumePointerFree: true})
	if errors.Is(err, execution.ErrUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	lo, hi, err := c.CallPair(execution.Words(uint64(17))...)
	if err != nil || execution.Wide(lo, hi) != 24 {
		t.Fatal(lo, hi, err)
	}
}

func TestHardenCompareImmediateEdges(t *testing.T) {
	if !hardensControl() {
		t.Skip("control hardening unsupported")
	}
	for _, constant := range []uint64{0, 1, 4095, 4096, 1 << 31, 1 << 32, 1 << 63, ^uint64(0)} {
		for _, condition := range []ir.BranchCondition{ir.BranchEqual, ir.BranchLess, ir.BranchBelow, ir.BranchAboveEqual} {
			b := nyanjit.NewBuilder()
			x := b.Param()
			yes, no := b.NewLabel(), b.NewLabel()
			b.Branch(b.CompareImmediate(x, condition, int64(constant)), yes)
			b.Jump(no)
			b.Bind(yes)
			b.Return(b.Const(1))
			b.Bind(no)
			b.Return(b.Const(0))
			p, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			c, err := nyanjit.Compile(p, compile.TargetSpec{}, compile.Func{Harden: true, ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 240}})
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range []uint64{0, constant - 1, constant, constant + 1, ^uint64(0)} {
				lo, hi, err := c.CallPair(execution.Words(value)...)
				want := uint64(0)
				if compareReference(condition, value, constant) {
					want = 1
				}
				if err != nil || execution.Wide(lo, hi) != want {
					t.Fatalf("c%d x%x k%x got%x want%x err%v", condition, value, constant, execution.Wide(lo, hi), want, err)
				}
			}
			c.Close()
		}
	}
}

func TestHardenControlReportIncludesClampedArgument(t *testing.T) {
	if builder.NativeBits != 64 {
		t.Skip("cross-target fixture uses 64-bit builder addresses")
	}
	b := nyanjit.NewBuilder()
	base, index, count := b.ParamNative(), b.ParamNative(), b.ParamNative()
	bad := b.NewLabel()
	b.Return(b.LoadBits(b.IndexedAddress(base, index, count, 1, bad), 8))
	b.Bind(bad)
	b.Return(b.ConstBits(8, 0))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	u, err := lowerUnit(p, compile.TargetSpec{Architecture: "amd64"}, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640}, Harden: true})
	if err != nil {
		t.Fatal(err)
	}
	// The clamp selects the index or zero in RAX (CMOVAE RAX, RCX) and
	// writes the result back where the index lives: the C argument register
	// RSI, a copy of it, or, when RAX or RCX holds the copy, that register's
	// save slot, which the clamp's restore reloads.
	listing, err := u.Listing()
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, line := range strings.Split(listing, "\n") {
		if _, instruction, ok := strings.Cut(line, "  "); ok {
			rows = append(rows, instruction)
		}
	}
	clamped := ""
	saved := map[string]string{} // the register last stored to each slot
	for i, row := range rows {
		if slot, register, ok := strings.Cut(strings.TrimPrefix(row, "mov qword ptr "), ", "); ok && strings.HasPrefix(row, "mov qword ptr [RSP") {
			saved[slot] = register
		}
		if row != ".byte 48 0f 43 c1" || i+1 == len(rows) {
			continue
		}
		destination, source, _ := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(rows[i+1], "mov "), "qword ptr "), ", ")
		if source != "RAX" {
			break
		}
		if register := saved[destination]; register == "RAX" || register == "RCX" {
			destination = register
		}
		clamped = destination
	}
	if clamped == "" || strings.Contains(clamped, "[") {
		t.Fatalf("no register clamp write-back in:\n%s", listing)
	}
	if !slices.Contains(u.Report().ClobberedRegisters(), clamped) {
		t.Fatalf("clamped index register %s missing from clobbers: %v\n%s", clamped, u.Report().ClobberedRegisters(), listing)
	}
}

func TestHardenScaledUint64(t *testing.T) {
	for _, scale := range []uint64{2, 8, 65537} {
		b := nyanjit.NewBuilder()
		base, index := b.Param(), b.Param()
		b.Return(b.Add(base, b.Mul(index, b.Const(scale))))
		p, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		c, err := nyanjit.Compile(p, compile.TargetSpec{}, compile.Func{Harden: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, test := range [][2]uint64{{1, 2}, {0x12345678ffffffff, 0xdeadbeefcabba032}, {1 << 63, 1 << 62}, {^uint64(0), ^uint64(0)}} {
			lo, hi, err := c.CallPair(execution.Words(test[0], test[1])...)
			if err != nil || execution.Wide(lo, hi) != test[0]+test[1]*scale {
				t.Fatalf("scale%d args%x got%x err%v", scale, test, execution.Wide(lo, hi), err)
			}
		}
		c.Close()
	}
}

func TestHardenWrappedIndexArithmetic(t *testing.T) {
	if !hardensControl() {
		t.Skip("control hardening unsupported")
	}
	for _, constantCount := range []bool{false, true} {
		b := nyanjit.NewBuilder()
		base, raw := b.ParamNative(), b.ParamNative()
		count := b.ConstNative(3)
		if !constantCount {
			count = b.ParamNative()
		}
		bad := b.NewLabel()
		index := b.Add(raw, count)
		b.Return(b.Load64(b.IndexedAddress(base, index, count, 8, bad)))
		b.Bind(bad)
		b.Return(b.Const(0xdead))
		p, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		c, err := nyanjit.Compile(p, compile.TargetSpec{}, compile.Func{Harden: true, ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 240}})
		if err != nil {
			t.Fatal(err)
		}
		data := new([3]uint64)
		*data = [3]uint64{101, 202, 303}
		var args []uintptr
		if constantCount {
			args = execution.Words(data, ^uintptr(0))
		} else {
			args = execution.Words(data, ^uintptr(0), uintptr(3))
		}
		lo, hi, err := c.CallPair(args...)
		if err != nil || execution.Wide(lo, hi) != 303 {
			t.Fatalf("constantCount%v: wrapped index2 got%d want303 err%v", constantCount, execution.Wide(lo, hi), err)
		}
		c.Close()
	}
}

func TestHardenConstantBoundEmitsClamp(t *testing.T) {
	if !hardensControl() {
		t.Skip("control hardening unsupported")
	}
	b := nyanjit.NewBuilder()
	base, index := b.ParamNative(), b.ParamNative()
	bad := b.NewLabel()
	b.Return(b.LoadBits(b.IndexedAddress(base, index, b.ConstNative(3), 1, bad), 8))
	b.Bind(bad)
	b.Return(b.ConstBits(8, 0))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	template, err := lowerTemplate(p, compile.TargetSpec{}, compile.Func{Context: compile.ContextExternal, Harden: true, ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 240}})
	if err != nil {
		t.Fatal(err)
	}
	code, err := template.Bytes(nil)
	if err != nil {
		t.Fatal(err)
	}
	clamp := []byte{0x0f, 0x43, 0xc1}
	switch runtime.GOARCH {
	case "arm64":
		clamp = []byte{0, 0x30, 0x81, 0x9a}
	case "ppc64le":
		clamp = []byte{0x1e, 0x20, 0x63, 0x7c} // isel r3, r3, r4, lt
	}
	if !bytes.Contains(code, clamp) {
		t.Fatal("constant bounds edge omitted clamp")
	}
}

// Blinding keeps a constant in the registers its value has: on 386 the
// narrow carry's 0xffff mask is one word although its Bits keep the 64-bit
// default (security audit 2026-10-25, L20).
func TestHardenNarrowCarryConstants(t *testing.T) {
	for _, w := range []uint8{8, 16, 32} {
		for _, control := range []bool{false, true} {
			pb := ir.NewProgramBuilder()
			entry := pb.Entry()
			x, y := pb.Param(entry, ir.Scalar(w)), pb.Param(entry, ir.Scalar(w))
			carry := pb.Append(entry, ir.Instruction{Op: ir.OpCarryAdd, Bits: w, A: x, B: y})
			if control {
				done := pb.NewBlock(ir.Scalar(w))
				pb.Branch(entry, &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchEqual, Value: x, Immediate: 3}, done, []int{y}, done, []int{carry})
				pb.Return(done, pb.BlockParam(done, 0))
			} else {
				pb.Return(entry, carry)
			}
			program, err := pb.Seal()
			if err != nil {
				t.Fatal(err)
			}
			f := compile.Func{ABI: abi.C, Harden: true}
			for _, arch := range []string{"386", "amd64", "arm64", "ppc64le"} {
				target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch})
				if err != nil {
					t.Fatal(err)
				}
				unit, err := compile.Lower(target, program, f)
				if err == nil {
					_, err = unit.Listing()
				}
				if err != nil {
					t.Errorf("%s w=%d control=%v: %v", arch, w, control, err)
				}
			}
			if !hardensControl() {
				continue // a ProgramBuilder program always has control steps
			}
			code, err := nyanjit.Compile(program, compile.TargetSpec{}, f)
			if err != nil {
				t.Fatalf("w=%d control=%v: %v", w, control, err)
			}
			mask := uint64(1)<<w - 1
			for _, in := range [][2]uint64{{mask, 1}, {mask >> 1, 1}, {1, 2}, {mask, mask}} {
				got, err := code.Call(uintptr(in[0]), uintptr(in[1]))
				want := uint64(0)
				if in[0]+in[1] > mask {
					want = 1
				}
				if control && in[0] == 3 {
					want = in[1]
				}
				if err != nil || uint64(got) != want {
					t.Errorf("w=%d control=%v carry(%#x, %#x) = %#x, %v; want %#x", w, control, in[0], in[1], got, err, want)
				}
			}
			code.Close()
		}
	}
}
