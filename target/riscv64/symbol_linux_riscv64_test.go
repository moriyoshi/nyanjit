//go:build linux && riscv64

package riscv64_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/execution"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/target/riscv64"
)

func TestSymbolRelocationRoundTrip(t *testing.T) {
	a := riscv64.NewByteAssembler(abi.StackNoUse, 0)
	a.Constant(11, 0x123456789abcdef0)
	a.Symbol(10, "value")
	a.Ret()
	original := a.Bytes()
	if relocations := a.Relocations(); len(relocations) != 1 || relocations[0].Offset == 0 {
		t.Fatalf("symbol relocation after constant = %+v", relocations)
	}
	for _, value := range []uint64{0, 1, 0x7f80ff00aa55cc33, 0x8000000000000000, ^uint64(0)} {
		linked, err := machine.LinkRelocations(original, a.Relocations(), map[string]uint64{"value": value})
		if err != nil {
			t.Fatalf("link %#x: %v", value, err)
		}
		code, err := execution.NewCode(linked, execution.CodeOptions{ABI: abi.C}, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, callErr := code.Call()
		closeErr := code.Close()
		if callErr != nil || closeErr != nil || uint64(got) != value {
			t.Fatalf("symbol %#x returned %#x, call %v, close %v", value, got, callErr, closeErr)
		}
	}
	if !bytes.Equal(original, a.Bytes()) {
		t.Fatal("link mutated the unbound instruction sequence")
	}
}

func TestSymbolRelocationRejectsCorruptImmediate(t *testing.T) {
	a := riscv64.NewByteAssembler(abi.StackNoUse, 0)
	a.Symbol(10, "value")
	for _, offset := range []int{0, 8} {
		code := a.Bytes()
		word := binary.LittleEndian.Uint32(code[offset:])
		binary.LittleEndian.PutUint32(code[offset:], word|0x10000000)
		if _, err := machine.LinkRelocations(code, a.Relocations(), map[string]uint64{"value": 0x123456789abcdef0}); err == nil {
			t.Fatalf("corrupted immediate at byte %d was accepted", offset)
		}
	}
}
