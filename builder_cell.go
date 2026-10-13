package nyanjit

import "github.com/moriyoshi/nyanjit/builder"

// ValueCell returns a read-only Data view of *v in host Go layout. T may be a
// pointer-free scalar, struct or array (as StackData accepts). The address is
// compiled in.
//
// The view reads *v itself, not a copy: Go code must not write it while
// generated code may read it. ValueCell pins *v and registers its release
// with the program's Finalizers, which run when the last Code built from the
// program is closed (see execution.CodeOptions.Finalizers). A program that
// is never bound keeps the pin until it, and everything lowered from it,
// becomes unreachable. Store on the view is rejected. See builder.ValueCell.
func ValueCell[T any](b *Builder, v *T) Data {
	d := Data{builder: b}
	if b.ready() {
		var err error
		d.inner, err = builder.ValueCell(b.inner, v)
		b.fail(err)
	}
	return d
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
// valid index and nothing to pin. See builder.SliceCell.
func SliceCell[E any](b *Builder, s []E) Data {
	d := Data{builder: b}
	if b.ready() {
		var err error
		d.inner, err = builder.SliceCell(b.inner, s)
		b.fail(err)
	}
	return d
}
