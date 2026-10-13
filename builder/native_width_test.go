package builder

import (
	"strconv"
	"testing"
)

func TestNativeWidthValuesAndByteMemory(t *testing.T) {
	if NativeBits != uint8(strconv.IntSize) {
		t.Fatalf("native width = %d, want %d", NativeBits, strconv.IntSize)
	}
	paramBuilder := NewBuilder()
	param, err := paramBuilder.ParamNative()
	if err != nil || param.Bits() != NativeBits {
		t.Fatalf("native parameter width = %d, error = %v", param.Bits(), err)
	}
	b := NewBuilder()
	base, err := b.ConstNative(0x100)
	if err != nil {
		t.Fatal(err)
	}
	index, err := b.ConstNative(3)
	if err != nil {
		t.Fatal(err)
	}
	address, err := b.materialize(base.Add(index.MulConst(4)))
	if err != nil {
		t.Fatal(err)
	}
	if address.Bits() != NativeBits {
		t.Fatalf("address width = %d, want %d", address.Bits(), NativeBits)
	}
	value, err := b.LoadBits(address, 8)
	if err != nil {
		t.Fatal(err)
	}
	if value.Bits() != 8 || b.code[value.id].A != address.id {
		t.Fatal("byte load widened the native address")
	}
	converted, err := b.CastNative(value)
	if err != nil || converted.Bits() != NativeBits {
		t.Fatalf("native cast width = %d, error = %v", converted.Bits(), err)
	}
	if err := b.StoreBits(address, value); err != nil {
		t.Fatal(err)
	}
	store := b.code[len(b.code)-1]
	if store.Op != opStore8 || store.A != address.id || store.B != value.id {
		t.Fatal("byte store widened the native address or value")
	}
}
