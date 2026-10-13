package compile

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

func TestResolvedCPUBoundaryPreservesVerifiedFeatures(t *testing.T) {
	for _, arch := range []string{"386", "amd64", "arm64", "riscv64", "loong64", "ppc64le"} {
		requested, err := (abi.CPUProfile{}).Resolved(arch)
		if err != nil {
			t.Fatal(err)
		}
		host := requested.Profile()
		if err := validateResolvedCPUCompatibility(requested, host); err != nil {
			t.Fatal(err)
		}
		if len(host.Extensions) > 0 {
			host.Extensions = host.Extensions[1:]
			if err := validateResolvedCPUCompatibility(requested, host); !errors.Is(err, ErrCPUUnsupported) {
				t.Fatalf("%s invented missing verified feature: %v", arch, err)
			}
		}
	}
}
