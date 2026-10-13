package cpuprofile

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"testing"
)

func Test386SSE2DiscoveryIsFailClosed(t *testing.T) {
	required, err := (abi.CPUProfile{}).Resolve("386")
	if err != nil {
		t.Fatal(err)
	}
	if !required.Has("sse2") {
		t.Fatal("386 baseline omits SSE2")
	}
	for _, p := range []abi.CPUProfile{x86FallbackProfile("386"), resolveVerifiedX86Profile(abi.CPUProfile{}, "386", false), resolveVerifiedX86Profile(abi.CPUProfile{Level: "invalid"}, "386", true)} {
		if p.Has("sse2") || p.Supports(required) {
			t.Fatalf("unverified profile claims SSE2: %+v", p)
		}
	}
	if p := resolveVerifiedX86Profile(abi.CPUProfile{}, "386", true); !p.Supports(required) {
		t.Fatalf("verified SSE2 rejected: %+v", p)
	}
}
