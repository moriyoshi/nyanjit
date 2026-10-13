//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64 || ppc64le)

package compile_test

import (
	"fmt"
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/execution"
	"math/bits"
	"runtime"
	"testing"
)

func TestCountZerosWidthsAndZero(t *testing.T) {
	for _, w := range []uint8{8, 16, 32, 64} {
		for _, leading := range []bool{true, false} {
			for _, baseline := range []bool{true, false} {
				t.Run(fmt.Sprintf("width%d/leading%v/baseline%v", w, leading, baseline), func(t *testing.T) {
					b := nyanjit.NewBuilder()
					x := b.ParamBits(w)
					var v nyanjit.Value
					if leading {
						v = b.LeadingZeros(x)
					} else {
						v = b.TrailingZeros(x)
					}
					b.Return(v)
					p := finishCrossAssembly(t, b)
					options := abi.CompileOptions{Stack: abi.StackPlatform, StackBytesAvailable: 65536}
					if baseline {
						options.CPU, _ = (abi.CPUProfile{}).Resolve(runtime.GOARCH)
					}
					code, err := compile.Compile(p, options)
					if err != nil {
						t.Fatal(err)
					}
					defer code.Close()
					for _, x := range []uint64{0, 1, 2, 3, 1 << 7, 1 << 15, 1 << 31, 1 << 63, ^uint64(0), 0x0012304000200010} {
						input := x & (^uint64(0) >> (64 - w))
						n := bits.TrailingZeros64(input)
						if n > int(w) {
							n = int(w)
						}
						if leading {
							n = bits.LeadingZeros64(input) - (64 - int(w))
						}
						args := []uintptr{uintptr(x)}
						if w == 64 {
							args = execution.Words("L", x)
						}
						got, err := code.Call(args...)
						if err != nil || got != uintptr(n) {
							t.Fatalf("input%#x: %d,%v want%d", x, got, err, n)
						}
					}
				})
			}
		}
	}
}
