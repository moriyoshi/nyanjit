//go:build nyanjit_no_goregister

package goregister

import (
	"errors"
	"testing"
)

func TestRegistrationDisabled(t *testing.T) {
	if Supported() {
		t.Fatal("managed registration enabled")
	}
	if _, err := Register([]byte{0}, Frame{}); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := RuntimeEntry("runtime.GC"); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if n, _ := RegisteredFunctions(); n != 0 {
		t.Fatal(n)
	}
}
