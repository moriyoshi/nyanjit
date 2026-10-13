package execution

import (
	"errors"
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"testing"
)

func TestGoCallTrampolinesRejected(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime lookup disabled")
	}
	// Force callbackasm and reflect.makeFuncStub into the executable; never call
	// these entry points with a fabricated signature.
	if _, err := RegisterForeignCallback(func() uintptr { return 0 }); err != nil {
		t.Fatal(err)
	}
	names := []string{"github.com/moriyoshi/nyanjit/backend/execution.call", "github.com/moriyoshi/nyanjit/backend/execution.callPair", "github.com/moriyoshi/nyanjit/backend/execution.call386", "runtime.call16", "runtime.asmcgocall", "runtime.systemstack", "runtime.morestack", "runtime.morestack_noctxt", "reflect.makeFuncStub", "callbackasm"}
	checked := 0
	for _, name := range names {
		pc, err := goregister.RuntimeEntry(name)
		if err != nil {
			continue
		}
		checked++
		t.Run(name, func(t *testing.T) {
			code, err := NewCode([]byte{0}, CodeOptions{GoCallTargets: []GoCallTarget{{Address: uint64(pc)}}}, nil)
			if code != nil {
				code.Close()
			}
			if !errors.Is(err, backend.ErrInvalidFunction) {
				t.Fatalf("trampoline accepted: %v", err)
			}
		})
	}
	if checked < 3 {
		t.Fatalf("only %d trampoline fixtures found", checked)
	}
	pc, err := goregister.RuntimeEntry("runtime.GC")
	if err != nil {
		t.Fatal(err)
	}
	if err = validateGoCallTarget(uint64(pc)); err != nil {
		t.Fatalf("ordinary Go function rejected: %v", err)
	}
}
