package compile_test

import (
	"embed"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

//go:embed testdata/simd-*.s
var simdGoldens embed.FS

func simdExample(t *testing.T, number int) *ir.Program {
	t.Helper()
	b := nyanjit.NewBuilder()
	switch number {
	case 1:
		dst, a, c := b.Param(), b.Param(), b.Param()
		shape := ir.Vec(4, 32)
		b.VStore(dst, b.Add(b.VLoad(shape, a), b.VLoad(shape, c)))
		b.Return(b.Literal(0))
	case 3:
		p, needle := b.Param(), b.ParamBits(8)
		shape := ir.Vec(16, 8)
		b.Return(b.MaskBits(b.VCompare(b.VLoad(shape, p), ir.BranchEqual, b.Splat(shape, needle))))
	}
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestSIMDExampleAssemblyGoldens(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		for _, example := range []int{1, 3} {
			p := simdExample(t, example)
			listing, err := lowerListing(p, compile.TargetSpec{Architecture: arch}, compile.Func{})
			if err != nil {
				t.Fatal(err)
			}
			suffix := "add"
			if example == 3 {
				suffix = "byte-mask"
			}
			path := filepath.Join("testdata", "simd-"+arch+"-"+suffix+".s")
			want, err := simdGoldens.ReadFile(filepath.ToSlash(path))
			if err != nil {
				t.Fatal(err)
			}
			if listing != string(want) {
				t.Fatalf("%s changed:\ngot:\n%s\nwant:\n%s", path, listing, want)
			}
		}
	}
}
func TestSIMDFeaturesAndSplitListing(t *testing.T) {
	b := nyanjit.NewBuilder()
	dst, a := b.Param(), b.Param()
	shape := ir.Vec(16, 32)
	b.VStore(dst, b.Add(b.VLoad(shape, a), b.Splat(shape, b.LiteralBits(32, 1))))
	b.Return(b.Literal(0))
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"amd64", "arm64", "386", "riscv64", "ppc64le"} {
		listing, err := lowerListing(p, compile.TargetSpec{Architecture: arch}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
		if err != nil {
			t.Fatalf("%s: %v", arch, err)
		}
		mnemonic := "paddd"
		if arch == "arm64" {
			mnemonic = "add V"
		}
		if (arch == "amd64" || arch == "386" || arch == "arm64") && strings.Count(listing, mnemonic) != 4 {
			t.Fatalf("%s: shape was not split into four SIMD adds:\n%s", arch, listing)
		}
	}
	if _, err := lowerListing(p, compile.TargetSpec{Architecture: "arm64", Vectors: compile.VectorsRequire("avx2")}, compile.Func{}); err == nil {
		t.Fatal("incompatible feature accepted")
	}
	scalar, err := compile.NewTarget(compile.TargetSpec{Architecture: "arm64", Vectors: compile.VectorsOff()})
	if err != nil {
		t.Fatal(err)
	}
	if scalar.Vectors().Lowering(ir.OpAdd, shape) != abi.Scalarized {
		t.Fatal("empty Features did not request scalarization")
	}
}
