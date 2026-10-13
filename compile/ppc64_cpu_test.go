package compile_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/builder"
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
	program, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	return program
}

// ppc64LevelSpec returns a target spec selecting a POWER level; the
// unspecified level keeps the target baseline.
func ppc64LevelSpec(architecture string, level abi.PPC64CPU) compile.TargetSpec {
	spec := compile.TargetSpec{Architecture: architecture}
	if level != abi.PPC64CPUUnspecified {
		spec.CPU.Level = level.String()
	}
	return spec
}

func TestPPC64CPUAssemblyFusion(t *testing.T) {
	for _, arch := range []string{"ppc64le"} {
		for _, level := range []abi.PPC64CPU{0, 8, 9, 10} {
			for _, width := range []uint8{8, 16, 32, 64} {
				if width == 64 && builder.NativeBits == 32 {
					// Builder values wider than its host word are paired;
					// those IR shapes have separate wide-arithmetic coverage.
					continue
				}
				for _, reverse := range []bool{false, true} {
					program := multiplyAddProgram(t, width, reverse, false, false)
					listing, err := lowerListing(program, ppc64LevelSpec(arch, level), compile.Func{})
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
				listing, err := lowerListing(shared, ppc64LevelSpec(arch, level), compile.Func{})
				if err != nil || strings.Contains(listing, "maddld") {
					t.Fatalf("shared product was duplicated: %v\n%s", err, listing)
				}
			}
			scaled := multiplyAddProgram(t, builder.NativeBits, false, false, true)
			listing, err := lowerListing(scaled, ppc64LevelSpec(arch, level), compile.Func{})
			if err != nil || strings.Contains(listing, "maddld") != (level >= abi.PPC64Power9) {
				t.Fatalf("scaled expression CPU %s: %v\n%s", level, err, listing)
			}
		}
	}
}

func TestPPC64CPUOptionValidation(t *testing.T) {
	program := multiplyAddProgram(t, builder.NativeBits, false, false, false)
	for _, spec := range []compile.TargetSpec{
		{Architecture: "ppc64le", CPU: abi.CPUProfile{Level: "power7"}},
		ppc64LevelSpec("arm64", abi.PPC64Power9),
		ppc64LevelSpec("amd64", abi.PPC64Power8),
	} {
		if _, err := lowerListing(program, spec, compile.Func{}); !errors.Is(err, compile.ErrInvalidFunction) {
			t.Fatalf("spec %+v = %v; want ErrInvalidFunction", spec, err)
		}
		u, err := lowerUnit(program, spec, compile.Func{})
		if err == nil {
			_, err = u.RegisterScopes()
		}
		if !errors.Is(err, compile.ErrInvalidFunction) {
			t.Fatalf("scope preflight spec %+v = %v; want ErrInvalidFunction", spec, err)
		}
	}
}
