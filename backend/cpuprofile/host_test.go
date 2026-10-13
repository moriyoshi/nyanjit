package cpuprofile_test

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/cpuprofile"
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
