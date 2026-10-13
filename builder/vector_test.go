package builder

import (
	"errors"
	"github.com/moriyoshi/nyanjit/ir"
	"testing"
)

func TestVectorTypesTotalBitsAndDeferredShapes(t *testing.T) {
	b := NewBuilder()
	x, _ := b.Splat(Vec(16, 32), mustScalar(t, b, 32))
	if x.Bits() != 512 || x.Type() != Vec(16, 32) {
		t.Fatal("vector total width lost")
	}
	scalar, _ := b.ConstBits(128, 0)
	if scalar.Type() == Vec(2, 64) || scalar.Type() != Scalar(128) {
		t.Fatal("wide integer confused with vector")
	}
	if _, err := b.Add(x, mustVector(t, b, Vec(8, 64))); !errors.Is(err, ErrInvalidFunction) {
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
func mustVector(t *testing.T, b *Builder, typ Type) Value {
	t.Helper()
	v, err := b.Splat(typ, mustScalar(t, b, typ.LaneBits))
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestTypedVectorLabelMismatchAndSnapshots(t *testing.T) {
	b := NewBuilder()
	label := b.NewLabel()
	x := mustVector(t, b, Vec(4, 32))
	if err := b.JumpArgs(label, x); err != nil {
		t.Fatal(err)
	}
	params, err := b.BindParamsTypes(label, Vec(2, 64))
	if err != nil {
		t.Fatal(err)
	}
	r, err := b.ExtractLane(params[0], 0)
	if err != nil {
		t.Fatal(err)
	}
	b.Return(r)
	if _, err := b.Finish(); !errors.Is(err, ErrInvalidFunction) {
		t.Fatal("typed label accepted a different shape")
	}
	b = NewBuilder()
	data := make([]byte, 16)
	data[0] = 42
	x, err = b.ConstVector(Vec(16, 8), data)
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 0
	r, _ = b.ExtractLane(x, 0)
	b.Return(r)
	p, err := b.Finish()
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
