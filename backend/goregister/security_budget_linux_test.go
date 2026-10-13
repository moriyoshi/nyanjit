//go:build !nyanjit_no_goregister && linux && (amd64 || arm64 || 386 || riscv64 || loong64 || ppc64le)

package goregister

import (
	"errors"
	"testing"
)

func TestSecurityRegistrationBudget(t *testing.T) {
	jitRegistry.Lock()
	old := registrationLimit
	registrationLimit = len(jitRegistry.modules)
	jitRegistry.Unlock()
	defer func() { jitRegistry.Lock(); registrationLimit = old; jitRegistry.Unlock() }()
	used, limit := RegisteredFunctions()
	if used != limit {
		t.Fatal("bad budget statistics")
	}
	linked, e := Register([]byte{1}, Frame{CodeBytes: 1, PointerFree: true})
	if linked || !errors.Is(e, ErrRegistrationLimit) {
		t.Fatalf("registration crossed budget: linked=%v err=%v", linked, e)
	}
	after, _ := RegisteredFunctions()
	if after != used {
		t.Fatal("failed registration retained metadata")
	}
}
