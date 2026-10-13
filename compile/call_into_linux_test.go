//go:build linux && (amd64 || arm64 || 386 || riscv64)

package compile

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/execution"
)

func TestCallIntoPinnedOutput(t *testing.T) {
	b := builder.NewBuilder()
	address, err := b.ParamNative()
	if err != nil {
		t.Fatal(err)
	}
	value, err := b.ConstBits(8, 0x5a)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.StoreBits(address, value); err != nil {
		t.Fatal(err)
	}
	answer, err := b.ConstBits(32, 77)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(answer); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := Compile(program, CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	if _, err := code.CallInto(nil); !errors.Is(err, execution.ErrEmptyOutput) {
		t.Fatalf("CallInto(nil) = %v, want ErrEmptyOutput", err)
	}
	output := []byte{0, 0xcc}
	got, err := code.CallInto(output)
	if err != nil {
		t.Fatal(err)
	}
	if got != 77 || output[0] != 0x5a || output[1] != 0xcc {
		t.Fatalf("CallInto = (%d, %x), want (77, 5acc)", got, output)
	}
}
