//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || loong64)

package execution

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

// Hand-assembled kernels for the float-passing boundary. Each copies one
// argument register to the result register, so a test sees exactly where the
// trampoline put a word and in which representation.
type floatKernels struct {
	gpResult func(k int) []byte // integer result = integer argument register k
	fpBits   func(k int) []byte // integer result = raw 64 bits of FP argument register k
	fpResult func(k int) []byte // FP result = FP argument register k
}

func words32(ws ...uint32) []byte {
	out := make([]byte, 0, 4*len(ws))
	for _, w := range ws {
		out = binary.LittleEndian.AppendUint32(out, w)
	}
	return out
}

// amd64GP is the integer argument register order of each convention.
var amd64GP = map[abi.Flavor][]byte{abi.C: {7, 6, 2, 1, 8, 9}, abi.Go: {0, 3, 1, 7, 6, 8}}

func kernelsFor(flavor abi.Flavor) floatKernels {
	switch runtime.GOARCH {
	case "arm64":
		ret := uint32(0xd65f03c0)
		return floatKernels{
			gpResult: func(k int) []byte { return words32(0xaa0003e0|uint32(k)<<16, ret) },
			fpBits:   func(k int) []byte { return words32(0x9e660000|uint32(k)<<5, ret) },
			fpResult: func(k int) []byte { return words32(0x1e604000|uint32(k)<<5, ret) },
		}
	case "amd64":
		return floatKernels{
			gpResult: func(k int) []byte {
				r := amd64GP[flavor][k]
				return []byte{0x48 | r>>3, 0x89, 0xc0 | (r&7)<<3, 0xc3}
			},
			fpBits:   func(k int) []byte { return []byte{0x66, 0x48, 0x0f, 0x7e, 0xc0 | byte(k)<<3, 0xc3} },
			fpResult: func(k int) []byte { return []byte{0x0f, 0x28, 0xc0 | byte(k), 0xc3} },
		}
	case "riscv64":
		ret := uint32(0x00008067)
		return floatKernels{
			gpResult: func(k int) []byte { return words32(uint32(10+k)<<15|10<<7|0x13, ret) },
			fpBits:   func(k int) []byte { return words32(0xe2000053|uint32(10+k)<<15|10<<7, ret) },
			fpResult: func(k int) []byte { return words32(0x22000053|uint32(10+k)<<20|uint32(10+k)<<15|10<<7, ret) },
		}
	case "ppc64le":
		ret := uint32(0x4e800020)
		return floatKernels{
			gpResult: func(k int) []byte { s := uint32(3 + k); return words32(31<<26|s<<21|3<<16|s<<11|444<<1, ret) },
			fpBits:   func(k int) []byte { return words32(31<<26|uint32(1+k)<<21|3<<16|51<<1, ret) },
			fpResult: func(k int) []byte { return words32(63<<26|1<<21|uint32(1+k)<<11|72<<1, ret) },
		}
	case "loong64":
		ret := uint32(0x4c000020)
		return floatKernels{
			gpResult: func(k int) []byte { return words32(0x00150000|uint32(4+k)<<5|4, ret) },
			fpBits:   func(k int) []byte { return words32(0x0114b800|uint32(k)<<5|4, ret) },
			fpResult: func(k int) []byte { return words32(0x01149800|uint32(k)<<5, ret) },
		}
	}
	panic("no kernels for " + runtime.GOARCH)
}

// fpRepresentation is binary32 x as this target holds it in an FP register.
func fpRepresentation(x uint32) uint64 {
	switch runtime.GOARCH {
	case "riscv64":
		return uint64(x) | 0xffffffff<<32
	case "ppc64le":
		return widenSingle(x)
	}
	return uint64(x)
}

var (
	singles = []uint32{0x3fc00000, 0x80000000, 0x00000001, 0x007fffff, 0x00400000, 0x7f800001, 0x7fc12345, 0xff800000, 0x7f7fffff, 0x00800000}
	doubles = []uint64{0x3ff8000000000000, 0x8000000000000000, 0x0000000000000001, 0x000fffffffffffff, 0x7ff0000000000001, 0x7ff8dead0000beef, 0xfff0000000000000}
)

// mixed is f(a int64, x float32, b int64, y float64).
const mixed = abi.FloatPassing(1<<1 | 1<<3)

// mixedGP returns the integer register index of b, the third argument.
func mixedGP(flavor abi.Flavor) int {
	if runtime.GOARCH == "ppc64le" && flavor == abi.C {
		return 2 // ELFv2: a float argument skips its positional GPR
	}
	return 1
}

type floatEntry struct {
	foreign, managed bool
}

func newFloatCode(t *testing.T, code []byte, flavor abi.Flavor, floats abi.FloatPassing, bits []uint8, entry floatEntry) *Code {
	t.Helper()
	options := CodeOptions{ABI: flavor, Arity: len(bits), ParamBits: bits, Floats: floats, Foreign: entry.foreign, GoManaged: entry.managed}
	var register FrameRegistrar
	if entry.managed {
		register = func([]byte) (bool, error) { return true, nil }
	}
	c, err := NewCode(code, options, register)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func floatEntries(flavor abi.Flavor) []floatEntry {
	entries := []floatEntry{{}, {managed: true}}
	if flavor == abi.C {
		entries = append(entries, floatEntry{foreign: true})
	}
	return entries
}

func TestFloatPassingRoutes(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		k := kernelsFor(flavor)
		bits := []uint8{64, 32, 64, 64}
		for _, entry := range floatEntries(flavor) {
			name := fmt.Sprintf("flavor%d/foreign=%v/managed=%v", flavor, entry.foreign, entry.managed)
			t.Run(name, func(t *testing.T) {
				a, b := uintptr(0x1111222233334444), uintptr(0x5555666677778888)
				check := func(code []byte, floats abi.FloatPassing, x uint32, y uint64, want uint64, pair bool) {
					t.Helper()
					c := newFloatCode(t, code, flavor, floats, bits, entry)
					defer c.Close()
					args := []uintptr{a, uintptr(x), b, uintptr(y)}
					var got, hi uintptr
					var err error
					if pair {
						got, hi, err = c.CallPair(args...)
					} else {
						got, err = c.Call(args...)
					}
					if err != nil || uint64(got) != want || hi != 0 {
						t.Fatalf("x=%#x y=%#x: got %#x:%#x, %v; want %#x", x, y, hi, got, err, want)
					}
				}
				for i, x := range singles {
					y := doubles[i%len(doubles)]
					check(k.gpResult(0), mixed, x, y, uint64(a), false)
					check(k.gpResult(mixedGP(flavor)), mixed, x, y, uint64(b), true)
					check(k.fpBits(0), mixed, x, y, fpRepresentation(x), false)
					check(k.fpBits(1), mixed, x, y, y, false)
					check(k.fpResult(0), mixed|abi.FloatResult32, x, y, uint64(x), false)
					check(k.fpResult(1), mixed|abi.FloatResult64, x, y, y, true)
				}
			})
		}
	}
}

func TestFloatPassingFunc(t *testing.T) {
	k := kernelsFor(abi.Go)
	c := newFloatCode(t, k.fpResult(0), abi.Go, abi.FloatArgument(0)|abi.FloatResult64, []uint8{64}, floatEntry{})
	var f func(float64) float64
	if err := c.Func(&f); err != nil {
		t.Fatal(err)
	}
	for _, x := range []float64{2.5, math.Copysign(0, -1), math.SmallestNonzeroFloat64, math.Inf(-1)} {
		if got := f(x); math.Float64bits(got) != math.Float64bits(x) {
			t.Fatalf("f(%v) = %v", x, got)
		}
	}
	c = newFloatCode(t, k.fpResult(0), abi.Go, abi.FloatArgument(1)|abi.FloatResult32, []uint8{64, 32}, floatEntry{})
	var g func(int64, float32) float32
	if err := c.Func(&g); err != nil {
		t.Fatal(err)
	}
	for _, x := range []uint32{0x3fc00000, 0x80000000, 0x00000001, 0x7fc12345} {
		if got := g(7, math.Float32frombits(x)); math.Float32bits(got) != x {
			t.Fatalf("g(%#x) = %#x", x, math.Float32bits(got))
		}
	}
	var h func(int64, float32) float32
	if err := c.Func(&h); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []any{
		new(func(int64, float64) float32), // width differs from the compiled one
		new(func(int64, uint32) float32),  // a marked parameter must be a float
		new(func(float64, float32) float32),
		new(func(int64, float32) uint32), // a marked result must be a float
		new(func(int64, float32) float64),
	} {
		if err := c.Func(bad); !errors.Is(err, ErrSignature) {
			t.Fatalf("%T: %v", bad, err)
		}
	}
	plain := newFloatCode(t, k.fpResult(0), abi.Go, 0, []uint8{64}, floatEntry{})
	if err := plain.Func(new(func(float64) float64)); !errors.Is(err, ErrSignature) {
		t.Fatalf("unmarked float signature: %v", err)
	}
}

func TestFloatPassingOptions(t *testing.T) {
	code := kernelsFor(abi.C).fpResult(0)
	for _, options := range []CodeOptions{
		{ABI: abi.C, Arity: 1, Floats: abi.FloatArgument(1)},
		{ABI: abi.C, Arity: 1, Floats: abi.FloatResult32 | abi.FloatResult64},
		{ABI: abi.C, Arity: 1, ParamBits: []uint8{16}, Floats: abi.FloatArgument(0)},
		{ABI: abi.C, Arity: 1, ResultWords: 2, Floats: abi.FloatResult64},
		{ABI: abi.C, Arity: 8, Floats: 0x1ff},
	} {
		if _, err := NewCode(code, options, nil); !errors.Is(err, ErrInvalidCodeOptions) {
			t.Fatalf("%+v: %v", options, err)
		}
	}
}

func TestFloatPassingRepresentation(t *testing.T) {
	for _, x := range singles {
		if got := narrowDouble(widenSingle(x)); got != x {
			t.Fatalf("%#x round-trips to %#x", x, got)
		}
		if f := math.Float32frombits(x); f == f && math.Float64bits(float64(f)) != widenSingle(x) {
			t.Fatalf("%#x widens to %#x", x, widenSingle(x))
		}
	}
	for x := uint32(1); x < 1<<23; x = x*3 + 1 {
		if got := narrowDouble(widenSingle(x)); got != x || math.Float64bits(float64(math.Float32frombits(x))) != widenSingle(x) {
			t.Fatalf("subnormal %#x", x)
		}
	}
}
