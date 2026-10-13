package cpuprofile_test

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/cpuprofile"
	"golang.org/x/sys/cpu"
	"runtime"
	"testing"
)

func TestHostProfile(t *testing.T) {
	p := cpuprofile.Host()
	baseline, err := (abi.CPUProfile{}).Resolve(runtime.GOARCH)
	if err != nil {
		t.Skip("no backend for host")
	}
	if !p.Supports(baseline) {
		t.Fatalf("host %+v does not support baseline %+v", p, baseline)
	}
	t.Logf("verified host CPU: %s %v", p.Level, p.Extensions)
	p.Extensions = append(p.Extensions, "mutated")
	if len(p.Extensions) > 1 {
		p.Extensions[0] = "mutated"
	}
	if cpuprofile.Host().Has("mutated") {
		t.Fatal("Host returns shared slice")
	}
}

func TestHostPMULL(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" {
		t.Skip("arm64 native CPU discovery")
	}
	if got, want := cpuprofile.Host().Has("pmull"), cpu.ARM64.HasPMULL; got != want {
		t.Fatalf("Host pmull=%t, process HWCAP=%t", got, want)
	}
	t.Logf("pmull=%t", cpu.ARM64.HasPMULL)
}
