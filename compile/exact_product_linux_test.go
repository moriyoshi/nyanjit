//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le)

package compile_test

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/cpuprofile"
	"math"
	"math/big"
	"testing"
)

func TestExactProductErrorMatchesExactRemainder(t *testing.T) {
	o := abi.CompileOptions{CPU: cpuprofile.Host()}
	code, err := compile.Compile(productErrorProgram(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, ab := range [][2]float64{{1.1, 1.3}, {-1.1, 1.3}, {math.Nextafter(1, 2), math.Nextafter(1, 2)}, {0x1.fffffffffffffp100, 0x1.fffffffffffffp-100}, {0x1.abcdef0123456p-200, 0x1.23456789abcdep200}, {0, 1}, {1, 1}} {
		a, b := ab[0], ab[1]
		exact := new(big.Float).SetPrec(256).Mul(new(big.Float).SetPrec(256).SetFloat64(a), new(big.Float).SetPrec(256).SetFloat64(b))
		exact.Sub(exact, new(big.Float).SetPrec(256).SetFloat64(a*b))
		want, _ := exact.Float64()
		got, err := code.Call(uintptr(math.Float64bits(a)), uintptr(math.Float64bits(b)))
		if err != nil || uint64(got) != math.Float64bits(want) {
			t.Fatalf("(%g,%g) error=%#x,%v want%#x", a, b, got, err, math.Float64bits(want))
		}
	}
}
