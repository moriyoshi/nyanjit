package compile_test

import (
	"errors"
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/cpuprofile"
	"runtime"
	"strings"
	"testing"
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
		options := abi.CompileOptions{Architecture: "amd64", CPU: abi.CPUProfile{Level: tc.level}}
		text, err := compile.EmitAssembly(p, options)
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
	candidates := map[string][]string{"amd64": {"avx512vl", "fma", "bmi2"}, "arm64": {"sve2", "sve"}, "riscv64": {"zic64b", "supm"}, "386": {"bmi2"}, "ppc64": {"ca32"}, "ppc64le": {"ca32"}}
	for _, e := range candidates[runtime.GOARCH] {
		if cpuprofile.Host().Has(e) {
			continue
		}
		profile := abi.CPUProfile{Extensions: []string{e}}
		if runtime.GOARCH == "ppc64" || runtime.GOARCH == "ppc64le" {
			profile.Level = "power9"
		}
		code, err := compile.Compile(p, abi.CompileOptions{CPU: profile})
		if code != nil {
			code.Close()
		}
		if !errors.Is(err, compile.ErrBackendLimit) {
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
	legacy, err := compile.EmitAssembly(p, abi.CompileOptions{Architecture: "amd64", AMD64FMA: true})
	if err != nil {
		t.Fatal(err)
	}
	modern, err := compile.EmitAssembly(p, abi.CompileOptions{Architecture: "amd64", CPU: abi.CPUProfile{Extensions: []string{"fma"}}})
	if err != nil || legacy != modern || !strings.Contains(legacy, "vfmadd") {
		t.Fatalf("legacy FMA does not map to CPU profile: %v", err)
	}
	for _, arch := range []string{"ppc64", "ppc64le"} {
		options := abi.CompileOptions{Architecture: arch, PPC64CPU: abi.PPC64Power9, CPU: abi.CPUProfile{Level: "POWER9"}}
		if _, err := compile.EmitAssembly(p, options); err != nil {
			t.Fatalf("case-insensitive POWER agreement: %v", err)
		}
		options.CPU.Level = "POWER10"
		if _, err := compile.EmitAssembly(p, options); !errors.Is(err, compile.ErrInvalidFunction) {
			t.Fatalf("conflicting POWER options: %v", err)
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
	report, err := compile.Analyze(p, abi.CompileOptions{Architecture: runtime.GOARCH})
	if err != nil {
		t.Fatal(err)
	}
	if report.CPU.Level != expected.Level {
		t.Fatalf("report: %+v", report.CPU)
	}
}
