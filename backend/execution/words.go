package execution

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"unsafe"
)

// ErrArgumentType means Words received a value it cannot encode.
var ErrArgumentType = errors.New("execution: unsupported argument type")

// maxArguments is the entry parameter limit that MaxArgumentWords is sized for.
const maxArguments = 8

// argumentFrame owns a Words result that refers to Go memory. The returned
// slice points into words, so while the caller holds it the frame, and
// through keep every boxed root and pointer target, stays reachable. Go's
// heap does not move objects, so the addresses stay valid across collections
// and stack growth.
type argumentFrame struct {
	words [MaxArgumentWords]uintptr
	keep  [maxArguments]unsafe.Pointer
}

type argumentKind uint8

const (
	nativeArgument    argumentKind = iota // one word
	wideArgument                          // 64 bits: two words on 32-bit hosts
	referenceArgument                     // the address of Go memory: one word
)

// Words converts logical arguments into native ABI words, choosing each
// encoding from the value's type:
//
//   - int, uint, uintptr, bool and integers of at most 32 bits occupy one
//     word. Narrow signed values are encoded at their own width, so
//     int32(-1) becomes 0xffffffff.
//   - int64, uint64 and float64 are 64-bit values: one word on 64-bit
//     hosts and the low then the high word on 32-bit hosts, matching the
//     cdecl stack order. Write uint64(35) rather than an untyped constant,
//     which becomes a native int.
//   - float32 and float64 travel as their IEEE bit patterns.
//   - A struct or array value becomes the address of a read-only boxed copy.
//     A typed pointer to transferable scalar or aggregate memory becomes its
//     writable address. Nil aggregate pointers are rejected; nil scalar
//     pointers encode zero for trusted raw memory operations.
//   - A slice becomes the address of its boxed header; generated code reads
//     the header and may write elements in its backing array. A nil slice
//     has length zero, so every bounds predicate is false.
//
// Aggregates may contain bools, integers, floats, structs, arrays and slices,
// nested arbitrarily. Pointers, unsafe.Pointer, strings, maps, interfaces,
// chans, funcs and complex numbers are rejected with ErrArgumentType and a
// field path. Named types encode like their underlying kinds.
//
// The frame keeps roots and their reachable backing arrays alive. If generated
// code calls back into Go and that callback replaces a reachable slice, an old
// backing array may become unreachable while generated code still uses it.
// Do not replace those slices until the generated call returns.
//
// Use c.Call(Words(values...)...) or c.CallPair(Words(values...)...) for all
// entry kinds, including managed and aggregate entries. Pointer inputs escape
// to the non-moving heap, and the original Words slice owns their roots until
// the native call returns. Copying words into another slice (for example with
// append) drops that ownership. Retain the original result if you do copy it.
// Words cannot check a compiled entry's signature: callers must supply the
// matching logical types and layouts. A reference-bearing result panics with
// ErrArity if it exceeds MaxArgumentWords words or the logical argument limit.
func Words(values ...any) []uintptr {
	count, references := 0, false
	for i, v := range values {
		_, kind := classifyArgument(i, v)
		if kind == wideArgument && strconv.IntSize == 32 {
			count += 2
		} else {
			count++
		}
		references = references || kind == referenceArgument
	}
	if !references {
		words := make([]uintptr, 0, count)
		for i, v := range values {
			bits, kind := classifyArgument(i, v)
			words = appendArgument(words, bits, kind)
		}
		return words
	}
	if count > MaxArgumentWords || len(values) > maxArguments {
		panic(ErrArity)
	}
	frame := new(argumentFrame)
	words, kept := frame.words[:0:count], 0
	for i, v := range values {
		bits, kind := classifyArgument(i, v)
		if kind == referenceArgument {
			// An interface's data word is the pointer itself for a struct
			// pointer. A transferable value is never pointer-shaped, so its
			// data word addresses the copy made when the value was boxed;
			// that copy may be shared static data, hence read-only.
			address := (*[2]unsafe.Pointer)(unsafe.Pointer(&v))[1]
			frame.keep[kept] = address
			kept++
			words = append(words, uintptr(address))
			continue
		}
		words = appendArgument(words, bits, kind)
	}
	return words
}

func appendArgument(words []uintptr, bits uint64, kind argumentKind) []uintptr {
	if kind == wideArgument && strconv.IntSize == 32 {
		return append(words, uintptr(uint32(bits)), uintptr(uint32(bits>>32)))
	}
	return append(words, uintptr(bits))
}

// classifyArgument returns a scalar's bits and how Words encodes value.
func classifyArgument(index int, value any) (uint64, argumentKind) {
	switch v := value.(type) {
	case uint64:
		return v, wideArgument
	case int64:
		return uint64(v), wideArgument
	case float64:
		return math.Float64bits(v), wideArgument
	case int:
		return uint64(uintptr(v)), nativeArgument
	case uint:
		return uint64(uintptr(v)), nativeArgument
	case uintptr:
		return uint64(v), nativeArgument
	case uint32:
		return uint64(v), nativeArgument
	case int32:
		return uint64(uint32(v)), nativeArgument
	case float32:
		return uint64(math.Float32bits(v)), nativeArgument
	case bool:
		if v {
			return 1, nativeArgument
		}
		return 0, nativeArgument
	}
	t := reflect.TypeOf(value)
	if t == nil {
		panic(argumentTypeError(index, value))
	}
	switch t.Kind() {
	case reflect.Bool:
		if reflect.ValueOf(value).Bool() {
			return 1, nativeArgument
		}
		return 0, nativeArgument
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32:
		bits := uint64(reflect.ValueOf(value).Int())
		if t.Kind() != reflect.Int {
			bits &= 1<<(8*t.Size()) - 1
		}
		return uint64(uintptr(bits)), nativeArgument
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uintptr:
		return reflect.ValueOf(value).Uint(), nativeArgument
	case reflect.Int64:
		return uint64(reflect.ValueOf(value).Int()), wideArgument
	case reflect.Uint64:
		return reflect.ValueOf(value).Uint(), wideArgument
	case reflect.Float32:
		return uint64(math.Float32bits(float32(reflect.ValueOf(value).Float()))), nativeArgument
	case reflect.Float64:
		return math.Float64bits(reflect.ValueOf(value).Float()), wideArgument
	case reflect.Struct, reflect.Array, reflect.Slice:
		if invalid := checkTransferable(t); invalid != nil {
			panic(fmt.Errorf("%w: argument %d field %q has unsupported type %s", ErrArgumentType, index, invalid.path, invalid.typ))
		}
		return 0, referenceArgument
	case reflect.Pointer:
		if (t.Elem().Kind() == reflect.Struct || t.Elem().Kind() == reflect.Array || t.Elem().Kind() == reflect.Slice) && reflect.ValueOf(value).IsNil() {
			panic(fmt.Errorf("%w: argument %d is a nil aggregate pointer", ErrArgumentType, index))
		}
		if invalid := checkTransferable(t.Elem()); invalid != nil {
			panic(fmt.Errorf("%w: argument %d field %q has unsupported type %s", ErrArgumentType, index, invalid.path, invalid.typ))
		}
		return 0, referenceArgument
	}
	panic(argumentTypeError(index, value))
}

func argumentTypeError(index int, value any) error {
	return fmt.Errorf("%w: argument %d has type %T", ErrArgumentType, index, value)
}
