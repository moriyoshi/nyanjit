package builder

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/regalloc"
)

func TestSplitIntegerWords(t *testing.T) {
	const value = uint64(0xfedcba9876543210)
	parts, err := regalloc.SplitIntegerWords(value, 32, 2)
	if err != nil || parts != [2]uint64{0x76543210, 0xfedcba98} {
		t.Fatalf("32-bit pair = %#v, %v", parts, err)
	}
	parts, err = regalloc.SplitIntegerWords(value, 64, 1)
	if err != nil || parts != [2]uint64{value, 0} {
		t.Fatalf("64-bit single = %#v, %v", parts, err)
	}
	if _, err := regalloc.SplitIntegerWords(value, 64, 2); !errors.Is(err, ErrBackendLimit) {
		t.Fatalf("invalid 64-bit pair = %v", err)
	}
}

func TestIntegerRegisterWords(t *testing.T) {
	for _, tc := range []struct {
		bits, registerBits uint8
		want               uint8
	}{
		{8, 32, 1}, {32, 32, 1}, {64, 32, 2},
		{8, 64, 1}, {64, 64, 1}, {128, 64, 2},
	} {
		got, err := regalloc.IntegerRegisterWords(tc.bits, tc.registerBits)
		if err != nil || got != tc.want {
			t.Fatalf("%d bits / %d-bit register = %d, %v; want %d", tc.bits, tc.registerBits, got, err, tc.want)
		}
	}
	for _, bits := range []uint8{0, 96, 128} {
		if _, err := regalloc.IntegerRegisterWords(bits, 32); !errors.Is(err, ErrBackendLimit) {
			t.Fatalf("%d bits / 32-bit register = %v, want ErrBackendLimit", bits, err)
		}
	}
}
