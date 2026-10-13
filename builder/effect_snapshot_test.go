package builder

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestCallEffectSnapshotOwnsSpecialResourcesAndLocations(t *testing.T) {
	profile, err := abi.NewPPC64Profile("ppc64le", abi.C)
	if err != nil {
		t.Fatal(err)
	}
	effect, err := profile.PreflightCall(abi.FunctionCall, 1)
	if err != nil {
		t.Fatal(err)
	}
	b := NewBuilder()
	target, err := b.ConstNative(0x1000)
	if err != nil {
		t.Fatal(err)
	}
	argument, err := b.Const(42)
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.CallCWithEffect(target, effect, argument)
	if err != nil {
		t.Fatal(err)
	}
	effect.ArgumentLocations[0].Words[0].Register = "changed"
	effect.ResultLocation.Words[0].Register = "changed"
	effect.ClobberedSpecialRegisters[0] = "changed"
	if err := b.Return(result); err != nil {
		t.Fatal(err)
	}
	program, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, ins := range program.Instructions {
		if ins.Op != ir.OpCallC {
			continue
		}
		if ins.Effect.ArgumentLocations[0].Words[0].Register != "R3" || ins.Effect.ResultLocation.Words[0].Register != "R3" || ins.Effect.ClobberedSpecialRegisters[0] != "LR" {
			t.Fatalf("caller mutation changed the finished program: %+v", ins.Effect)
		}
		return
	}
	t.Fatal("finished program has no call")
}
