package builder_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/builder"
)

func TestLoad8NativeAddress(t *testing.T) {
	b := builder.NewBuilder()
	address, err := b.ConstNative(0x10)
	if err != nil {
		t.Fatal(err)
	}
	value, err := b.Load8(address)
	if err != nil {
		t.Fatal(err)
	}
	if value.Type().LaneBits != 64 {
		t.Fatalf("Load8 type=%v", value.Type())
	}
	if err := b.Return(value); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(); err != nil {
		t.Fatal(err)
	}
}
