//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"fmt"
	"math/bits"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
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
					var spec compile.TargetSpec
					if baseline {
						spec.CPU, _ = (abi.CPUProfile{}).Resolve(runtime.GOARCH)
					}
					code, err := nyanjit.Compile(p, spec, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
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
							args = execution.Words(x)
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
