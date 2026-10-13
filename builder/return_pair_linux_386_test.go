//go:build linux && 386

package builder

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/compile"
)

func TestWideReturnReportsBackendLimit386(t *testing.T) {
	b := NewBuilder()
	wide, err := b.ConstBits(128, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(wide); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	_, err = compile.Compile(program, CompileOptions{ABI: ABIC})
	if !errors.Is(err, ErrBackendLimit) {
		t.Fatalf("Compile error = %v, want %v", err, ErrBackendLimit)
	}
}

func TestLabeledWideReturnReportsBackendLimit386(t *testing.T) {
	b := NewBuilder()
	entry := b.NewLabel()
	if err := b.Bind(entry); err != nil {
		t.Fatal(err)
	}
	wide, err := b.ConstBits(128, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(wide); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	_, err = compile.Compile(program, CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536})
	if !errors.Is(err, ErrBackendLimit) {
		t.Fatalf("Compile error = %v, want %v", err, ErrBackendLimit)
	}
}
