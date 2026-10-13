package builder

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func TestVectorTypesTotalBitsAndDeferredShapes(t *testing.T) {
	b := NewBuilder()
	x, _ := b.Splat(ir.Vec(16, 32), mustScalar(t, b, 32))
	if x.Bits() != 512 || x.Type() != ir.Vec(16, 32) {
		t.Fatal("vector total width lost")
	}
	scalar, _ := b.ConstBits(128, 0)
	if scalar.Type() == ir.Vec(2, 64) || scalar.Type() != ir.Scalar(128) {
		t.Fatal("wide integer confused with vector")
	}
	if _, err := b.Add(x, mustVector(t, b, ir.Vec(8, 64))); !errors.Is(err, ErrInvalidFunction) {
		t.Fatal("equal total bits accepted with different lanes")
	}
	if err := b.Return(x); err == nil {
		t.Fatal("vector returned through scalar ABI")
	}
}
func mustScalar(t *testing.T, b *Builder, bits uint8) Value {
	t.Helper()
	v, err := b.ConstBits(bits, 1)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func mustVector(t *testing.T, b *Builder, typ ir.Type) Value {
	t.Helper()
	v, err := b.Splat(typ, mustScalar(t, b, typ.LaneBits))
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestTypedVectorLabelMismatchAndSnapshots(t *testing.T) {
	b := NewBuilder()
	x := mustVector(t, b, ir.Vec(4, 32))
	slot, err := b.Var(x)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Set(slot, mustVector(t, b, ir.Vec(2, 64))); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("vector Var accepted a different shape: %v", err)
	}
	b = NewBuilder()
	data := make([]byte, 16)
	data[0] = 42
	x, err = b.ConstVector(ir.Vec(16, 8), data)
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 0
	r, _ := b.ExtractLane(x, 0)
	b.Return(r)
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	if p.Instructions[0].Data[0] != 42 {
		t.Fatal("constant aliases input bytes")
	}
	copy := p.Clone()
	copy.Instructions[0].Data[0] = 7
	if p.Instructions[0].Data[0] != 42 {
		t.Fatal("Clone aliases vector bytes")
	}
	bad := p.Clone()
	bad.Instructions[0].Type = ir.Vec(8, 16)
	if err := bad.Validate(); err == nil {
		t.Fatal("changed lane type invalidates the extraction")
	}
}
