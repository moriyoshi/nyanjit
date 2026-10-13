package foreign

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/execution"
	"github.com/moriyoshi/nyanjit/regalloc"
)

func TestNewCopiesEffect(t *testing.T) {
	effect := &abi.CallEffect{ArgumentRegisters: []string{"R0"}, ClobberedRegisters: []string{"R1"}}
	target, err := New(1, 1, effect)
	if err != nil {
		t.Fatal(err)
	}
	effect.ArgumentRegisters[0] = "changed"
	effect.ClobberedRegisters[0] = "changed"
	if target.effect.ArgumentRegisters[0] != "R0" || target.effect.ClobberedRegisters[0] != "R1" {
		t.Fatalf("effect changed through caller alias: %+v", target.effect)
	}
}

func TestCallRejectsWrongArity(t *testing.T) {
	target, err := New(1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Call(nyanjit.NewBuilder(), target); !errors.Is(err, execution.ErrArity) {
		t.Fatalf("Call with no arguments = %v, want ErrArity", err)
	}
	if _, err := RegisterCallback(42); !errors.Is(err, regalloc.ErrInvalidFunction) {
		t.Fatalf("invalid callback = %v, want ErrInvalidFunction", err)
	}
}
