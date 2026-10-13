package compile_test

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/cpuprofile"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestCPUProfileCrossEmission(t *testing.T) {
	b := nyanjit.NewBuilder()
	x, y, z := b.Param(), b.Param(), b.Param()
	b.Return(x.FMul(y).FAdd(z))
	p := finishCrossAssembly(t, b)
	for _, tc := range []struct {
		level string
		fma   bool
	}{{"x86-64-v1", false}, {"x86-64-v3", true}} {
		text, err := lowerListing(p, compile.TargetSpec{Architecture: "amd64", CPU: abi.CPUProfile{Level: tc.level}}, compile.Func{})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "vfmadd") != tc.fma {
			t.Fatalf("%s FMA=%v:\n%s", tc.level, tc.fma, text)
		}
	}
}
func TestCPUProfileNativeGate(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("callable backend requires Linux")
	}
	b := nyanjit.NewBuilder()
	b.Return(b.Const(42))
	p := finishCrossAssembly(t, b)
	// Pick an extension absent from the host; optional instructions are rejected
	// before executable allocation, even when this particular program is trivial.
	candidates := map[string][]string{"amd64": {"avx512vl", "fma", "bmi2"}, "arm64": {"sve2", "sve"}, "riscv64": {"zic64b", "supm"}, "386": {"bmi2"}, "ppc64le": {"ca32"}}
	for _, e := range candidates[runtime.GOARCH] {
		if cpuprofile.Host().Has(e) {
			continue
		}
		profile := abi.CPUProfile{Extensions: []string{e}}
		if runtime.GOARCH == "ppc64le" {
			profile.Level = "power9"
		}
		code, err := nyanjit.Compile(p, compile.TargetSpec{CPU: profile}, compile.Func{})
		if code != nil {
			code.Close()
		}
		if !errors.Is(err, compile.ErrCPUUnsupported) || !errors.Is(err, compile.ErrBackendLimit) {
			t.Fatalf("unsupported %s: %v", e, err)
		}
		return
	}
	t.Skip("host supplies every tested optional extension")
}

func TestCPUProfileCompatibilityOptions(t *testing.T) {
	b := nyanjit.NewBuilder()
	x, y, z := b.Param(), b.Param(), b.Param()
	b.Return(x.FMul(y).FAdd(z))
	p := finishCrossAssembly(t, b)
	// The former AMD64FMA and PPC64CPU options are now spelled in the CPU
	// profile: an "fma" extension and a POWER level.
	modern, err := lowerListing(p, compile.TargetSpec{Architecture: "amd64", CPU: abi.CPUProfile{Extensions: []string{"fma"}}}, compile.Func{})
	if err != nil || !strings.Contains(modern, "vfmadd") {
		t.Fatalf("FMA extension does not enable FMA: %v", err)
	}
	for _, arch := range []string{"ppc64le"} {
		upper, err := lowerListing(p, compile.TargetSpec{Architecture: arch, CPU: abi.CPUProfile{Level: "POWER9"}}, compile.Func{})
		if err != nil {
			t.Fatalf("case-insensitive POWER level: %v", err)
		}
		lower, err := lowerListing(p, compile.TargetSpec{Architecture: arch, CPU: abi.CPUProfile{Level: abi.PPC64Power9.String()}}, compile.Func{})
		if err != nil || lower != upper {
			t.Fatalf("POWER9 spellings disagree: %v", err)
		}
		if _, err := lowerListing(p, compile.TargetSpec{Architecture: arch, CPU: abi.CPUProfile{Level: "POWER7"}}, compile.Func{}); !errors.Is(err, compile.ErrInvalidFunction) {
			t.Fatalf("unsupported POWER level: %v", err)
		}
	}
}

func TestCPUProfileReport(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("callable backend requires Linux")
	}
	b := nyanjit.NewBuilder()
	b.Return(b.Const(42))
	p := finishCrossAssembly(t, b)
	expected, err := (abi.CPUProfile{}).Resolve(runtime.GOARCH)
	if err != nil {
		t.Skip("no backend")
	}
	report, err := lowerReport(p, compile.TargetSpec{Architecture: runtime.GOARCH}, compile.Func{})
	if err != nil {
		t.Fatal(err)
	}
	if report.CPU().Level != expected.Level {
		t.Fatalf("report: %+v", report.CPU())
	}
}
