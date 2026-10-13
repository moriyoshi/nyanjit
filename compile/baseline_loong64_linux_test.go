//go:build linux && loong64

package compile_test

import (
	"math"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

// TestLoong64BaselineBinary32Bits checks that binary32 results leave the
// floating-point bank zero-extended: LA64 leaves the upper half of a
// single-precision result undefined, and QEMU sets it to ones.
func TestLoong64BaselineBinary32Bits(t *testing.T) {
	x := uint64(math.Float32bits(2.25))
	y := uint64(math.Float32bits(-0.75))
	args := [][]uint64{{x, y}, {y, x}, {uint64(math.Float32bits(float32(math.Inf(-1)))), x}}
	kernels := map[string]func(*nyanjit.Builder){
		"fadd": func(b *nyanjit.Builder) { b.Return(b.FAdd(b.ParamBits(32), b.ParamBits(32))) },
		"fdiv": func(b *nyanjit.Builder) { b.Return(b.FDiv(b.ParamBits(32), b.ParamBits(32))) },
		"fma": func(b *nyanjit.Builder) {
			p, q := b.ParamBits(32), b.ParamBits(32)
			b.Return(b.FMA(p, q, p))
		},
		"sqrt": func(b *nyanjit.Builder) {
			p, q := b.ParamBits(32), b.ParamBits(32)
			b.Return(b.FSqrt(b.FMul(p, q)))
		},
		"convert": func(b *nyanjit.Builder) {
			p := b.FConvert(b.ParamBits(32), 64)
			b.ParamBits(32)
			b.Return(b.FConvert(b.FAdd(p, p), 32))
		},
		"from-signed": func(b *nyanjit.Builder) {
			p := b.Param()
			b.Param()
			b.Return(b.FFromSigned(p, 32))
		},
	}
	for name, build := range kernels {
		for _, f := range []compile.Func{
			{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 14}},
			{ABI: abi.C},
		} {
			runBoth(t, name, build, f, args)
		}
	}
}
