package compile_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func multiplyAddProgram(t *testing.T, width uint8, reverse, shared, scale bool) *ir.Program {
	t.Helper()
	b := nyanjit.NewBuilder()
	x, y, addend := b.ParamBits(width), b.ParamBits(width), b.ParamBits(width)
	var result nyanjit.Value
	if scale {
		result = x.MulConst(7).Add(addend)
	} else {
		product := b.Mul(x, y)
		if reverse {
			result = b.Add(addend, product)
		} else {
			result = b.Add(product, addend)
		}
		if shared {
			result = b.Xor(result, product)
		}
	}
	b.Return(result)
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return program
}

func TestPPC64CPUAssemblyFusion(t *testing.T) {
	for _, arch := range []string{"ppc64", "ppc64le"} {
		for _, level := range []abi.PPC64CPU{0, 8, 9, 10} {
			for _, width := range []uint8{8, 16, 32, 64} {
				if width == 64 && nyanjit.NativeBits == 32 {
					// Builder values wider than its host word are paired;
					// those IR shapes have separate wide-arithmetic coverage.
					continue
				}
				for _, reverse := range []bool{false, true} {
					program := multiplyAddProgram(t, width, reverse, false, false)
					listing, err := compile.EmitAssembly(program, abi.CompileOptions{Architecture: arch, PPC64CPU: level})
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(listing, "maddld") != (level >= abi.PPC64Power9) {
						t.Fatalf("%s/%s/%d: wrong multiply-add:\n%s", arch, level, width, listing)
					}
					if level >= abi.PPC64Power9 && strings.Contains(listing, "mulld ") {
						t.Fatalf("fused expression also rendered multiply:\n%s", listing)
					}
				}
				shared := multiplyAddProgram(t, width, false, true, false)
				listing, err := compile.EmitAssembly(shared, abi.CompileOptions{Architecture: arch, PPC64CPU: level})
				if err != nil || strings.Contains(listing, "maddld") {
					t.Fatalf("shared product was duplicated: %v\n%s", err, listing)
				}
			}
			scaled := multiplyAddProgram(t, nyanjit.NativeBits, false, false, true)
			listing, err := compile.EmitAssembly(scaled, abi.CompileOptions{Architecture: arch, PPC64CPU: level})
			if err != nil || strings.Contains(listing, "maddld") != (level >= abi.PPC64Power9) {
				t.Fatalf("scaled expression CPU %s: %v\n%s", level, err, listing)
			}
		}
	}
}

func TestPPC64CPUOptionValidation(t *testing.T) {
	program := multiplyAddProgram(t, nyanjit.NativeBits, false, false, false)
	for _, options := range []abi.CompileOptions{
		{Architecture: "ppc64", PPC64CPU: 11},
		{Architecture: "ppc64le", PPC64CPU: 1},
		{Architecture: "arm64", PPC64CPU: abi.PPC64Power9},
		{Architecture: "amd64", PPC64CPU: abi.PPC64Power8},
	} {
		if _, err := compile.EmitAssembly(program, options); !errors.Is(err, compile.ErrInvalidFunction) {
			t.Fatalf("options %+v = %v; want ErrInvalidFunction", options, err)
		}
		if _, err := compile.PreflightRegisterScopes(program, options); !errors.Is(err, compile.ErrInvalidFunction) {
			t.Fatalf("scope preflight options %+v = %v; want ErrInvalidFunction", options, err)
		}
	}
}
