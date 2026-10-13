package abi_test

import (
	"errors"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"testing"
)

func TestCPUProfiles(t *testing.T) {
	for _, tc := range []struct{ arch, level, feature string }{
		{"amd64", "x86-64-v1", "sse2"}, {"amd64", "x86-64-v3", "bmi2"}, {"amd64", "x86-64-v4", "avx512vl"},
		{"arm64", "armv8.0-a", "neon"}, {"riscv64", "rva20", "za128rs"}, {"riscv64", "rva22", "zic64b"}, {"riscv64", "rva23", "supm"}, {"ppc64le", "power9", "maddld"},
	} {
		p, err := (abi.CPUProfile{Level: tc.level}).Resolve(tc.arch)
		if err != nil || !p.Has(tc.feature) {
			t.Fatalf("%s/%s: %+v %v", tc.arch, tc.level, p, err)
		}
		if !p.Supports(p) {
			t.Fatal("profile does not support itself")
		}
	}
	p, err := (abi.CPUProfile{Extensions: []string{"FMA", "fma"}}).Resolve("amd64")
	if err != nil || !p.Has("avx") || !p.Has("xsave") || len(p.Extensions) != 4 {
		t.Fatalf("normalized profile: %+v %v", p, err)
	}
	c := p.Clone()
	c.Extensions[0] = "mutated"
	if p.Has("mutated") {
		t.Fatal("clone aliases extensions")
	}
	for _, p := range []abi.CPUProfile{{Level: "power9"}, {Extensions: []string{"zbb"}}} {
		if _, err := p.Resolve("amd64"); !errors.Is(err, abi.ErrInvalidSpec) {
			t.Fatalf("bad profile: %v", err)
		}
	}
}

func TestCPUProfileDependencies(t *testing.T) {
	for _, extension := range []string{"avx512f", "avx512bw", "avx512cd", "avx512dq", "avx512vl"} {
		p, err := (abi.CPUProfile{Extensions: []string{extension}}).Resolve("amd64")
		if err != nil || !p.Has("avx512f") || !p.Has("avx") || !p.Has("xsave") {
			t.Fatalf("%s prerequisites: %+v,%v", extension, p, err)
		}
		again, err := p.Resolve("amd64")
		if err != nil || len(again.Extensions) != len(p.Extensions) {
			t.Fatal("dependency resolution is not idempotent")
		}
	}
	for _, arch := range []string{"ppc64le"} {
		for _, extension := range []string{"maddld", "ca32"} {
			for _, level := range []string{"", "power8", "POWER8"} {
				if _, err := (abi.CPUProfile{Level: level, Extensions: []string{extension}}).Resolve(arch); !errors.Is(err, abi.ErrInvalidSpec) {
					t.Fatalf("%s %s+%s: %v; want ErrInvalidSpec", arch, level, extension, err)
				}
			}
			for _, level := range []string{"power9", "power10"} {
				if _, err := (abi.CPUProfile{Level: level, Extensions: []string{extension}}).Resolve(arch); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestPOWERProfileMinimumLevel(t *testing.T) {
	for _, arch := range []string{"ppc64le"} {
		levels := []string{"power8", "power9", "power10"}
		for hostRank, hostLevel := range levels {
			host, err := (abi.CPUProfile{Level: hostLevel}).Resolve(arch)
			if err != nil {
				t.Fatal(err)
			}
			for requestRank, requestLevel := range levels {
				request, err := (abi.CPUProfile{Level: requestLevel}).Resolve(arch)
				if err != nil {
					t.Fatal(err)
				}
				if got, want := host.Supports(request), hostRank >= requestRank; got != want {
					t.Errorf("%s host %s supports %s = %t; want %t", arch, hostLevel, requestLevel, got, want)
				}
			}
		}
	}
}

func TestPCLMULQDQProfile(t *testing.T) {
	baseline, err := (abi.CPUProfile{}).Resolve("amd64")
	if err != nil {
		t.Fatal(err)
	}
	p, err := (abi.CPUProfile{Extensions: []string{"PCLMULQDQ", "pclmulqdq"}}).Resolve("amd64")
	if err != nil || !p.Has("pclmulqdq") || !p.Has("sse2") || len(p.Extensions) != 2 {
		t.Fatalf("profile=%+v err=%v", p, err)
	}
	if baseline.Has("pclmulqdq") || baseline.Supports(p) || !p.Supports(baseline) {
		t.Fatal("optional extension treated as mandatory baseline")
	}
	for _, arch := range []string{"arm64", "riscv64", "ppc64le"} {
		if _, err := (abi.CPUProfile{Extensions: []string{"pclmulqdq"}}).Resolve(arch); !errors.Is(err, abi.ErrInvalidSpec) {
			t.Fatalf("%s accepted x86 extension: %v", arch, err)
		}
	}
}

func Test386RequiresSSE2(t *testing.T) {
	for _, p := range []abi.CPUProfile{{}, {Level: "i686"}, {Extensions: []string{"sse2"}}} {
		got, err := p.Resolve("386")
		if err != nil || got.Level != "i686" || !got.Has("sse2") {
			t.Fatalf("386 baseline=%+v err=%v", got, err)
		}
	}
	if _, err := (abi.CPUProfile{Extensions: []string{"fma"}}).Resolve("386"); !errors.Is(err, abi.ErrInvalidSpec) {
		t.Fatal("386 accepted FMA")
	}
}

func Test386PCLMULQDQProfile(t *testing.T) {
	base, err := (abi.CPUProfile{}).Resolve("386")
	if err != nil {
		t.Fatal(err)
	}
	requested, err := (abi.CPUProfile{Extensions: []string{"PCLMULQDQ"}}).Resolve("386")
	if err != nil || !requested.Has("pclmulqdq") || !requested.Has("sse2") || base.Supports(requested) || !requested.Supports(base) {
		t.Fatalf("base%+v request%+v err%v", base, requested, err)
	}
}

func TestResolvedProfileEditsDoNotLeaveStaleCapabilities(t *testing.T) {
	p, err := (abi.CPUProfile{Extensions: []string{"bmi2"}}).Resolve("amd64")
	if err != nil {
		t.Fatal(err)
	}
	for n, ext := range p.Extensions {
		if ext == "bmi2" {
			p.Extensions[n] = "popcnt"
		}
	}
	if p.Has("bmi2") || !p.Has("POPCNT") {
		t.Fatal("capability cache ignored a public field edit")
	}
	q, err := p.Resolve("amd64")
	if err != nil || q.Has("bmi2") || !q.Has("popcnt") {
		t.Fatalf("re-resolve: %+v, %v", q, err)
	}
}
