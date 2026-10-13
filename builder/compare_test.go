package builder

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func TestCompareValueRecordsCondition(t *testing.T) {
	for _, width := range []uint8{8, 16, 32, 64} {
		b := NewBuilder()
		x, _ := b.ParamBits(width)
		y, _ := b.ParamBits(width)
		value, err := b.CompareValue(x, ir.BranchLessEqual, y)
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
		var compares, extends int
		for _, ins := range p.Instructions() {
			switch ins.Op {
			case ir.OpCompare:
				compares++
				if ins.Bits != width || ir.BranchCondition(ins.Imm) != ir.BranchLessEqual {
					t.Errorf("OpCompare Bits %d Imm %d", ins.Bits, ins.Imm)
				}
			case ir.OpSignExtend:
				extends++
			}
		}
		// Signed conditions compare at the operand width: no extension.
		if compares != 1 || extends != 0 {
			t.Errorf("width %d: %d OpCompare and %d OpSignExtend", width, compares, extends)
		}
	}
}

func TestCompareValueRejectsInvalidOperands(t *testing.T) {
	b := NewBuilder()
	x32, _ := b.ParamBits(32)
	x64, _ := b.ParamBits(64)
	if _, err := b.CompareValue(x32, ir.BranchLess, x64); !errors.Is(err, ErrInvalidFunction) {
		t.Errorf("mixed widths: %v", err)
	}
	if _, err := b.CompareValue(x64, ir.BranchAbove+1, x64); !errors.Is(err, ErrInvalidFunction) {
		t.Errorf("undefined condition: %v", err)
	}
}

func TestCompareValueWide(t *testing.T) {
	b := NewBuilder()
	lo, _ := b.Param()
	hi, _ := b.Param()
	x, _ := b.Concat(lo, hi)
	value, err := b.CompareValue(x, ir.BranchLess, x)
	if err != nil {
		t.Fatal(err)
	}
	if value.Type().LaneBits != 64 {
		t.Errorf("128-bit comparison has %d bits", value.Type().LaneBits)
	}
}

func TestPredicateValueShares(t *testing.T) {
	b := NewBuilder()
	x, _ := b.Param()
	y, _ := b.Param()
	shared, _ := b.Compare(x, ir.BranchBelow, y)
	p := shared.And(shared.Not()).Or(shared)
	value, err := b.PredicateValue(p, 32)
	if err != nil {
		t.Fatal(err)
	}
	if value.Type().LaneBits != 32 {
		t.Errorf("result has %d bits", value.Type().LaneBits)
	}
	if err := b.Return(value); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	compares := 0
	for _, ins := range program.Instructions() {
		if ins.Op == ir.OpCompare {
			compares++
		}
	}
	if compares != 1 {
		t.Errorf("shared comparison emitted %d times", compares)
	}
}

func TestPredicateValueRejects(t *testing.T) {
	b, other := NewBuilder(), NewBuilder()
	x, _ := b.Param()
	y, _ := other.Param()
	p, _ := b.IsZero(x)
	q, _ := other.IsZero(y)
	if _, err := b.PredicateValue(q, 64); !errors.Is(err, ErrInvalidFunction) {
		t.Errorf("another Builder's predicate: %v", err)
	}
	if _, err := b.PredicateValue(p, 12); !errors.Is(err, ErrInvalidFunction) {
		t.Errorf("width 12: %v", err)
	}
	one, err := b.PredicateValue(Predicate{}, 8)
	if err != nil {
		t.Fatal(err)
	}
	if imm, ok := b.instructionLiteral(one.id); !ok || imm != 1 || one.Type().LaneBits != 8 {
		t.Errorf("empty predicate: %d %v, %d bits", imm, ok, one.Type().LaneBits)
	}
}

func TestReverseBytesWidths(t *testing.T) {
	b := NewBuilder()
	for _, w := range []uint8{16, 32, 64} {
		x, _ := b.ParamBits(w)
		v, err := b.ReverseBytes(x)
		if err != nil || v.Type().LaneBits != w {
			t.Errorf("width %d: %v, %d bits", w, err, v.Type().LaneBits)
		}
	}
	x8, _ := b.ParamBits(8)
	if _, err := b.ReverseBytes(x8); !errors.Is(err, ErrInvalidFunction) {
		t.Errorf("8 bits: %v", err)
	}
	lo, _ := b.Param()
	wide, _ := b.Concat(lo, lo)
	if v, err := b.ReverseBytes(wide); err != nil || v.Type().LaneBits != 128 {
		t.Errorf("128 bits: %v, %d bits", err, v.Type().LaneBits)
	}
}

func TestOverflowRejects(t *testing.T) {
	b := NewBuilder()
	x, _ := b.Param()
	y, _ := b.Param()
	xor, _ := b.Xor(x, y)
	if _, err := b.Overflow(xor); !errors.Is(err, ErrInvalidFunction) {
		t.Errorf("Xor: %v", err)
	}
	if _, err := b.Overflow(x); !errors.Is(err, ErrInvalidFunction) {
		t.Errorf("parameter: %v", err)
	}
	sum, _ := b.Add(x, y)
	p, err := b.Overflow(sum)
	if err != nil || !p.node.hasAnchor || p.node.anchor != sum.id {
		t.Errorf("Add: %v", err)
	}
}
