package builder

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func TestMulHighRecordsWidth(t *testing.T) {
	for _, width := range []uint8{8, 16, 32, 64} {
		b := NewBuilder()
		x, _ := b.ParamBits(width)
		y, _ := b.ParamBits(width)
		unsigned, err := b.MulHigh(x, y)
		if err != nil {
			t.Fatal(err)
		}
		signed, err := b.MulHighSigned(x, y)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Return(b.mustXor(t, unsigned, signed)); err != nil {
			t.Fatal(err)
		}
		p, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		var got []ir.Opcode
		for _, ins := range p.Instructions() {
			if ins.Op == ir.OpMulHigh || ins.Op == ir.OpMulHighSigned {
				if ins.Bits != width {
					t.Errorf("%v Bits %d, want %d", ins.Op, ins.Bits, width)
				}
				got = append(got, ins.Op)
			}
		}
		if len(got) != 2 || got[0] != ir.OpMulHigh || got[1] != ir.OpMulHighSigned {
			t.Errorf("width %d: high multiplies %v", width, got)
		}
	}
}

func TestMulHighRejectsInvalidOperands(t *testing.T) {
	b := NewBuilder()
	x, _ := b.ParamBits(32)
	y, _ := b.ParamBits(64)
	if _, err := b.MulHighSigned(x, y); !errors.Is(err, ErrInvalidFunction) {
		t.Errorf("mixed widths: %v", err)
	}
	hi, _ := b.ParamBits(64)
	wide, err := b.Concat(y, hi)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.MulHigh(wide, wide); !errors.Is(err, ErrInvalidFunction) {
		t.Errorf("128-bit high multiply: %v", err)
	}
}
