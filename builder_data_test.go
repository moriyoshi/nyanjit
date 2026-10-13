package nyanjit_test

import (
	"errors"
	"strings"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestDataDeferredErrors(t *testing.T) {
	for _, pointer := range []bool{false, true} {
		b := n.NewBuilder()
		var s n.Data
		if pointer {
			s = n.PointerParam[struct{ X uint32 }](b)
		} else {
			s = n.ValueParam[struct{ X uint32 }](b)
		}
		s.Field("Missing")
		s.At("X").Field("Later")
		s.At("X").Store(b.Const(4))
		b.Return(b.Const(0))
		_, err := b.Finish()
		if !errors.Is(err, compile.ErrInvalidFunction) || !strings.Contains(err.Error(), "Missing") || strings.Contains(err.Error(), "Later") {
			t.Fatalf("first error: %v", err)
		}
	}
	b := n.NewBuilder()
	n.ValueParam[struct{ Bad string }](b)
	b.Return(b.Const(0))
	if _, err := b.Finish(); !errors.Is(err, compile.ErrInvalidFunction) || !strings.Contains(err.Error(), "Bad") {
		t.Fatalf("layout error: %v", err)
	}
	b = n.NewBuilder()
	s := n.ValueParam[struct{ X uint64 }](b)
	s.At("X").Store(b.Const(1))
	if _, err := b.Finish(); !errors.Is(err, compile.ErrInvalidFunction) || !strings.Contains(err.Error(), "writable") {
		t.Fatalf("read-only Store: %v", err)
	}
	// Zero/uninitialized wrappers and nil builders must not panic.
	n.ValueParam[struct{}](nil).Field("X")
	var zero n.Data
	zero.Field("X")
	zero.At("X").Addr()
	zero.Store(n.Value{})
	zero.Index(n.Value{}, n.Label{})
}
