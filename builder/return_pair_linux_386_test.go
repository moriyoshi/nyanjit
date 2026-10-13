//go:build linux && 386

package builder

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
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
	program, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	_, err = compileCode(program, compile.TargetSpec{}, compile.Func{ABI: abi.C})
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
	program, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	_, err = compileCode(program, compile.TargetSpec{}, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
	if !errors.Is(err, ErrBackendLimit) {
		t.Fatalf("Compile error = %v, want %v", err, ErrBackendLimit)
	}
}
