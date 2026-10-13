package cpuprofile_test

import (
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/cpuprofile"
	"golang.org/x/sys/cpu"
)

func TestHostPCLMULQDQ(t *testing.T) {
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "386") {
		t.Skip("x86 native CPU discovery")
	}
	if got, want := cpuprofile.Host().Has("pclmulqdq"), cpu.X86.HasPCLMULQDQ; got != want {
		t.Fatalf("Host pclmulqdq=%t, process CPUID=%t", got, want)
	}
	t.Logf("pclmulqdq=%t", cpu.X86.HasPCLMULQDQ)
}
