//go:build linux && riscv64

package builder

import "testing"

func testWideParts(t *testing.T, b *Builder, value Value) (Value, Value) {
	t.Helper()
	low, err := b.ExtractBits(value, 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	high, err := b.ExtractBits(value, 64, 64)
	if err != nil {
		t.Fatal(err)
	}
	return low, high
}
