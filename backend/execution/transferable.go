package execution

import (
	"reflect"
	"strings"
	"sync"
)

type transferError struct {
	path string
	typ  reflect.Type
}
type transferResult struct{ invalid *transferError }

var transferableTypes sync.Map // reflect.Type -> transferResult
var transferableMu sync.Mutex

func transferable(t reflect.Type) bool { return checkTransferable(t) == nil }
func checkTransferable(t reflect.Type) *transferError {
	if cached, ok := transferableTypes.Load(t); ok {
		return cached.(transferResult).invalid
	}
	transferableMu.Lock()
	defer transferableMu.Unlock()
	if cached, ok := transferableTypes.Load(t); ok {
		return cached.(transferResult).invalid
	}
	invalid := scanTransferable(t, make(map[reflect.Type]bool))
	transferableTypes.Store(t, transferResult{invalid: invalid})
	return invalid
}
func transferChildError(prefix string, invalid *transferError) *transferError {
	if invalid.path != "" && !strings.HasPrefix(invalid.path, "[") {
		prefix += "."
	}
	return &transferError{path: prefix + invalid.path, typ: invalid.typ}
}
func scanTransferable(t reflect.Type, visiting map[reflect.Type]bool) *transferError {
	// The only permitted recursive edges are slice elements. Check each type's
	// own fields once per traversal, without following a cycle indefinitely.
	if visiting[t] {
		return nil
	}
	visiting[t] = true
	switch t.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64:
		return nil
	case reflect.Array, reflect.Slice:
		if invalid := scanTransferable(t.Elem(), visiting); invalid != nil {
			return transferChildError("[0]", invalid)
		}
		return nil
	case reflect.Struct:
		for i := range t.NumField() {
			f := t.Field(i)
			if invalid := scanTransferable(f.Type, visiting); invalid != nil {
				return transferChildError(f.Name, invalid)
			}
		}
		return nil
	}
	return &transferError{typ: t}
}
