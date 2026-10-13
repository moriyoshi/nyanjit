package builder

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

type stackFixture struct {
	A    uint8
	B    [3]uint32
	C    struct{ D float64 }
	Flag bool
}

func TestStackDataTypes(t *testing.T) {
	b := NewBuilder()
	for _, c := range []struct {
		name string
		make func(*Builder) (Data, error)
		ok   bool
	}{
		{"uint64", StackData[uint64], true},
		{"array", StackData[[16]uint16], true},
		{"struct", StackData[stackFixture], true},
		{"uintptr", StackData[uintptr], true},
		{"slice", StackData[[]uint64], false},
		{"nested slice", StackData[struct{ X [2]struct{ S []uint8 } }], false},
		{"string", StackData[string], false},
		{"pointer", StackData[*uint64], false},
		{"empty", StackData[struct{}], false},
		{"over 64 KiB", StackData[[1<<16 + 1]byte], false},
	} {
		_, err := c.make(b)
		if c.ok && err != nil || !c.ok && !errors.Is(err, ErrInvalidFunction) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if _, err := StackData[uint64](nil); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("nil builder: %v", err)
	}
}

// Every view emits a fresh OpStackAddress, so a view works in any block
// and scope; Finish records the areas with the Go type's size and
// alignment.
func TestStackDataAcrossBlocks(t *testing.T) {
	for _, segment := range []bool{false, true} {
		var rec *SegmentRecorder
		b := NewBuilder()
		if segment {
			rec = NewSegmentRecorder()
			b = NewSegmentBuilder(rec.Emit)
		}
		x, _ := b.Param()
		first, err := StackData[uint64](b)
		if err != nil {
			t.Fatal(err)
		}
		second, _ := StackData[stackFixture](b)
		if err := first.Store(x); err != nil {
			t.Fatal(err)
		}
		next := b.NewLabel()
		if err := b.Jump(next); err != nil {
			t.Fatal(err)
		}
		if err := b.Bind(next); err != nil {
			t.Fatal(err)
		}
		if err := b.Enter(); err != nil {
			t.Fatal(err)
		}
		value, err := first.Load()
		if err != nil {
			t.Fatal(err)
		}
		field, err := second.At("C.D")
		if err != nil {
			t.Fatal(err)
		}
		if err := field.Store(value); err != nil {
			t.Fatal(err)
		}
		if field.Rebase(value).stack != 0 {
			t.Fatal("Rebase keeps the stack area")
		}
		if err := b.Leave(); err != nil {
			t.Fatal(err)
		}
		loaded, err := second.Field("B[2]")
		if err != nil {
			t.Fatal(err)
		}
		cast, _ := b.CastUnsigned(loaded, 64)
		sum, _ := b.Add(cast, x)
		if err := b.Return(sum); err != nil {
			t.Fatal(err)
		}
		var p *ir.SealedProgram
		if segment {
			if err := b.End(); err != nil {
				t.Fatal(err)
			}
			if p, err = rec.Seal(); err != nil {
				t.Fatal(err)
			}
			if len(rec.Segments[0].StackAreas) != 2 || len(rec.Segments[1].StackAreas) != 0 {
				t.Fatalf("segment areas: %v, %v", rec.Segments[0].StackAreas, rec.Segments[1].StackAreas)
			}
		} else if p, err = b.Finish(); err != nil {
			t.Fatal(err)
		}
		want := []ir.StackArea{{Bytes: 8, Align: 8}, {Bytes: 32, Align: 8}}
		if NativeBits == 32 {
			want[0] = ir.StackArea{Bytes: 8, Align: 4}
			want[1] = ir.StackArea{Bytes: 28, Align: 4}
		}
		areas := p.StackAreas()
		if len(areas) != 2 || areas[0] != want[0] || areas[1] != want[1] {
			t.Fatalf("segment %v: areas %v, want %v", segment, areas, want)
		}
		addresses := 0
		for _, ins := range p.Instructions() {
			if ins.Op == ir.OpStackAddress {
				addresses++
			}
		}
		if addresses != 4 {
			t.Fatalf("segment %v: %d stack addresses, want one per access", segment, addresses)
		}
	}
}
