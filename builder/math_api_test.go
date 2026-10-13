package builder

import (
	"errors"
	"testing"
)

func TestMathOperandsRequireMatchingFloatingWidths(t *testing.T) {
	b := NewBuilder()
	small, _ := b.ConstBits(8, 1)
	x, _ := b.ParamBits(64)
	y, _ := b.ParamBits(32)
	if _, err := b.FSqrt(small); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("sqrt byte: %v", err)
	}
	if _, err := b.FConvert(x, 16); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("convert to 16 bits: %v", err)
	}
	if _, err := b.FSin(small); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("sin byte: %v", err)
	}
	if _, err := b.FPow(x, y); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("mixed pow: %v", err)
	}
	other, _ := NewBuilder().ParamBits(64)
	if _, err := b.FCos(other); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("foreign cosine: %v", err)
	}
}
