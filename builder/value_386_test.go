//go:build linux && 386

package builder

import "testing"

func TestWideValueSelectsFour32BitWords(t *testing.T) {
	b := NewBuilder()
	input, err := b.ParamBits(128)
	if err != nil {
		t.Fatal(err)
	}
	if input.Bits() != 128 || len(input.components()) != 2 || b.arity != 2 {
		t.Fatalf("128-bit parameter: width=%d components=%d arity=%d", input.Bits(), len(input.components()), b.arity)
	}
	one, err := b.ConstBits(128, 1)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := b.Add(input, one)
	if err != nil {
		t.Fatal(err)
	}
	high, err := b.ExtractBits(sum, 64, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(high); err != nil {
		t.Fatal(err)
	}
	program, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(program.Instructions) == 0 {
		t.Fatal("empty 128-bit program")
	}
}

func TestWideMemoryAccepts32BitAddress(t *testing.T) {
	b := NewBuilder()
	address, err := b.ParamBits(32)
	if err != nil {
		t.Fatal(err)
	}
	value, err := b.ParamBits(128)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.StoreBits(address, value); err != nil {
		t.Fatal(err)
	}
	loaded, err := b.LoadBits(address, 128)
	if err != nil {
		t.Fatal(err)
	}
	high, err := b.ExtractBits(loaded, 64, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(high); err != nil {
		t.Fatal(err)
	}
	if _, err := testFinishRaw(b); err != nil {
		t.Fatal(err)
	}
}
