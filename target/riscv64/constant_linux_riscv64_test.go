//go:build linux && riscv64

package riscv64_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/execution"
	"github.com/moriyoshi/nyanjit/target/riscv64"
)

func TestConstantWordsExecute(t *testing.T) {
	values := []uint64{
		0, 1, 2047, 2048, 4095, 4096,
		0x7fffffff, 0x80000000, 0xffffffff, 0x100000000,
		0x123456789abcdef0, 0x7fffffffffffffff, 0x8000000000000000,
		^uint64(0), ^uint64(2047), ^uint64(2048),
	}
	state := uint64(0x123456789abcdef0)
	for i := 0; i < 64; i++ {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		values = append(values, state)
	}
	for _, want := range values {
		a := riscv64.NewByteAssembler(abi.StackNoUse, 0)
		a.Constant(10, want)
		a.Ret()
		code, err := execution.NewCode(a.Bytes(), execution.CodeOptions{ABI: abi.C}, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, callErr := code.Call()
		closeErr := code.Close()
		if callErr != nil || closeErr != nil || uint64(got) != want {
			t.Fatalf("constant %#x executed as %#x (call: %v, close: %v)", want, got, callErr, closeErr)
		}
	}
}
