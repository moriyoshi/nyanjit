package nyanjit_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/compile"
)

// Var names itself in its errors, like the operations that share its helper.
func TestVarErrorNamesVar(t *testing.T) {
	b := nyanjit.NewBuilder()
	b.Var(nyanjit.Value{})
	_, err := b.Finish()
	if !errors.Is(err, compile.ErrInvalidFunction) {
		t.Fatalf("Finish = %v, want ErrInvalidFunction", err)
	}
	if !strings.HasPrefix(err.Error(), "Var: ") {
		t.Fatalf("Var error %q does not start with %q", err, "Var: ")
	}
}

// A message that already names Var is not prefixed twice.
func TestVarErrorKeepsOwnPrefix(t *testing.T) {
	b := nyanjit.NewBuilder()
	x := b.Param()
	b.Enter()
	label := b.NewLabel()
	b.Jump(label)
	b.Bind(label)
	b.Var(x)
	_, err := b.Finish()
	if err == nil || !strings.HasPrefix(err.Error(), "Var: ") || strings.HasPrefix(err.Error(), "Var: Var: ") {
		t.Fatalf("Var error = %v, want one Var prefix", err)
	}
}

// Successful operations without a result record no error and allocate
// nothing for it.
func TestSuccessfulVoidOperationsDoNotAllocate(t *testing.T) {
	b := nyanjit.NewBuilder()
	x := b.Param()
	v := b.Var(x)
	allocs := testing.AllocsPerRun(1000, func() { b.Set(v, x) })
	if allocs != 0 {
		t.Fatalf("Set allocated %v times per call, want 0", allocs)
	}
	b.Return(b.Get(v))
	if _, err := b.Finish(); err != nil {
		t.Fatal(err)
	}
}
