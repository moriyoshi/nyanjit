//go:build linux && (amd64 || arm64 || riscv64)

package compile

import (
	"encoding/binary"
	"testing"

	"github.com/moriyoshi/nyanjit/builder"
)

func TestCallIntoWideOutput(t *testing.T) {
	b := builder.NewBuilder()
	address, err := b.ParamNative()
	if err != nil {
		t.Fatal(err)
	}
	low, err := b.ConstBits(64, 0x1122334455667788)
	if err != nil {
		t.Fatal(err)
	}
	high, err := b.ConstBits(64, 0x99aabbccddeeff00)
	if err != nil {
		t.Fatal(err)
	}
	wide, err := b.Concat(low, high)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.StoreBits(address, wide); err != nil {
		t.Fatal(err)
	}
	if err := b.Return(low); err != nil {
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
	output := make([]byte, 16)
	result, err := code.CallInto(output)
	if err != nil {
		t.Fatal(err)
	}
	if result != 0x1122334455667788 || binary.LittleEndian.Uint64(output[:8]) != uint64(result) ||
		binary.LittleEndian.Uint64(output[8:]) != 0x99aabbccddeeff00 {
		t.Fatalf("CallInto wide result = %#x, output = %x", result, output)
	}
}
