//go:build linux && 386

package builder

import (
	"errors"
	"github.com/moriyoshi/nyanjit/compile"
	"testing"
)

func TestPreflight386CStackArgumentsAndPairResult(t *testing.T) {
	effect, err := compile.PreflightCall(CallSpec{Kind: FunctionCall, ABI: ABIC, ArgumentCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	if effect.Architecture != "386" || len(effect.ArgumentLocations) != 2 || len(effect.ArgumentRegisters) != 2 ||
		effect.ArgumentRegisters[0] != "" || effect.ResultRegister != "" {
		t.Fatalf("386 call shape = %+v", effect)
	}
	for arg, location := range effect.ArgumentLocations {
		if len(location.Words) != 1 || location.Words[0].Storage != ABIStack ||
			location.Words[0].StackOffset != 4*arg {
			t.Fatalf("argument %d = %+v", arg, location)
		}
	}
	if words := effect.ResultLocation.Words; len(words) != 2 || words[0].Register != "EAX" || words[1].Register != "EDX" {
		t.Fatalf("386 result = %+v", words)
	}
	if !effect.RequiresCallStack || effect.StackAlignment != 16 || !effect.FlagsClobbered || len(effect.ClobberedRegisters) != 3 {
		t.Fatalf("386 call effect = %+v", effect)
	}
	goEffect, err := compile.PreflightCall(CallSpec{Kind: FunctionCall, ABI: ABIGo, ArgumentCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	if goEffect.ResultLocation.Words[0].Storage != ABIStack || goEffect.ResultLocation.Words[0].StackOffset != 8 ||
		goEffect.ArgumentLocations[1].Words[0].StackOffset != 4 || goEffect.StackAlignment != 4 {
		t.Fatalf("386 Go call shape = %+v", goEffect)
	}
	if _, err := compile.PreflightCall(CallSpec{Kind: SystemCall, ABI: ABIC}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("system call = %v, want ErrUnsupported", err)
	}
}
