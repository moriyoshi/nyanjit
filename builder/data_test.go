package builder

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

type structLayoutFixture struct {
	Prefix uint32
	Wide   int64
	Inner  struct {
		Flag bool
		N    int16
		F32  float32
		F64  float64
	}
	Rows [2]struct{ Cells [4]uint16 }
	Grid [2][3]uint8
}

func TestDataHostLayoutAndViews(t *testing.T) {
	var v structLayoutFixture
	b := NewBuilder()
	s, err := PointerParam[structLayoutFixture](b)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path   string
		offset uintptr
		bits   uint16
	}{
		{"Prefix", unsafe.Offsetof(v.Prefix), 32},
		{"Wide", unsafe.Offsetof(v.Wide), 64},
		{"Inner.Flag", unsafe.Offsetof(v.Inner) + unsafe.Offsetof(v.Inner.Flag), 8},
		{"Inner.N", unsafe.Offsetof(v.Inner) + unsafe.Offsetof(v.Inner.N), 16},
		{"Inner.F32", unsafe.Offsetof(v.Inner) + unsafe.Offsetof(v.Inner.F32), 32},
		{"Inner.F64", unsafe.Offsetof(v.Inner) + unsafe.Offsetof(v.Inner.F64), 64},
		{"Rows[1].Cells[3]", unsafe.Offsetof(v.Rows) + unsafe.Sizeof(v.Rows[0]) + 3*unsafe.Sizeof(v.Rows[0].Cells[0]), 16},
		{"Grid[1][2]", unsafe.Offsetof(v.Grid) + unsafe.Sizeof(v.Grid[0]) + 2, 8},
	}
	for _, tt := range tests {
		leaf, err := s.At(tt.path)
		l, offset := leaf.layout, leaf.offset
		if err != nil || offset != tt.offset || uint16(l.bits) != tt.bits {
			t.Fatalf("%s: offset %d, layout %+v, err %v; want %d/%d", tt.path, offset, l, err, tt.offset, tt.bits)
		}
		value, err := s.Field(tt.path)
		if err != nil || value.Bits() != tt.bits {
			t.Fatalf("%s: width %d, err %v", tt.path, value.Bits(), err)
		}
	}
	wantWide := uintptr(8)
	if NativeBits == 32 {
		wantWide = 4
	}
	if unsafe.Offsetof(v.Wide) != wantWide {
		t.Fatalf("architecture-dependent Wide offset %d, want %d", unsafe.Offsetof(v.Wide), wantWide)
	}
	child, err := s.At("Rows[1]")
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := child.At("Cells[3]")
	offset := leaf.offset
	if err != nil || offset != tests[6].offset || !child.writable {
		t.Fatalf("nested view: offset %d, err %v", offset, err)
	}
	cells, _ := child.At("Cells")
	if address, err := cells.Addr(); err != nil || address.Bits() != uint16(NativeBits) {
		t.Fatalf("aggregate Addr: %v, width %d", err, address.Bits())
	}
	a, err := s.Field("Wide")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Field("Wide")
	if err != nil || a.id == c.id {
		t.Fatal("Field reused an earlier load", err)
	}
	if err := b.Return(c); err != nil {
		t.Fatal(err)
	}
	p, err := b.Finish()
	if err != nil || p.Arity() != 1 {
		t.Fatalf("Finish: %v", err)
	}
}

func TestDataRejectedTypesNameFieldPath(t *testing.T) {
	bad := []reflect.Type{
		reflect.TypeFor[*int](), reflect.TypeFor[unsafe.Pointer](), reflect.TypeFor[string](),
		reflect.TypeFor[map[int]int](), reflect.TypeFor[any](),
		reflect.TypeFor[chan int](), reflect.TypeFor[func()](),
		reflect.TypeFor[complex64](), reflect.TypeFor[complex128](),
	}
	for _, typ := range bad {
		t.Run(typ.String(), func(t *testing.T) {
			typ = reflect.StructOf([]reflect.StructField{{Name: "Bad", Type: typ}})
			typ = reflect.StructOf([]reflect.StructField{{Name: "Items", Type: reflect.ArrayOf(2, typ)}})
			typ = reflect.StructOf([]reflect.StructField{{Name: "Outer", Type: typ}})
			_, invalid := cachedDataLayout(typ)
			if invalid == nil || invalid.path != "Outer.Items[0].Bad" {
				t.Fatalf("layout error %+v", invalid)
			}
		})
	}
	b := NewBuilder()
	_, err := ValueParam[struct{ Outer [2]struct{ Bad *int } }](b)
	if !errors.Is(err, ErrInvalidFunction) || !strings.Contains(err.Error(), "Outer[0].Bad") || b.arity != 0 {
		t.Fatalf("invalid layout: %v, arity %d", err, b.arity)
	}
	if _, err := ValueParam[int](b); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("non-struct T: %v", err)
	}
	if _, err := PointerParam[*struct{}](b); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("pointer T: %v", err)
	}
	if _, err := ValueParam[struct{}](nil); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("nil builder: %v", err)
	}
	// Transferability is checked by element type, including zero-length arrays.
	if _, err := ValueParam[struct{ Empty [0]*int }](b); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("pointer element: %v", err)
	}
	empty, err := ValueParam[[0]uint32](b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := empty.Field("[0]"); !errors.Is(err, ErrInvalidFunction) {
		t.Fatal(err)
	}

}

func TestDataPathsAndMutabilityErrors(t *testing.T) {
	b := NewBuilder()
	s, err := ValueParam[structLayoutFixture](b)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", ".", "Inner.", "Inner..N", "Missing", "Inner.Missing", "Wide.X", "Wide[0]", "Rows[]", "Rows[-1]", "Rows[+1]", "Rows[2]", "Rows[999999999999999999999999]", "Rows[1", "Rows[0]X", "Rows.[0]", "Rows[0]]", "Rows", "Inner"} {
		if _, err := s.Field(path); !errors.Is(err, ErrInvalidFunction) {
			t.Errorf("Field(%q): %v", path, err)
		}
	}
	leaf, err := s.At("Wide")
	if err != nil {
		t.Fatal(err)
	}
	x, _ := b.Const(3)
	if _, err := leaf.Addr(); err != nil {
		t.Fatal("read-only Addr is allowed", err)
	}
	if err := leaf.Store(x); !errors.Is(err, ErrInvalidFunction) {
		t.Fatal("read-only Store", err)
	}
	child, _ := s.At("Inner.N")
	narrow, _ := b.ConstBits(16, 1)
	if err := child.Store(narrow); !errors.Is(err, ErrInvalidFunction) {
		t.Fatal("read-only child Store", err)
	}
	w, err := PointerParam[structLayoutFixture](b)
	if err != nil {
		t.Fatal(err)
	}
	prefix, _ := w.At("Prefix")
	if err := prefix.Store(x); !errors.Is(err, ErrInvalidFunction) {
		t.Fatal("Store width mismatch", err)
	}
	rows, _ := w.At("Rows")
	if err := rows.Store(x); !errors.Is(err, ErrInvalidFunction) {
		t.Fatal("Store aggregate", err)
	}
	foreign, _ := NewBuilder().Const(7)
	wide, _ := w.At("Wide")
	if err := wide.Store(foreign); !errors.Is(err, ErrInvalidFunction) {
		t.Fatal("Store foreign builder", err)
	}
	if _, err := wide.Index(x, b.NewLabel()); !errors.Is(err, ErrInvalidFunction) {
		t.Fatal("Index scalar", err)
	}
	if _, err := rows.Index(foreign, b.NewLabel()); !errors.Is(err, ErrInvalidFunction) {
		t.Fatal("Index foreign builder", err)
	}
	two, _ := b.Literal(2)
	if err := wide.Store(x.Add(two)); err != nil {
		t.Fatal("Store deferred expression", err)
	}
	if err := b.Return(x); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Field("Wide"); !errors.Is(err, ErrInvalidFunction) {
		t.Fatal("view after Return", err)
	}
}

func TestDataLayoutCacheConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			b := NewBuilder()
			s, err := ValueParam[structLayoutFixture](b)
			if err != nil {
				t.Error(err)
				return
			}
			layout, invalid := cachedDataLayout(reflect.TypeFor[structLayoutFixture]())
			if invalid != nil || s.layout != layout {
				t.Error("layout cache not shared")
			}
		})
	}
	wg.Wait()
}

func TestDataArraysSlicesAndRecursiveLayouts(t *testing.T) {
	type node struct {
		Children []node
		ID       uint32
	}
	type args struct {
		Count  uint32
		Items  []node
		Nested [2][][3]uint16
	}
	b := NewBuilder()
	root, err := ValueParam[args](b)
	if err != nil {
		t.Fatal(err)
	}
	items, err := root.At("Items")
	if err != nil {
		t.Fatal(err)
	}
	var v args
	if items.offset != unsafe.Offsetof(v.Items) || items.layout.typ.Size() != unsafe.Sizeof(v.Items) {
		t.Fatal("slice layout differs from Go")
	}
	if items.layout.elem.fields["Children"].layout.elem != items.layout.elem {
		t.Fatal("recursive layout not shared")
	}
	for _, path := range []string{"Items[0]", "Items.ID", "Nested[0][0]"} {
		if _, err := root.At(path); !errors.Is(err, ErrInvalidFunction) {
			t.Errorf("%s: %v", path, err)
		}
	}
	if _, err := items.Load(); !errors.Is(err, ErrInvalidFunction) {
		t.Fatal("header Load", err)
	}
	array, err := PointerParam[[2]struct{ ID uint32 }](b)
	if err != nil {
		t.Fatal(err)
	}
	zero, _ := b.ConstNative(0)
	elem, err := items.Index(zero, b.NewLabel())
	if err != nil {
		t.Fatal(err)
	}
	id, err := elem.At("ID")
	if err != nil {
		t.Fatal(err)
	}
	one, _ := b.ConstBits(32, 1)
	if err := id.Store(one); err != nil {
		t.Fatal("slice backing array should be writable", err)
	}
	if err := items.Store(one); !errors.Is(err, ErrInvalidFunction) {
		t.Fatal("header Store", err)
	}
	count, _ := root.At("Count")
	if err := count.Store(one); !errors.Is(err, ErrInvalidFunction) {
		t.Fatal("value field Store", err)
	}

	leaf, err := array.At("[1].ID")
	if err != nil || leaf.offset != 4 {
		t.Fatalf("root array view: %+v, %v", leaf, err)
	}
	if err := leaf.Store(one); err != nil {
		t.Fatal(err)
	}
	if _, err := SliceParam[*int](b); !errors.Is(err, ErrInvalidFunction) {
		t.Fatal("slice pointer element", err)
	}
}

func TestDataRootAcrossBlocksAndLocalRebase(t *testing.T) {
	b := NewBuilder()
	root, err := ValueParam[struct {
		Items []struct {
			Pad uint32
			ID  uint32
		}
	}](b)
	if err != nil {
		t.Fatal(err)
	}
	slice, err := root.At("Items")
	if err != nil {
		t.Fatal(err)
	}
	index, _ := b.ConstNative(0)
	invalid := b.NewLabel()
	elem, err := slice.Index(index, invalid)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := elem.At("ID")
	if err != nil {
		t.Fatal(err)
	}
	rebound := leaf.Rebase(leaf.Base())
	if rebound.offset != 4 {
		t.Fatal("Rebase lost static offset")
	}
	if _, err := rebound.Load(); err != nil {
		t.Fatal(err)
	}
	next := b.NewLabel()
	if err := b.Jump(next); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(next); err != nil {
		t.Fatal(err)
	}
	base, _ := b.ConstNative(0)
	if _, err := leaf.Rebase(base).Load(); !errors.Is(err, ErrInvalidFunction) {
		t.Fatal("indexed Rebase crossed a block", err)
	}
	if _, err := slice.Len(); err != nil {
		t.Fatal("root did not survive block", err)
	}
	index, _ = b.ConstNative(0)
	fresh, err := slice.Index(index, invalid)
	if err != nil {
		t.Fatal(err)
	}
	freshLeaf, err := fresh.At("ID")
	if err != nil {
		t.Fatal(err)
	}
	value, err := freshLeaf.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(value); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(invalid); err != nil {
		t.Fatal(err)
	}
	zero, _ := b.ConstBits(32, 0)
	if err := b.Return(zero); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestDataIndexedViewCannotCrossBlockInsideNestedScope(t *testing.T) {
	b := NewBuilder()
	root, err := ValueParam[[1]uint32](b)
	if err != nil {
		t.Fatal(err)
	}
	zero, _ := b.ConstNative(0)
	elem, err := root.Index(zero, b.NewLabel())
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Enter(); err != nil {
		t.Fatal(err)
	}
	next := b.NewLabel()
	if err := b.Jump(next); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(next); err != nil {
		t.Fatal(err)
	}
	// Indexed address borrows do not participate in implicit scope capture.
	if b.Valid(elem.local) {
		t.Fatal("indexed base crossed a block through capture")
	}
	if _, err := elem.Load(); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("indexed view crossed a block without Rebase: %v", err)
	}
}
