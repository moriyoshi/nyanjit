//go:build linux && loong64

package compile_test

import (
	"encoding/binary"
	"errors"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
	"github.com/moriyoshi/nyanjit/compile"
)

//go:noinline
func loongGrow(depth int) uint64 {
	var a [8192]byte
	a[0] = byte(depth)
	if depth == 0 {
		runtime.GC()
		runtime.KeepAlive(a)
		return 0
	}
	v := loongGrow(depth - 1)
	runtime.KeepAlive(a)
	return v
}

//go:noinline
func loongSixteen(a, b, c, d, e, f, g, h, i, j, k, l, m, o, p, q uint64) uint64 {
	loongGrow(6)
	return a + 2*b + 3*c + 4*d + 5*e + 6*f + 7*g + 8*h + 9*i + 10*j + 11*k + 12*l + 13*m + 14*o + 15*p + 16*q
}
func TestLoong64SixteenGoArgumentsAndHomes(t *testing.T) {
	for _, entry := range []abi.Flavor{abi.Go, abi.C} {
		for _, params := range []bool{false, true} {
			b := n.NewBuilder()
			var args []n.Value
			var incoming []any
			var want uint64
			if params {
				for i := 0; i < 8; i++ {
					args = append(args, b.Param())
					incoming = append(incoming, uintptr(i+11))
					want += uint64(i+1) * uint64(i+11)
				}
			}
			for i := len(args); i < 16; i++ {
				args = append(args, b.Const(uint64(i+1)))
				want += uint64((i + 1) * (i + 1))
			}
			// Repeated live use forces real spills above every Go argument home.
			live := b.Var(b.Const(99))
			r := b.CallGo(b.ConstNative(reflect.ValueOf(loongSixteen).Pointer()), args...)
			b.Return(b.Add(r, b.Get(live)))
			want += 99
			p, e := testFinishRaw(b)
			if e != nil {
				t.Fatal(e)
			}
			template, e := lowerTemplate(p, compile.TargetSpec{}, compile.Func{ABI: entry, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 512}, AssumePointerFree: true})
			if e != nil {
				t.Fatal(e)
			}
			report := template.Report()
			if report.StackBytes() < 144 {
				t.Fatalf("no sixteen-argument home area: %+v", report.Snapshot())
			}
			if entry == abi.C {
				for _, reg := range []int{23, 24, 25, 26, 27, 28, 29, 30, 31} {
					if slices.Contains(report.ClobberedRegisters(), "R"+strconv.Itoa(reg)) {
						t.Fatalf("C register R%d not restored", reg)
					}
				}
				for r := 24; r < 32; r++ {
					if slices.Contains(report.ClobberedFPRegisters(), "F"+strconv.Itoa(r)) {
						t.Fatalf("C FP F%d not restored", r)
					}
				}
			}
			code, e := template.Bind(nil)
			if e != nil {
				t.Fatal(e)
			}
			got, e := code.Call(execution.Words(incoming...)...)
			code.Close()
			if e != nil || uint64(got) != want {
				t.Fatalf("entry%d params%t got%d want%d err%v", entry, params, got, want, e)
			}
		}
	}
}
func TestLoong64CWordExtensionAndCallbacks(t *testing.T) {
	// A raw one-word C entry exposes the upper half: uint32 C arguments must
	// arrive sign-extended in LP64D, although IR values themselves are unsigned.
	e := loong64.NewByteAssembler(abi.StackNoUse, 0)
	e.Ret()
	mem, err := execution.Map(e.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	defer execution.Unmap(mem)
	address := execution.Entry(mem)
	for _, managed := range []bool{false, true} {
		b := n.NewBuilder()
		x := b.ParamBits(32)
		b.Return(b.CallC(b.ConstNative(address), x))
		p, err := testFinishRaw(b)
		if err != nil {
			t.Fatal(err)
		}
		fn := compile.Func{ABI: abi.C, Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 512}, AssumePointerFree: managed}
		if managed {
			fn.Context = compile.ContextGoManaged
		}
		code, err := n.Compile(p, compile.TargetSpec{}, fn)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range []uint32{0, 1, 0x7fffffff, 0x80000000, 0xffffffff} {
			got, err := code.Call(execution.Words(uintptr(v))...)
			want := uint64(int64(int32(v)))
			if err != nil || uint64(got) != want {
				t.Fatalf("managed%t v%#x got%#x want%#x err%v", managed, v, got, want, err)
			}
		}
		code.Close()
	}
	cb, err := execution.RegisterForeignCallback(func(v uintptr) uintptr { runtime.GC(); return v + 7 })
	if err != nil {
		t.Fatal(err)
	}
	b := n.NewBuilder()
	x := b.Var(b.Param())
	y := b.CallC(b.ConstNative(cb), b.Get(x))
	b.Return(b.Add(y, b.Get(x)))
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	code, err := n.Compile(p, compile.TargetSpec{}, compile.Func{ABI: abi.C, Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	if got, e := code.Call(execution.Words(17)...); e != nil || got != 41 {
		t.Fatalf("C callback live Var got%d err%v", got, e)
	}
}
func TestLoong64LibcCall(t *testing.T) {
	h, err := purego.Dlopen("libc.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		t.Fatal(err)
	}
	defer purego.Dlclose(h)
	address, err := purego.Dlsym(h, "labs")
	if err != nil {
		t.Fatal(err)
	}
	for _, managed := range []bool{false, true} {
		b := n.NewBuilder()
		x := b.Param()
		b.Return(b.CallC(b.ConstNative(address), x))
		p, err := testFinishRaw(b)
		if err != nil {
			t.Fatal(err)
		}
		fn := compile.Func{ABI: abi.C, Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 512}, AssumePointerFree: managed}
		if managed {
			fn.Context = compile.ContextGoManaged
		}
		code, err := n.Compile(p, compile.TargetSpec{}, fn)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range []int64{42, -42, -0x123456789ab} {
			got, err := code.Call(execution.Words(uintptr(v))...)
			want := v
			if want < 0 {
				want = -want
			}
			if err != nil || int64(got) != want {
				t.Fatalf("labs(%d)=%d,%v want%d", v, got, err, want)
			}
		}
		code.Close()
	}
}

func TestLoong64CallPairBoundaryAndStackPolicy(t *testing.T) {
	b := n.NewBuilder()
	b.Return(b.Concat(b.Const(0x1122334455667788), b.Const(0x99aabbccddeeff00)))
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	c, err := n.Compile(p, compile.TargetSpec{}, compile.Func{ABI: abi.C})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	entry, err := c.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	lo, hi := execution.CallForeignPair(entry, nil)
	if uint64(lo) != 0x1122334455667788 || uint64(hi) != 0x99aabbccddeeff00 {
		t.Fatalf("LP64D a0/a1 got%#x:%#x", hi, lo)
	}
	b = n.NewBuilder()
	b.Return(b.CallC(b.ConstNative(entry)))
	p, err = testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	_, err = lowerListing(p, compile.TargetSpec{Architecture: "loong64"}, compile.Func{Context: compile.ContextExternal, Stack: compile.StackPolicy{Practice: abi.StackNoUse}})
	if !errors.Is(err, compile.ErrStackRequired) {
		t.Fatalf("outbound calls require a platform call frame: %v", err)
	}
}

//go:noinline
func loongGrowTarget(v uint64) uint64 { loongGrow(6); return v + 7 }

func TestLoong64ForeignThreadReentersRegisteredCode(t *testing.T) {
	h, err := purego.Dlopen("libc.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		t.Fatal(err)
	}
	defer purego.Dlclose(h)
	create, err := purego.Dlsym(h, "pthread_create")
	if err != nil {
		t.Fatal(err)
	}
	join, err := purego.Dlsym(h, "pthread_join")
	if err != nil {
		t.Fatal(err)
	}
	b := n.NewBuilder()
	v := b.ParamNative()
	b.Return(b.CallGo(b.ConstNative(reflect.ValueOf(loongGrowTarget).Pointer()), v))
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	code, err := n.Compile(p, compile.TargetSpec{}, compile.Func{ABI: abi.Go, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 512}, AssumePointerFree: true})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	callback, err := execution.RegisterForeignCallback(func(v uintptr) uintptr {
		got, e := code.Call(execution.Words(v)...)
		if e != nil {
			panic(e)
		}
		return got
	})
	if err != nil {
		t.Fatal(err)
	}
	mem, err := syscall.Mmap(-1, 0, 4096, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatal(err)
	}
	// A timed-out foreign thread may still retain these addresses; only release
	// after pthread_join has proved that the C thread is gone.
	address := uintptr(unsafe.Pointer(&mem[0]))
	if e := execution.CallForeign(create, []uintptr{address, 0, callback, 35}); e != 0 {
		syscall.Munmap(mem)
		t.Fatalf("pthread_create=%d", e)
	}
	thread := uintptr(binary.LittleEndian.Uint64(mem))
	done := make(chan uintptr, 1)
	go func() { done <- execution.CallForeign(join, []uintptr{thread, address + 8}) }()
	select {
	case result := <-done:
		defer syscall.Munmap(mem)
		if result != 0 {
			t.Fatalf("pthread_join=%d", result)
		}
		if got := binary.LittleEndian.Uint64(mem[8:]); got != 42 {
			t.Fatalf("foreign-thread -> callback -> JIT -> Go got%d want42", got)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("foreign-thread reentry timed out")
	}
}
