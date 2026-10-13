package builder

import (
	"reflect"
	"strings"
	"sync"
)

type dataField struct {
	offset uintptr
	layout *dataLayout
}
type dataLayout struct {
	typ    reflect.Type
	fields map[string]dataField
	elem   *dataLayout
	bits   uint8
}
type dataLayoutError struct {
	path string
	typ  reflect.Type
}
type dataLayoutEntry struct {
	layout *dataLayout
	err    *dataLayoutError
}

var dataLayouts sync.Map // reflect.Type -> dataLayoutEntry
var dataLayoutMu sync.Mutex

func cachedDataLayout(t reflect.Type) (*dataLayout, *dataLayoutError) {
	if cached, ok := dataLayouts.Load(t); ok {
		e := cached.(dataLayoutEntry)
		return e.layout, e.err
	}
	dataLayoutMu.Lock()
	defer dataLayoutMu.Unlock()
	if cached, ok := dataLayouts.Load(t); ok {
		e := cached.(dataLayoutEntry)
		return e.layout, e.err
	}
	// Slice types can refer back to their enclosing type. Publish descriptors
	// only after this entire private graph has been initialized and validated.
	local := make(map[reflect.Type]*dataLayout)
	l, invalid := makeDataLayout(t, local)
	if invalid != nil {
		dataLayouts.Store(t, dataLayoutEntry{err: invalid})
		return nil, invalid
	}
	for typ, layout := range local {
		dataLayouts.Store(typ, dataLayoutEntry{layout: layout})
	}
	return l, nil
}
func layoutChildError(prefix string, invalid *dataLayoutError) *dataLayoutError {
	if invalid.path != "" && !strings.HasPrefix(invalid.path, "[") {
		prefix += "."
	}
	return &dataLayoutError{path: prefix + invalid.path, typ: invalid.typ}
}
func makeDataLayout(t reflect.Type, local map[reflect.Type]*dataLayout) (*dataLayout, *dataLayoutError) {
	if l := local[t]; l != nil {
		return l, nil
	}
	if cached, ok := dataLayouts.Load(t); ok {
		e := cached.(dataLayoutEntry)
		if e.err == nil {
			return e.layout, nil
		}
		// A recursive error path depends on the traversal's starting type.
		// Only root lookups may reuse cached errors; rescan invalid children
		// against this walk's visited types to preserve the first field path.
	}
	l := &dataLayout{typ: t}
	local[t] = l
	switch t.Kind() {
	case reflect.Bool:
		l.bits = 8
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64:
		l.bits = uint8(8 * t.Size())
	case reflect.Array, reflect.Slice:
		var invalid *dataLayoutError
		l.elem, invalid = makeDataLayout(t.Elem(), local)
		if invalid != nil {
			return nil, layoutChildError("[0]", invalid)
		}
	case reflect.Struct:
		l.fields = make(map[string]dataField, t.NumField())
		for i := range t.NumField() {
			f := t.Field(i)
			child, invalid := makeDataLayout(f.Type, local)
			if invalid != nil {
				return nil, layoutChildError(f.Name, invalid)
			}
			if f.Name != "_" {
				l.fields[f.Name] = dataField{offset: f.Offset, layout: child}
			}
		}
	default:
		return nil, &dataLayoutError{typ: t}
	}
	return l, nil
}
