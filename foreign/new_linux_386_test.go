//go:build linux && 386

package foreign

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/execution"
)

func TestCallRejects386UintptrSignature(t *testing.T) {
	target, err := New(1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Call(nyanjit.NewBuilder(), target); !errors.Is(err, execution.ErrUnsupported) {
		t.Fatalf("Call() = %v, want ErrUnsupported", err)
	}
}
