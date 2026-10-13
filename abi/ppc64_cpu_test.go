package abi_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
)

func TestPPC64CPUResolve(t *testing.T) {
	for _, level := range []abi.PPC64CPU{0, 8, 9, 10} {
		got, err := level.Resolve()
		want := level
		if want == 0 {
			want = abi.PPC64Power8
		}
		if err != nil || got != want {
			t.Fatalf("Resolve(%d)=%d,%v; want %d", level, got, err, want)
		}
		if got.String() == "invalid" {
			t.Fatalf("invalid CPU name for %d", got)
		}
	}
	for _, invalid := range []abi.PPC64CPU{1, 7, 11, 255} {
		if _, err := invalid.Resolve(); !errors.Is(err, abi.ErrInvalidSpec) {
			t.Errorf("Resolve(%d)=%v; want ErrInvalidSpec", invalid, err)
		}
	}
}
