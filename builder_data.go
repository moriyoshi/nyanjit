package nyanjit

import (
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// Data views a scalar, struct, array or slice in host Go layout. The first
// construction error is reported by Builder.Finish. Roots and their At views
// work across blocks through an address Var. Index emits its own bounds branch;
// indexed views expire at calls/syscalls and cannot cross block boundaries.
// Take a fresh Index in the destination block. Reads are not cached.
type Data struct {
	inner   builder.Data
	builder *Builder
}

// ValueParam adds one native-width parameter for a read-only struct/array value
// supplied by execution.Words. Slice backing arrays remain writable.
func ValueParam[T any](b *Builder) Data {
	d := Data{builder: b}
	if b.ready() {
		var err error
		d.inner, err = builder.ValueParam[T](b.inner)
		b.fail(err)
	}
	return d
}

// PointerParam adds one native-width parameter for a writable *T, where T is
// a transferable struct or array. Nil pointers must not be accessed.
func PointerParam[T any](b *Builder) Data {
	d := Data{builder: b}
	if b.ready() {
		var err error
		d.inner, err = builder.PointerParam[T](b.inner)
		b.fail(err)
	}
	return d
}

// SliceParam adds one native-width parameter for a []E supplied by Words.
// Elements are writable, but slice headers cannot be written with Store.
func SliceParam[E any](b *Builder) Data {
	d := Data{builder: b}
	if b.ready() {
		var err error
		d.inner, err = builder.SliceParam[E](b.inner)
		b.fail(err)
	}
	return d
}

// At views a field or constant array element, e.g. "Rows[1].Cells" or "[2]".
// A slice ends a path; use Index for its elements.
func (d Data) At(path string) Data {
	child := Data{builder: d.builder}
	if d.builder.ready() {
		var err error
		child.inner, err = d.inner.At(path)
		d.builder.fail(err)
	}
	return child
}

// Field is At(path).Load(), a fresh scalar read at its declared field width.
func (d Data) Field(path string) Value {
	return takeValue(d.builder, func() (Value, error) { return d.inner.Field(path) })
}

// Load reads a scalar leaf. Signed integers are zero-extended at their width;
// use SignExtend for signed arithmetic. Floats use IEEE bit-pattern Values.
func (d Data) Load() Value {
	return takeValue(d.builder, func() (Value, error) { return d.inner.Load() })
}

// Store writes a matching-width scalar in a writable region. It cannot store
// structs, arrays or slice headers and therefore never stores Go pointers.
func (d Data) Store(value Value) { do(d.builder, func() error { return d.inner.Store(value) }) }

// Len returns an array's native-width constant length or freshly reads a
// slice's length. Keep it in a Var yourself if a snapshot is needed.
func (d Data) Len() Value {
	return takeValue(d.builder, func() (Value, error) { return d.inner.Len() })
}

// Index checks unsigned bounds and branches to outOfBounds on failure. It
// returns an element in a fresh in-bounds block, which earlier values dominate.
// Indexed views expire at calls/syscalls. Call Index again after callbacks.
func (d Data) Index(index Value, outOfBounds Label) Data {
	elem := Data{builder: d.builder}
	if !d.builder.ready() {
		return elem
	}
	inner, err := d.inner.Index(index, outOfBounds)
	if err != nil {
		d.builder.fail(err)
		return elem
	}
	elem.inner = inner
	return elem
}

// Addr returns the view's native address including its static offset. Raw
// stores bypass writability checks: writes to read-only regions are undefined,
// and Go pointers and slice headers must not be stored.
func (d Data) Addr() Value {
	return takeValue(d.builder, func() (Value, error) { return d.inner.Addr() })
}

// Base returns the underlying base without the static At offset. Indexed
// bases are borrows confined to this block, including their arithmetic aliases.
func (d Data) Base() Value {
	return takeValue(d.builder, func() (Value, error) {
		v := d.inner.Base()
		if !d.builder.inner.Valid(v) {
			return Value{}, compile.ErrInvalidFunction
		}
		return v, nil
	})
}

// Rebase replaces a block-local base, preserving type, offset and writability.
// Indexed views remain confined to the original checked block.
// Indexed views remain confined to their original block, even after Rebase.
// The replacement base must retain the original Go type's alignment.
func (d Data) Rebase(base Value) Data {
	if d.builder.ready() {
		if !d.builder.inner.Valid(base) || base.Type() != ir.Scalar(builder.NativeBits) {
			d.builder.fail(compile.ErrInvalidFunction)
		} else {
			d.inner = d.inner.Rebase(base)
		}
	}
	return d
}
