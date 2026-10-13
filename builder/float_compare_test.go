package builder

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func TestFCompareRecordsCondition(t *testing.T) {
	for _, width := range []uint8{32, 64} {
		b := NewBuilder()
		x, _ := b.ParamBits(width)
		y, _ := b.ParamBits(width)
		value, err := b.FCompare(x, ir.FloatUnorderedLess, y)
		if err != nil {
			t.Fatal(err)
		}
		if value.Type().LaneBits != width {
			t.Errorf("width %d: result has %d bits", width, value.Type().LaneBits)
		}
		if err := b.Return(value); err != nil {
			t.Fatal(err)
		}
		p, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, ins := range p.Instructions() {
			if ins.Op == ir.OpFCompare {
				found = true
				if ins.Bits != width || ir.FloatCondition(ins.Imm) != ir.FloatUnorderedLess {
					t.Errorf("FCompare Bits %d Imm %d", ins.Bits, ins.Imm)
				}
			}
		}
		if !found {
			t.Errorf("width %d: no OpFCompare", width)
		}
	}
}

func TestFCompareRejectsInvalidOperands(t *testing.T) {
	b := NewBuilder()
	x32, _ := b.ParamBits(32)
	x64, _ := b.ParamBits(64)
	x16, _ := b.ParamBits(16)
	cases := []struct {
		name      string
		x, y      Value
		condition ir.FloatCondition
	}{
		{"mixed widths", x32, x64, ir.FloatEqual},
		{"16 bits", x16, x16, ir.FloatEqual},
		{"undefined condition", x64, x64, ir.FloatUnorderedEqual + 1},
	}
	for _, c := range cases {
		if _, err := b.FCompare(c.x, c.condition, c.y); !errors.Is(err, ErrInvalidFunction) {
			t.Errorf("%s: %v", c.name, err)
		}
		if _, err := b.FComparePredicate(c.x, c.condition, c.y); !errors.Is(err, ErrInvalidFunction) {
			t.Errorf("%s predicate: %v", c.name, err)
		}
	}
	wide, _ := b.ParamBits(128)
	if _, err := b.FCompare(wide, ir.FloatEqual, wide); !errors.Is(err, ErrInvalidFunction) {
		t.Errorf("128 bits: %v", err)
	}
}
