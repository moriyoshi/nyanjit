//go:build linux && 386

package execution

import (
	"errors"
	"math"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

type floatEntry struct{ foreign, managed bool }

// quiet64 and quiet32 give the value x87 FLD loads: a signaling NaN becomes quiet.
func quiet64(x uint64) uint64 {
	if x&0x7ff0000000000000 == 0x7ff0000000000000 && x&0xfffffffffffff != 0 {
		return x | 1<<51
	}
	return x
}

func quiet32(x uint32) uint32 {
	if x&0x7f800000 == 0x7f800000 && x&0x7fffff != 0 {
		return x | 1<<22
	}
	return x
}

// f(a int32, x float32, y float64) in cdecl stack words.
func TestFloatPassing386(t *testing.T) {
	floats := abi.FloatArgument(1) | abi.FloatArgument(2)
	kernels := []struct {
		code   []byte
		result abi.FloatPassing
	}{
		{[]byte{0xdd, 0x44, 0x24, 0x0c, 0xc3}, abi.FloatResult64}, // fld qword [esp+12]
		{[]byte{0xd9, 0x44, 0x24, 0x08, 0xc3}, abi.FloatResult32}, // fld dword [esp+8]
		{[]byte{0x8b, 0x44, 0x24, 0x08, 0xc3}, 0},                 // mov eax, [esp+8]
	}
	singles := []uint32{0x3fc00000, 0x80000000, 0x00000001, 0x7f800001, 0x7fc12345}
	doubles := []uint64{0x3ff8000000000000, 0x8000000000000000, 0x0000000000000001, 0x7ff0000000000001, 0x7ff8dead0000beef}
	for _, entry := range []floatEntry{{}, {managed: true}, {foreign: true}} {
		for _, k := range kernels {
			options := CodeOptions{ABI: abi.C, Arity: 3, ArgumentWords: 4, ParamBits: []uint8{32, 32, 64}, Floats: floats | k.result, Foreign: entry.foreign, GoManaged: entry.managed}
			var register FrameRegistrar
			if entry.managed {
				register = func([]byte) (bool, error) { return true, nil }
			}
			c, err := NewCode(k.code, options, register)
			if err != nil {
				t.Fatal(err)
			}
			for i, x := range singles {
				y := doubles[i]
				lo, hi, err := c.CallPair(7, uintptr(x), uintptr(uint32(y)), uintptr(y>>32))
				got := uint64(lo) | uint64(hi)<<32
				want := uint64(x)
				switch k.result {
				case abi.FloatResult64:
					want = quiet64(y)
				case abi.FloatResult32:
					want = uint64(quiet32(x))
				}
				if err != nil || got != want && k.result != 0 || k.result == 0 && uint32(lo) != x {
					t.Fatalf("%+v %v: got %#x, %v; want %#x", entry, k.result, got, err, want)
				}
				if lo, err := c.Call(7, uintptr(x), uintptr(uint32(y)), uintptr(y>>32)); err != nil || uint32(lo) != uint32(want) {
					t.Fatalf("Call: %#x, %v", lo, err)
				}
			}
			c.Close()
		}
	}
}

func TestFloatPassing386Func(t *testing.T) {
	// Go ABI0: copy the float64 argument at 4(SP) to the result slot at 12(SP).
	code := []byte{0x8b, 0x44, 0x24, 0x04, 0x89, 0x44, 0x24, 0x0c, 0x8b, 0x44, 0x24, 0x08, 0x89, 0x44, 0x24, 0x10, 0xc3}
	options := CodeOptions{ABI: abi.Go, Arity: 1, ArgumentWords: 2, ParamBits: []uint8{64}, Floats: abi.FloatArgument(0) | abi.FloatResult64}
	c, err := NewCode(code, options, nil)
	if err != nil {
		t.Fatal(err)
	}
	var f func(float64) float64
	if err := c.Func(&f); err != nil {
		t.Fatal(err)
	}
	if got := f(-2.5); got != -2.5 {
		t.Fatalf("f(-2.5) = %v", got)
	}
	if lo, hi, err := c.CallPair(uintptr(uint32(math.Float64bits(3))), uintptr(math.Float64bits(3)>>32)); err != nil || Wide(lo, hi) != math.Float64bits(3) {
		t.Fatalf("CallPair: %#x %#x %v", lo, hi, err)
	}
	options.Floats = abi.FloatArgument(0) | abi.FloatResult32
	c, err = NewCode(code, options, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Func(new(func(float64) float32)); !errors.Is(err, ErrSignature) {
		t.Fatalf("binary32 Go result on 386: %v", err)
	}
	if err := c.Func(new(func(float64) float64)); !errors.Is(err, ErrSignature) {
		t.Fatalf("binary32 Go result as float64 on 386: %v", err)
	}
}
