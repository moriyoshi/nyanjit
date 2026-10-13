package nyanjit_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestCellRejectsStore(t *testing.T) {
	type nested struct{ Values []uint32 }
	value := new(struct{ X uint32 })
	for name, store := range map[string]func(b *nyanjit.Builder){
		"value": func(b *nyanjit.Builder) { nyanjit.ValueCell(b, value).At("X").Store(b.ConstBits(32, 1)) },
		"element": func(b *nyanjit.Builder) {
			nyanjit.SliceCell(b, []uint32{1}).Index(b.ConstNative(0), b.NewLabel()).Store(b.ConstBits(32, 1))
		},
		"nested element": func(b *nyanjit.Builder) {
			outer := nyanjit.SliceCell(b, []nested{{Values: []uint32{1}}}).Index(b.ConstNative(0), b.NewLabel())
			outer.At("Values").Index(b.ConstNative(0), b.NewLabel()).Store(b.ConstBits(32, 1))
		},
	} {
		b := nyanjit.NewBuilder()
		store(b)
		b.Return(b.Const(0))
		if _, err := b.Finish(); !errors.Is(err, compile.ErrInvalidFunction) {
			t.Errorf("%s: Finish = %v", name, err)
		}
	}
}

func TestCellRejectsTypes(t *testing.T) {
	for name, capture := range map[string]func(b *nyanjit.Builder){
		"nil":        func(b *nyanjit.Builder) { nyanjit.ValueCell[uint32](b, nil) },
		"slice":      func(b *nyanjit.Builder) { nyanjit.ValueCell(b, &struct{ S []int }{}) },
		"zero size":  func(b *nyanjit.Builder) { nyanjit.ValueCell(b, &struct{}{}) },
		"pointer":    func(b *nyanjit.Builder) { nyanjit.ValueCell(b, &struct{ P *int }{}) },
		"string":     func(b *nyanjit.Builder) { nyanjit.SliceCell(b, []string{"x"}) },
		"nested ptr": func(b *nyanjit.Builder) { nyanjit.SliceCell(b, []struct{ S [][]*int }{}) },
	} {
		b := nyanjit.NewBuilder()
		capture(b)
		b.Return(b.Const(0))
		if _, err := b.Finish(); !errors.Is(err, compile.ErrInvalidFunction) {
			t.Errorf("%s: Finish = %v", name, err)
		}
	}
}

func TestCellViewErrors(t *testing.T) {
	value := new([2]uint32)
	for name, use := range map[string]func(b *nyanjit.Builder){
		"field of array": func(b *nyanjit.Builder) { nyanjit.ValueCell(b, value).Field("X") },
		"out of range":   func(b *nyanjit.Builder) { nyanjit.ValueCell(b, value).Field("[2]") },
		"aggregate load": func(b *nyanjit.Builder) { nyanjit.ValueCell(b, value).Load() },
		"scalar index":   func(b *nyanjit.Builder) { nyanjit.SliceCell(b, []uint8{1}).At("[0]") },
		"invalid index":  func(b *nyanjit.Builder) { nyanjit.SliceCell(b, []uint8{1}).Index(nyanjit.Value{}, b.NewLabel()) },
	} {
		b := nyanjit.NewBuilder()
		use(b)
		b.Return(b.Const(0))
		if _, err := b.Finish(); !errors.Is(err, compile.ErrInvalidFunction) {
			t.Errorf("%s: Finish = %v", name, err)
		}
	}
}
