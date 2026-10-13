//go:build linux && (amd64 || arm64 || riscv64)

package compile

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/builder"
)

func TestPrepareGoRequiresPointerFreeAssertion(t *testing.T) {
	b := builder.NewBuilder()
	value, err := b.ConstBits(64, 7)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(value); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	_, err = PrepareGo(program, CompileOptions{ABI: ABIGo, Stack: StackPlatform, StackBytesAvailable: 240})
	if !errors.Is(err, ErrPointerMapsRequired) {
		t.Fatalf("PrepareGo without pointer assertion = %v, want ErrPointerMapsRequired", err)
	}
}
