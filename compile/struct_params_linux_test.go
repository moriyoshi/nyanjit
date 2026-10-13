//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"testing"
	"unsafe"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/compile"
)

type structParamRecord struct {
	Prefix uint32
	Wide   int64 // offset 4 on 386, 8 on the supported 64-bit hosts
	Inner  struct {
		Enabled bool
		Signed  int16
		F32     float32
		F64     float64
	}
	Arr  [4]uint16
	Rows [2]struct{ Cells [3]uint32 }
}

func structParamOptions(flavor abi.Flavor) compile.Func {
	return compile.Func{ABI: flavor, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}}
}

func TestValueParamsReadAndWriteHostFields(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		t.Run(fmt.Sprint(flavor), func(t *testing.T) {
			b := n.NewBuilder()
			input := n.ValueParam[structParamRecord](b)
			output := n.PointerParam[structParamRecord](b)
			inner := input.At("Inner")
			prefix := input.Field("Prefix")
			wide := input.Field("Wide")
			signed := b.SignExtend(inner.Field("Signed"), 16)
			sum := b.Add(wide, b.Add(b.CastUnsigned(prefix, 64), signed))
			output.At("Prefix").Store(prefix)
			output.At("Wide").Store(sum)
			outInner := output.At("Inner")
			outInner.At("Enabled").Store(inner.Field("Enabled"))
			outInner.At("Signed").Store(inner.Field("Signed"))
			outInner.At("F32").Store(b.FAdd(inner.Field("F32"), b.Float32(1.5)))
			outInner.At("F64").Store(b.FMul(inner.Field("F64"), b.Float64(2.25)))
			output.At("Arr[1]").Store(input.Field("Arr[2]"))
			output.At("Rows[0].Cells[1]").Store(input.Field("Rows[1].Cells[2]"))
			b.Store16(output.At("Arr[3]").Addr(), b.ConstBits(16, 0xbeef))
			b.Return(output.Field("Wide")) // a fresh read observes the Store
			program, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			code, err := n.Compile(program, compile.TargetSpec{}, structParamOptions(flavor))
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			in := structParamRecord{Prefix: 0x80000001, Wide: -0x123456789}
			in.Inner.Enabled, in.Inner.Signed = true, -17
			in.Inner.F32, in.Inner.F64 = 1.25, -3.5
			in.Arr[2], in.Rows[1].Cells[2] = 0xfedc, 0xfedcba98
			before := in
			out := structParamRecord{Prefix: 99}
			lo, hi, err := code.CallPair(execution.Words(in, &out)...)
			want := uint64(in.Wide + int64(in.Prefix) + int64(in.Inner.Signed))
			if err != nil || execution.Wide(lo, hi) != want || uint64(out.Wide) != want ||
				out.Prefix != in.Prefix || out.Inner.Enabled != in.Inner.Enabled || out.Inner.Signed != in.Inner.Signed ||
				out.Inner.F32 != in.Inner.F32+1.5 || out.Inner.F64 != in.Inner.F64*2.25 ||
				out.Arr != [4]uint16{0, in.Arr[2], 0, 0xbeef} || out.Rows[0].Cells[1] != in.Rows[1].Cells[2] || in != before {
				t.Fatalf("got %#x, output %+v, input %+v, err %v; want %#x", execution.Wide(lo, hi), out, in, err, want)
			}
			wantOffset := uintptr(8)
			if runtime.GOARCH == "386" {
				wantOffset = 4
			}
			if unsafe.Offsetof(in.Wide) != wantOffset {
				t.Fatalf("Wide host offset = %d, want %d", unsafe.Offsetof(in.Wide), wantOffset)
			}
		})
	}
}

func TestValueParamsDynamicIndexBounds(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		for _, pointer := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/pointer=%v", flavor, pointer), func(t *testing.T) {
				b := n.NewBuilder()
				var s n.Data
				if pointer {
					s = n.PointerParam[structParamRecord](b)
				} else {
					s = n.ValueParam[structParamRecord](b)
				}
				index := b.Param() // exercise full 64-bit bounds even on 386
				invalid := b.NewLabel()
				address := s.At("Rows[1].Cells").Index(index, invalid)
				if pointer {
					address.Store(b.ConstBits(32, 0xfedcba98))
				}
				b.Return(b.CastUnsigned(address.Load(), 64))
				b.Bind(invalid)
				b.Return(b.Const(0xdeadbeef))
				program, err := b.Finish()
				if err != nil {
					t.Fatal(err)
				}
				code, err := n.Compile(program, compile.TargetSpec{}, structParamOptions(flavor))
				if err != nil {
					t.Fatal(err)
				}
				defer code.Close()
				for _, index := range []uint64{0, 1, 2, 3, 1 << 32, math.MaxUint64} {
					record := structParamRecord{}
					record.Rows[1].Cells = [3]uint32{11, 22, 33}
					var arg any = record
					if pointer {
						arg = &record
					}
					want := uint64(0xdeadbeef)
					if index < 3 {
						want = uint64(record.Rows[1].Cells[index])
						if pointer {
							want = 0xfedcba98
						}
					}
					lo, hi, err := code.CallPair(execution.Words(arg, index)...)
					if err != nil || execution.Wide(lo, hi) != want {
						t.Fatalf("index %#x: got %#x, err %v; want %#x", index, execution.Wide(lo, hi), err, want)
					}
					if pointer && index < 3 && record.Rows[1].Cells[index] != 0xfedcba98 {
						t.Fatal("indexed pointer store did not write back")
					}
				}
			})
		}
	}
}

func TestValueParamsTypedFunc(t *testing.T) {
	b := n.NewBuilder()
	s := n.PointerParam[structParamRecord](b)
	s.At("Wide").Store(b.Add(s.Field("Wide"), b.Const(1)))
	b.Return(s.Field("Wide"))
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := n.Compile(program, compile.TargetSpec{}, structParamOptions(abi.Go))
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	var f func(*structParamRecord) uint64
	err = code.Func(&f)
	if err != nil {
		t.Fatal(err)
	}
	in := &structParamRecord{Wide: 41}
	if got := f(in); got != 42 || in.Wide != 42 {
		t.Fatalf("Func returned %d, write-back %d", got, in.Wide)
	}
	runtime.KeepAlive(in)
	if err := code.Func(new(func(structParamRecord) uint64)); !errors.Is(err, execution.ErrSignature) {
		t.Fatalf("by-value Go ABI struct: %v", err)
	}
}

//go:noinline
func structParamGC(depth uint64) uint64 {
	var frame [512]byte
	frame[depth%512] = byte(depth)
	if depth != 0 {
		result := structParamGC(depth - 1)
		runtime.KeepAlive(&frame)
		return result
	}
	runtime.GC()
	return 7
}

func TestValueParamsManagedCallback(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	b := n.NewBuilder()
	s := n.PointerParam[structParamRecord](b)
	target := b.ConstNative(reflect.ValueOf(structParamGC).Pointer())
	result := b.CallGo(target, b.Const(64))
	// The base pointer must survive stack growth and GC before this read.
	b.Return(b.Add(s.Field("Wide"), result))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	opts := structParamOptions(abi.Go)
	opts.Context = compile.ContextGoManaged
	opts.AssumePointerFree = true
	code, err := n.Compile(p, compile.TargetSpec{}, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	var f func(*structParamRecord) uint64
	err = code.Func(&f)
	if err != nil {
		t.Fatal(err)
	}
	input := &structParamRecord{Wide: 35}
	var pin runtime.Pinner
	pin.Pin(input)
	defer pin.Unpin()
	resultCh := make(chan uint64, 1)
	go func() { resultCh <- f(input) }()
	if got := <-resultCh; got != 42 {
		t.Fatalf("managed pointer read after GC = %d", got)
	}
	runtime.KeepAlive(input)
	lo, hi, err := code.CallPair(execution.Words(input)...)
	if err != nil || execution.Wide(lo, hi) != 42 {
		t.Fatalf("managed Words call = %d, err %v", execution.Wide(lo, hi), err)
	}
}
