//go:build linux && (amd64 || arm64 || 386 || riscv64)

package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/builder"
)

func TestWordsOutput(t *testing.T) {
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
	code, err := compileCode(program, TargetSpec{}, Func{ABI: abi.C, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	output := []byte{0, 0xcc}
	got, err := code.Call(execution.Words(&output[0])...)
	if err != nil {
		t.Fatal(err)
	}
	if got != 77 || output[0] != 0x5a || output[1] != 0xcc {
		t.Fatalf("Call(Words) = (%d, %x), want (77, 5acc)", got, output)
	}
}
