package builder

import (
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend"
)

// ValueCell returns a read-only Data view of *v in host Go layout. T may be a
// pointer-free scalar, struct or array (as StackData accepts). The address is
// compiled in.
//
// The view reads *v itself, not a copy: Go code must not write it while
// generated code may read it. ValueCell pins *v and registers its release
// with the program's Finalizers (Program.Finalizers, or the Finalizers of the
// segment handed over next), which run when the last Code built from the
// program is closed (see execution.CodeOptions.Finalizers). A program that
// is never bound keeps the pin until it, and everything lowered from it,
// becomes unreachable. Store on the view is rejected.
func ValueCell[T any](b *Builder, v *T) (Data, error) {
	t := reflect.TypeFor[T]()
	if b == nil || b.finished {
		return Data{}, fmt.Errorf("ValueCell: builder is nil or finished: %w", ErrInvalidFunction)
	}
	if v == nil {
		return Data{}, fmt.Errorf("ValueCell: nil *%s: %w", t, ErrInvalidFunction)
	}
	layout, invalid := cachedDataLayout(t)
	if invalid != nil {
		return Data{}, fmt.Errorf("ValueCell: field %q has unsupported type %s: %w", invalid.path, invalid.typ, ErrInvalidFunction)
	}
	if path, ok := stackPointerFree(t, ""); !ok {
		return Data{}, fmt.Errorf("ValueCell: type %s holds a slice at %q; ValueCell needs a pointer-free type: %w", t, path, ErrInvalidFunction)
	}
	if t.Size() == 0 {
		return Data{}, fmt.Errorf("ValueCell: type %s has no bytes to read: %w", t, ErrInvalidFunction)
	}
	b.cellPins().pin(unsafe.Pointer(v))
	return Data{builder: b, layout: layout, cell: true, constant: true, address: uintptr(unsafe.Pointer(v))}, nil
}

// SliceCell returns a read-only Data view of s: Len is s's length (a native
// Value) and Index checks bounds against it, as for SliceParam. E as for
// SliceParam's element types.
//
// The view reads s's backing array, not a copy, and captures s's length:
// Go code must not write the elements, or replace the slices within them,
// while generated code may read them. Addr and Base give the address of the
// first element; there is no header. SliceCell pins the backing array and
// those of the slices within its elements, as ValueCell pins its value, and
// the views of those slices are read-only too. An empty s has length 0, no
// valid index and nothing to pin.
func SliceCell[E any](b *Builder, s []E) (Data, error) {
	t := reflect.TypeFor[[]E]()
	if b == nil || b.finished {
		return Data{}, fmt.Errorf("SliceCell: builder is nil or finished: %w", ErrInvalidFunction)
	}
	layout, invalid := cachedDataLayout(t)
	if invalid != nil {
		return Data{}, fmt.Errorf("SliceCell: field %q has unsupported type %s: %w", invalid.path, invalid.typ, ErrInvalidFunction)
	}
	d := Data{builder: b, layout: layout, cell: true, constant: true, cellSlice: true, cellLen: uintptr(len(s))}
	if len(s) != 0 {
		d.address = uintptr(unsafe.Pointer(unsafe.SliceData(s)))
		if t.Elem().Size() != 0 {
			pinCellSlices(b.cellPins(), reflect.ValueOf(s), make(map[uintptr]bool))
		}
	}
	return d, nil
}

// pinCellSlices pins the backing array of slice v and, through its
// elements, of every slice reachable from it. pinned holds the arrays
// already pinned, keyed by address.
func pinCellSlices(p *cellPin, v reflect.Value, pinned map[uintptr]bool) {
	if v.Len() == 0 || v.Type().Elem().Size() == 0 || pinned[v.Pointer()] {
		return
	}
	pinned[v.Pointer()] = true
	p.pin(v.UnsafePointer())
	if cellHoldsSlice(v.Type().Elem()) {
		for i := range v.Len() {
			pinCellElement(p, v.Index(i), pinned)
		}
	}
}

func pinCellElement(p *cellPin, v reflect.Value, pinned map[uintptr]bool) {
	switch v.Kind() {
	case reflect.Slice:
		pinCellSlices(p, v, pinned)
	case reflect.Array:
		if cellHoldsSlice(v.Type().Elem()) {
			for i := range v.Len() {
				pinCellElement(p, v.Index(i), pinned)
			}
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if cellHoldsSlice(v.Type().Field(i).Type) {
				pinCellElement(p, v.Field(i), pinned)
			}
		}
	}
}

// cellHoldsSlice reports whether t, a valid Data layout type, holds a slice.
func cellHoldsSlice(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Slice:
		return true
	case reflect.Array:
		return cellHoldsSlice(t.Elem())
	case reflect.Struct:
		for i := range t.NumField() {
			if cellHoldsSlice(t.Field(i).Type) {
				return true
			}
		}
	}
	return false
}

// cellSet is a Builder's cells: their pins and the Finalizer that releases
// them, which handed records a segment Builder has handed over.
type cellSet struct {
	pin        *cellPin
	finalizers backend.Finalizers
	handed     bool
}

// cellPin owns the pins of one Builder's cells. cellPinSet keeps every
// unreleased cellPin reachable, because a runtime.Pinner that is collected
// while it still pins memory panics. It must not reach its cellToken.
type cellPin struct {
	pinner runtime.Pinner
	// objects are the pinned objects, which a copy of the Builder pins
	// again with its own cellPin.
	objects  []unsafe.Pointer
	released atomic.Bool
}

var cellPinSet sync.Map // *cellPin -> struct{}

func (p *cellPin) pin(object unsafe.Pointer) {
	p.pinner.Pin(object)
	p.objects = append(p.objects, object)
}

// release unpins once; Finalize and the collection of an unused program
// may both call it.
func (p *cellPin) release() {
	if p.released.Swap(true) {
		return
	}
	p.pinner.Unpin()
	cellPinSet.Delete(p)
}

// cellToken is the program's Finalizer for a cellPin. When no program, Unit,
// Template or Code holds it any more, no generated code can read the pinned
// memory, so its cleanup releases the pins of a program that was never
// bound or whose Codes were not all closed.
type cellToken struct{ pin *cellPin }

func (t *cellToken) release() { t.pin.release() }

func newCellSet() *cellSet {
	p := new(cellPin)
	cellPinSet.Store(p, struct{}{})
	token := &cellToken{pin: p}
	runtime.AddCleanup(token, (*cellPin).release, p)
	return &cellSet{pin: p, finalizers: backend.Finalizers{backend.FinalizerFunc(token.release)}}
}

// cellPins returns the Builder's cellPin, creating it, and so its
// Finalizer, on first use.
func (b *Builder) cellPins() *cellPin {
	if b.cells == nil {
		b.cells = newCellSet()
	}
	return b.cells.pin
}

// detachCells gives a copy of a Builder its own pins of the same objects
// and its own Finalizer, so that neither releases the other's pins.
func (b *Builder) detachCells() {
	if b.cells == nil {
		return
	}
	cells := newCellSet()
	for _, object := range b.cells.pin.objects {
		cells.pin.pin(object)
	}
	b.cells = cells
}

// segmentFinalizers returns the Finalizers a segment Builder has not handed
// over yet.
func (b *Builder) segmentFinalizers() backend.Finalizers {
	if b.cells == nil || b.cells.handed {
		return nil
	}
	b.cells.handed = true
	return b.cells.finalizers
}
