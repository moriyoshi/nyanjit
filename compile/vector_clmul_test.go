package compile

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestCarrylessMulFeatures(t *testing.T) {
	for _, test := range []struct {
		name, arch string
		vectors    VectorPolicy
		cpu        abi.CPUProfile
		native     bool
	}{
		{name: "386-explicit", arch: "386", vectors: VectorsRequire("pclmulqdq"), native: true},
		{name: "386-profile", arch: "386", cpu: abi.CPUProfile{Extensions: []string{"pclmulqdq"}}, native: true},
		{name: "386-disabled", arch: "386", vectors: VectorsOff(), cpu: abi.CPUProfile{Extensions: []string{"pclmulqdq"}}},
		{name: "baseline", arch: "amd64", cpu: abi.CPUProfile{Level: "x86-64-v1"}},
		{name: "sse2-only", arch: "amd64", vectors: VectorsRequire("sse2")},
		{name: "explicit-extension", arch: "amd64", vectors: VectorsRequire("pclmulqdq"), native: true},
		{name: "CPU-extension", arch: "amd64", cpu: abi.CPUProfile{Extensions: []string{"pclmulqdq"}}, native: true},
		{name: "SIMD-disabled", arch: "amd64", vectors: VectorsOff(), cpu: abi.CPUProfile{Extensions: []string{"pclmulqdq"}}},
		{name: "arm64", arch: "arm64"}, {name: "386", arch: "386"}, {name: "riscv64", arch: "riscv64"}, {name: "ppc64le", arch: "ppc64le"},
	} {
		t.Run(test.name, func(t *testing.T) {
			target, err := NewTarget(TargetSpec{Architecture: test.arch, CPU: test.cpu, Vectors: test.vectors})
			if err != nil {
				t.Fatal(err)
			}
			info := target.Vectors()
			if test.native && (!slices.Contains(info.Features, "pclmulqdq") || !slices.Contains(info.Features, "sse2")) {
				t.Fatalf("PCLMULQDQ did not enable SSE2: %+v", info)
			}
			for _, lanes := range []uint16{2, 4, 8} {
				want := abi.Scalarized
				if test.native {
					want = abi.Native
					if lanes > 2 {
						want = abi.Split
					}
				}
				if got := info.Lowering(ir.OpVCarrylessMul, ir.Vec(lanes, 64)); got != want {
					t.Fatalf("lanes=%d lowering=%v want %v", lanes, got, want)
				}
			}
			if info.Lowering(ir.OpVCarrylessMul, ir.Vec(4, 32)) != abi.Scalarized {
				t.Fatal("non-64-bit lanes reported native")
			}
		})
	}
	for _, arch := range []string{"arm64", "riscv64", "ppc64le"} {
		spec := TargetSpec{Architecture: arch, Vectors: VectorsRequire("pclmulqdq")}
		if err := validateVectorFeatures(uncheckedTarget(spec).options(Func{})); !errors.Is(err, ErrBackendLimit) {
			t.Fatalf("%s accepted pclmulqdq: %v", arch, err)
		}
		if _, err := NewTarget(spec); !errors.Is(err, ErrBackendLimit) {
			t.Fatalf("%s target accepted pclmulqdq: %v", arch, err)
		}
		if info := vectorInfo(uncheckedTarget(spec).options(Func{})); info.PreferredBits != 0 || info.Lowering(ir.OpVCarrylessMul, ir.Vec(2, 64)) != abi.Scalarized {
			t.Fatalf("invalid features advertised SIMD: %+v", info)
		}
	}
}

func TestCarrylessMulCPURequirements(t *testing.T) {
	host, err := (abi.CPUProfile{}).Resolve("amd64")
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range []TargetSpec{
		{Architecture: "amd64", Vectors: VectorsRequire("pclmulqdq")},
		{Architecture: "amd64", CPU: abi.CPUProfile{Level: "x86-64-v1"}, Vectors: VectorsRequire("pclmulqdq")},
		{Architecture: "amd64", CPU: abi.CPUProfile{Extensions: []string{"pclmulqdq"}}},
	} {
		requested, err := resolveCPU(uncheckedTarget(spec).options(Func{}))
		if err != nil {
			t.Fatal(err)
		}
		if !requested.Has("pclmulqdq") || !requested.Has("sse2") {
			t.Fatalf("missing required extension: %+v", requested)
		}
		err = validateCPUCompatibility(requested, host)
		if !errors.Is(err, ErrCPUUnsupported) || !errors.Is(err, ErrBackendLimit) {
			t.Fatalf("CPU mismatch=%v; want ErrCPUUnsupported and legacy ErrBackendLimit", err)
		}
		if !strings.Contains(err.Error(), "pclmulqdq") || strings.Contains(err.Error(), ErrBackendLimit.Error()) {
			t.Fatalf("incorrect CPU diagnostic: %v", err)
		}
		if err := validateCPUCompatibility(requested, requested); err != nil {
			t.Fatal(err)
		}
	}
	if errors.Is(ErrBackendLimit, ErrCPUUnsupported) {
		t.Fatal("general backend limits were reclassified as CPU mismatches")
	}
}

func TestCarrylessMulCrossEmission(t *testing.T) {
	b := builder.NewBuilder()
	in1, in2, out := must(b.ParamNative()), must(b.ParamNative()), must(b.ParamNative())
	x, y := must(b.VLoad(ir.Vec(2, 64), in1)), must(b.VLoad(ir.Vec(2, 64), in2))
	mustDo(b.VStore(out, must(b.CarrylessMul(x, y, 0, 1))))
	mustDo(b.Return(must(b.ConstNative(0))))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	for _, explicitFeatures := range []bool{false, true} {
		spec := TargetSpec{Architecture: "amd64", CPU: abi.CPUProfile{Extensions: []string{"pclmulqdq"}}}
		if explicitFeatures {
			spec.CPU = abi.CPUProfile{}
			spec.Vectors = VectorsRequire("pclmulqdq")
		}
		text, err := lowerListing(p, spec, Func{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, "pclmulqdq") || !strings.Contains(text, ", 0x10") {
			t.Fatalf("missing native carryless product:\n%s", text)
		}
	}
}

func Test386CarrylessMulCPURequirements(t *testing.T) {
	host, err := (abi.CPUProfile{}).Resolve("386")
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range []TargetSpec{
		{Architecture: "386", Vectors: VectorsRequire("pclmulqdq")},
		{Architecture: "386", CPU: abi.CPUProfile{Level: "i686"}, Vectors: VectorsRequire("pclmulqdq")},
		{Architecture: "386", CPU: abi.CPUProfile{Extensions: []string{"pclmulqdq"}}},
	} {
		requested, err := resolveCPU(uncheckedTarget(spec).options(Func{}))
		if err != nil {
			t.Fatal(err)
		}
		if !requested.Has("pclmulqdq") || !requested.Has("sse2") {
			t.Fatal(requested)
		}
		if err := validateCPUCompatibility(requested, host); !errors.Is(err, ErrCPUUnsupported) || !errors.Is(err, ErrBackendLimit) {
			t.Fatalf("CPU mismatch: %v", err)
		}
	}
}
