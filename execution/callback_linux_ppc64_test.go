//go:build linux && ppc64

package execution_test

import (
	"errors"
	"github.com/moriyoshi/nyanjit/execution"
	"runtime"
	"testing"
	"time"
)

func TestPPC64CallbackSimple(t *testing.T) {
	callback, err := execution.RegisterForeignCallback(func(value uint64) uint64 { runtime.GC(); return value + 7 })
	if err != nil {
		t.Fatal(err)
	}
	before := runtime.NumCgoCall()
	if got := execution.CallForeign(callback, []uintptr{35}); got != 42 {
		t.Fatalf("callback=%d; want 42", got)
	}
	if runtime.NumCgoCall() <= before {
		t.Fatal("runtime cgo accounting was not incremented")
	}
}

func TestPPC64CallbackIntegerWidthsAndArity(t *testing.T) {
	var calls int
	callback, err := execution.RegisterForeignCallback(func(a int8, b uint8, c int16, d uint16, e int32, f uint32, g int64, h uintptr) uint64 {
		calls++
		if a != -1 || b != 0xfe || c != -123 || d != 65534 || e != -123456 || f != 0x87654321 || g != -987654321 || h != 42 {
			panic("incorrect callback argument decoding")
		}
		return 42
	})
	if err != nil {
		t.Fatal(err)
	}
	args := []uintptr{0xff, 0xfe, 0xff85, 65534, 0xfffe1dc0, 0x87654321, uintptr(0xffffffffc521974f), 42}
	if got := execution.CallForeign(callback, args); got != 42 || calls != 1 {
		t.Fatalf("callback=%d calls=%d", got, calls)
	}
	signed, err := execution.RegisterForeignCallback(func() int8 { return -7 })
	if err != nil {
		t.Fatal(err)
	}
	if got := execution.CallForeign(signed, nil); int64(got) != -7 {
		t.Fatalf("signed callback=%#x", got)
	}
	boolean, err := execution.RegisterForeignCallback(func(x bool) bool { return !x })
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ x, want uintptr }{{0, 1}, {1, 0}, {0x100, 1}} {
		if got := execution.CallForeign(boolean, []uintptr{test.x}); got != test.want {
			t.Fatalf("boolean(%d)=%d", test.x, got)
		}
	}
	empty, err := execution.RegisterForeignCallback(func() { calls++ })
	if err != nil {
		t.Fatal(err)
	}
	if got := execution.CallForeign(empty, nil); got != 0 || calls != 2 {
		t.Fatalf("void callback=%d calls=%d", got, calls)
	}
}

func TestPPC64CallbackRejectsUnsupportedSignatures(t *testing.T) {
	var nilFunction func(uint64) uint64
	for _, fn := range []any{nil, 42, nilFunction, func(...uint64) {}, func(float64) {}, func() float64 { return 1 }, func(string) {}, func(uint64) (uint64, uint64) { return 1, 2 }, func(a, b, c, d, e, f, g, h, i uint64) {}} {
		if _, err := execution.RegisterForeignCallback(fn); !errors.Is(err, execution.ErrInvalidCallback) {
			t.Fatalf("callback %T=%v", fn, err)
		}
	}
}

func TestPPC64CallbackNestedCallAndStackGrowth(t *testing.T) {
	inner, err := execution.RegisterForeignCallback(func(value uint64) uint64 { return callbackGrow(value, 64) })
	if err != nil {
		t.Fatal(err)
	}
	outer, err := execution.RegisterForeignCallback(func(value uint64) uint64 {
		return uint64(execution.CallForeign(inner, []uintptr{uintptr(value + 1)})) + 1
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := execution.CallForeign(outer, []uintptr{33}); got != 42 {
		t.Fatalf("nested callback=%d", got)
	}
}

//go:noinline
func callbackGrow(value, depth uint64) uint64 {
	var frame [1024]byte
	frame[0] = byte(value)
	if depth != 0 {
		result := callbackGrow(value, depth-1)
		runtime.KeepAlive(frame)
		return result
	}
	runtime.GC()
	runtime.KeepAlive(frame)
	return value + 7
}

func TestPPC64CallbackPanicUnwindsRuntimeState(t *testing.T) {
	callback, err := execution.RegisterForeignCallback(func() { panic("callback panic") })
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if got := recover(); got != "callback panic" {
				t.Errorf("panic=%v", got)
			}
		}()
		execution.CallForeign(callback, nil)
		t.Fatal("callback returned without panic")
	}()
	next, err := execution.RegisterForeignCallback(func() uint64 { runtime.GC(); return 42 })
	if err != nil {
		t.Fatal(err)
	}
	if got := execution.CallForeign(next, nil); got != 42 {
		t.Fatalf("callback after panic=%d", got)
	}
}

func TestPPC64CallbackConcurrentCalls(t *testing.T) {
	callback, err := execution.RegisterForeignCallback(func(x uint64) uint64 { runtime.Gosched(); return x + 7 })
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan bool, 16)
	for i := uint64(0); i < 16; i++ {
		go func(value uint64) {
			valid := true
			for j := 0; j < 10; j++ {
				if got := execution.CallForeign(callback, []uintptr{uintptr(value)}); uint64(got) != value+7 {
					valid = false
				}
			}
			results <- valid
		}(i)
	}
	for i := 0; i < 16; i++ {
		select {
		case valid := <-results:
			if !valid {
				t.Fatal("incorrect concurrent callback result")
			}
		case <-time.After(30 * time.Second):
			t.Fatal("callback concurrency timed out")
		}
	}
}
