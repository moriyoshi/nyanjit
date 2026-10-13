package builder

import (
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func TestWordMemoryRecordsItsRepresentation(t *testing.T) {
	for _, width := range []uint8{64, 128} {
		b := NewBuilder()
		address, err := b.Const(0x100)
		if err != nil {
			t.Fatal(err)
		}
		value, err := b.LoadBits(address, width)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.StoreBits(address, value); err != nil {
			t.Fatal(err)
		}
		loads, stores := 0, 0
		for _, ins := range b.code {
			if ins.Op == opLoad64 || ins.Op == opStore64 {
				if ins.ByteOrder != ir.ByteOrderLittleEndian {
					t.Fatalf("%d-bit helper lost little-endian representation: %+v", width, ins)
				}
				if ins.Op == opLoad64 {
					loads++
				} else {
					stores++
				}
			}
		}
		if loads != int(width)/64 || stores != int(width)/64 {
			t.Fatalf("%d-bit accesses = loads %d, stores %d", width, loads, stores)
		}
	}
	b := NewBuilder()
	address, err := b.Const(0x100)
	if err != nil {
		t.Fatal(err)
	}
	value, err := b.Load64(address)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Store64(address, value); err != nil {
		t.Fatal(err)
	}
	for _, ins := range b.code {
		if ins.Op == opLoad64 || ins.Op == opStore64 {
			if ins.ByteOrder != ir.ByteOrderNative {
				t.Fatalf("native word operation has explicit order: %+v", ins)
			}
		}
	}
}
