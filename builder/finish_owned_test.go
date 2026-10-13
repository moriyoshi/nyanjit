package builder_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestFinishOwnershipOfVectorAndControlStorage(t *testing.T) {
	b := builder.NewBuilder()
	label := b.NewLabel()
	data := make([]byte, 16)
	data[0] = 42
	vector, err := b.ConstVector(ir.Vec(4, 32), data)
	if err != nil {
		t.Fatal(err)
	}
	slot, err := b.Var(vector)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Jump(label); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(label); err != nil {
		t.Fatal(err)
	}
	old, err := b.Get(slot)
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.ExtractLane(old, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(result); err != nil {
		t.Fatal(err)
	}
	sealed, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	original := sealed.Clone()
	data[0] = 0
	changed := sealed.Clone()
	changed.Instructions[0].Data[0] = 9
	changed.Variables[0].Type = ir.Scalar(16)
	changed.Variables[0].Initial[0] = -1
	changed.Control.Labels[0] = false
	for i := range changed.Control.Steps {
		changed.Control.Steps[i].End = -1
	}
	if !reflect.DeepEqual(sealed.Clone(), original) {
		t.Fatal("sealed output aliases caller input or a mutable Clone")
	}
	if b.Valid(old) {
		t.Fatal("finished builder still accepts its values")
	}
	if _, err := b.Const(7); !errors.Is(err, builder.ErrInvalidFunction) {
		t.Fatalf("finished builder accepted append: %v", err)
	}
	other := builder.NewBuilder()
	value, err := other.Param()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Add(value, old); !errors.Is(err, builder.ErrInvalidFunction) {
		t.Fatalf("finished value accepted by another builder: %v", err)
	}
}

// Pure Value composition previously shared no mutable node allocator. Keep
// that use safe when independent expression trees share a builder's slabs.
func TestConcurrentPureValueComposition(t *testing.T) {
	b := builder.NewBuilder()
	root, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	values := make(chan builder.Value, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := root.Xor(root)
			for range 128 {
				v = v.Add(root)
			}
			values <- v
		}()
	}
	wg.Wait()
	close(values)
	result := root
	for v := range values {
		if !b.Valid(v) {
			t.Fatal("concurrently composed value is invalid")
		}
		result = result.Xor(v)
	}
	if err := b.Return(result); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestBuilderCopiesCannotChangeFinishedSnapshots(t *testing.T) {
	b := builder.NewBuilder()
	label := b.NewLabel()
	value, err := b.Const(7)
	if err != nil {
		t.Fatal(err)
	}
	carried, err := b.Var(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Jump(label); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(label); err != nil {
		t.Fatal(err)
	}
	values, err := b.Get(carried)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(values); err != nil {
		t.Fatal(err)
	}
	copyBuilder := *b
	fromCopy, err := copyBuilder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sealed.Clone(), fromCopy.Clone()) {
		t.Fatal("copy Finish changed the graph")
	}
	// A copy made before Finish can still share unused backing capacity. NewLabel
	// must not overwrite the sealed graph's implicit entry label in that capacity.
	other := builder.NewBuilder()
	next := other.NewLabel()
	v, err := other.Const(3)
	if err != nil {
		t.Fatal(err)
	}
	carried, err = other.Var(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Jump(next); err != nil {
		t.Fatal(err)
	}
	if err := other.Bind(next); err != nil {
		t.Fatal(err)
	}
	params, err := other.Get(carried)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Return(params); err != nil {
		t.Fatal(err)
	}
	pending := *other
	output, err := other.Finish()
	if err != nil {
		t.Fatal(err)
	}
	original := output.Clone()
	if pending.NewLabel() == (builder.Label{}) {
		t.Fatal("copy lost its original state")
	}
	if !reflect.DeepEqual(output.Clone(), original) {
		t.Fatal("copy mutation changed sealed output")
	}
}
