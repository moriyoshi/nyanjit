package execution

import (
	"errors"
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"reflect"
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

// The exported call helpers reach an address chosen at run time, so a fixed
// CallGo target must not name them (security audit 2026-10-25, L24).
func TestGoCallTargetRejectsExecutionHelpers(t *testing.T) {
	helpers := map[string]any{
		"Call": Call, "CallWithArity": CallWithArity, "CallPairWithArity": CallPairWithArity,
		"CallForeign": CallForeign, "CallForeignPair": CallForeignPair,
		"(*Code).Call": (*Code).Call, "(*Code).CallPair": (*Code).CallPair, "Map": Map, "Unmap": Unmap,
	}
	for name, fn := range helpers {
		if err := validateGoCallTarget(uint64(reflect.ValueOf(fn).Pointer())); !errors.Is(err, backend.ErrInvalidFunction) {
			t.Errorf("%s accepted as a CallGo target: %v", name, err)
		}
	}
}
