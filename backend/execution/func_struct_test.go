package execution

import (
	"errors"
	"reflect"
	"strconv"
	"testing"
	"unsafe"
)

func TestFuncStructPointerSignatures(t *testing.T) {
	type record struct {
		Count uint32
		Inner struct{ Data [3]float64 }
	}
	native := uint8(strconv.IntSize)
	options := CodeOptions{Arity: 1, ParamBits: []uint8{native}}
	for _, typ := range []reflect.Type{reflect.TypeFor[func(*record) uint64](), reflect.TypeFor[func(*[4]uint16) uint64](), reflect.TypeFor[func(*struct{ C []uint64 }) int64]()} {
		options.ParameterTypes = []reflect.Type{typ.In(0)}
		if err := checkFuncType(typ, options); err != nil {
			t.Fatal(err)
		}
	}
	options.ParameterTypes = []reflect.Type{reflect.TypeFor[*record]()}
	for _, typ := range []reflect.Type{
		reflect.TypeFor[func(record) uint64](),
		reflect.TypeFor[func(*struct{ Pointer *int })](),
		reflect.TypeFor[func(*struct{ Text string })](),
		reflect.TypeFor[func(*int)](), reflect.TypeFor[func(unsafe.Pointer)](),
		reflect.TypeFor[func(**record)](),
		reflect.TypeFor[func(*struct{ C complex128 })](),
		reflect.TypeFor[func(record)](), reflect.TypeFor[func([2]uint32)](), reflect.TypeFor[func([]uint32)](),
	} {
		if err := checkFuncType(typ, options); !errors.Is(err, ErrSignature) {
			t.Errorf("%s: %v", typ, err)
		}
	}
	options.ParamBits[0] = 96 - native // opposite of the native 32/64 width
	if err := checkFuncType(reflect.TypeFor[func(*record)](), options); !errors.Is(err, ErrSignature) {
		t.Fatalf("pointer width mismatch: %v", err)
	}
}

// A 386 Go-ABI entry stores both result words, so a direct function value must
// reserve an 8-byte result (security audit 2026-10-25, F17).
func TestFuncResultWidthOn386(t *testing.T) {
	options := CodeOptions{Arity: 1, ParamBits: []uint8{32}}
	for _, typ := range []reflect.Type{reflect.TypeFor[func(uint32) uint64](), reflect.TypeFor[func(uint32) int64]()} {
		if err := checkFuncTypeFor("386", typ, options); err != nil {
			t.Errorf("%s: %v", typ, err)
		}
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[func(uint32)](), reflect.TypeFor[func(uint32) uint32](), reflect.TypeFor[func(uint32) uint8](), reflect.TypeFor[func(uint32) uint16]()} {
		if err := checkFuncTypeFor("386", typ, options); !errors.Is(err, ErrSignature) {
			t.Errorf("386 %s: %v, want ErrSignature", typ, err)
		}
		if err := checkFuncTypeFor("amd64", typ, options); err != nil {
			t.Errorf("amd64 %s: %v", typ, err)
		}
	}
}
