package builder

import (
	"fmt"
	"reflect"

	"github.com/moriyoshi/nyanjit/ir"
)

// maxStackBytes is the ir limit on one stack area and on their sum.
const maxStackBytes = 1 << 16

// StackData returns a writable Data view of a zeroed, function-lifetime T in
// the generated function's frame (or workspace). T must be a pointer-free
// scalar, struct or array; its Go size and alignment give the area's.
func StackData[T any](b *Builder) (Data, error) {
	return newStackData(b, reflect.TypeFor[T]())
}

func newStackData(b *Builder, t reflect.Type) (Data, error) {
	if b == nil || b.finished {
		return Data{}, fmt.Errorf("StackData: builder is nil or finished: %w", ErrInvalidFunction)
	}
	layout, invalid := cachedDataLayout(t)
	if invalid != nil {
		return Data{}, fmt.Errorf("StackData: field %q has unsupported type %s: %w", invalid.path, invalid.typ, ErrInvalidFunction)
	}
	if path, ok := stackPointerFree(t, ""); !ok {
		return Data{}, fmt.Errorf("StackData: type %s holds a slice at %q; stack areas must be pointer-free: %w", t, path, ErrInvalidFunction)
	}
	total := int(t.Size())
	for _, area := range b.stackAreas {
		total += area.Bytes
	}
	if t.Size() == 0 || total > maxStackBytes {
		return Data{}, fmt.Errorf("StackData: type %s needs %d bytes; stack areas need 1 to %d bytes in all: %w", t, t.Size(), maxStackBytes, ErrInvalidFunction)
	}
	b.stackAreas = append(b.stackAreas, ir.StackArea{Bytes: int(t.Size()), Align: t.Align()})
	return Data{builder: b, stack: len(b.stackAreas), layout: layout, writable: true}, nil
}

// stackPointerFree reports whether t, a valid Data layout type, holds no
// slice header, and otherwise the path of the first one.
func stackPointerFree(t reflect.Type, path string) (string, bool) {
	switch t.Kind() {
	case reflect.Slice:
		return path, false
	case reflect.Array:
		return stackPointerFree(t.Elem(), path+"[0]")
	case reflect.Struct:
		for i := range t.NumField() {
			f := t.Field(i)
			name := f.Name
			if path != "" {
				name = path + "." + name
			}
			if at, ok := stackPointerFree(f.Type, name); !ok {
				return at, false
			}
		}
	}
	return "", true
}

// stackAddress emits the address of stack area index.
func (b *Builder) stackAddress(index int) (Value, error) {
	if !b.canAppend() || index < 0 || index >= len(b.stackAreas) {
		return Value{}, ErrInvalidFunction
	}
	return b.appendScalar(ir.OpStackAddress, NativeBits, 0, 0, uint64(index), 0), nil
}
