package compile_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestCrossAssemblyFloatUnary(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "riscv64", "loong64", "ppc64le", "386"} {
		for _, width := range []uint8{32, 64} {
			b := nyanjit.NewBuilder()
			b.Return(b.FSqrt(b.ParamBits(width)))
			text, err := lowerListing(finishCrossAssembly(t, b), compile.TargetSpec{Architecture: arch}, compile.Func{ABI: abi.C})
			if err != nil || !strings.Contains(text, "sqrt") {
				t.Fatalf("%s sqrt%d: %v\n%s", arch, width, err, text)
			}
			b = nyanjit.NewBuilder()
			b.Return(b.FConvert(b.ParamBits(width), 96-width))
			text, err = lowerListing(finishCrossAssembly(t, b), compile.TargetSpec{Architecture: arch}, compile.Func{ABI: abi.C})
			if err != nil {
				t.Fatalf("%s convert%d: %v\n%s", arch, width, err, text)
			}
		}
	}

}

// Inline math must stay a leaf: no calls, and no stack frame beyond what the
// default options allow.
func TestCrossAssemblyInlineMath(t *testing.T) {
	unary := map[string]func(*nyanjit.Builder, nyanjit.Value) nyanjit.Value{
		"Cos": (*nyanjit.Builder).FCos, "Tan": (*nyanjit.Builder).FTan, "Asin": (*nyanjit.Builder).FAsin,
		"Exp": (*nyanjit.Builder).FExp, "Log": (*nyanjit.Builder).FLog, "Sinh": (*nyanjit.Builder).FSinh,
		"Tanh": (*nyanjit.Builder).FTanh, "Cbrt": (*nyanjit.Builder).FCbrt, "Floor": (*nyanjit.Builder).FFloor,
	}
	binary := map[string]func(*nyanjit.Builder, nyanjit.Value, nyanjit.Value) nyanjit.Value{
		"Atan2": (*nyanjit.Builder).FAtan2, "Pow": (*nyanjit.Builder).FPow,
	}
	for _, arch := range []string{"amd64", "arm64", "riscv64", "loong64", "ppc64le", "386"} {
		for _, width := range []uint8{32, 64} {
			build := map[string]func() *nyanjit.Builder{}
			for name, f := range unary {
				build[name] = func() *nyanjit.Builder {
					b := nyanjit.NewBuilder()
					b.Return(f(b, b.ParamBits(width)))
					return b
				}
			}
			for name, f := range binary {
				build[name] = func() *nyanjit.Builder {
					b := nyanjit.NewBuilder()
					b.Return(f(b, b.ParamBits(width), b.ParamBits(width)))
					return b
				}
			}
			for name, b := range build {
				t.Run(name+strconv.Itoa(int(width))+"/"+arch, func(t *testing.T) {
					text, err := lowerListing(finishCrossAssembly(t, b()), compile.TargetSpec{Architecture: arch}, compile.Func{ABI: abi.C})
					if err != nil {
						t.Fatal(err)
					}
					for _, call := range []string{"call", "bl ", "blr", "jalr", "bctrl"} {
						if strings.Contains(strings.ToLower(text), "\t"+call) || strings.Contains(strings.ToLower(text), " "+call+" ") {
							t.Fatalf("inline math emitted %q:\n%s", call, text)
						}
					}
				})
			}
		}
	}
}
