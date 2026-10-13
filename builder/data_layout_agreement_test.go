package builder

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/execution"
)

type agreementRecursive []agreementRecursive
type agreementNode struct{ Kids []agreementNode }
type agreementNamedInt int32
type agreementNamedArray [2]agreementNamedInt
type agreementNamedSlice []agreementNamedArray
type agreementBadA struct {
	Children []agreementBadB
	BadA     string
}
type agreementBadB struct {
	Children []agreementBadA
	BadB     complex64
}

// Words calls checkTransferable on the wrapper's static type, including nil
// fields and zero-length arrays. Wrapping also subjects pointers/scalars to the
// aggregate member rules, rather than Words' distinct top-level conventions.
// This exercises both real checks without adding exported testing API.
func wordsTransferCheck(t *testing.T, typ reflect.Type) (accepted bool, path string) {
	t.Helper()
	wrapper := reflect.StructOf([]reflect.StructField{{Name: "Value", Type: typ}})
	accepted = true
	defer func() {
		if recovered := recover(); recovered != nil {
			err, ok := recovered.(error)
			if !ok || !errors.Is(err, execution.ErrArgumentType) {
				t.Fatalf("unexpected Words panic: %v", recovered)
			}
			accepted = false
			_, quoted, ok := strings.Cut(err.Error(), " field ")
			if !ok {
				t.Fatalf("missing field path: %v", err)
			}
			end := strings.Index(quoted[1:], "\"") + 2
			if end < 2 {
				t.Fatalf("malformed field path: %v", err)
			}
			full, e := strconv.Unquote(quoted[:end])
			if e != nil {
				t.Fatal(e)
			}
			if !strings.HasPrefix(full, "Value") {
				t.Fatalf("wrapper path: %q", full)
			}
			path = strings.TrimPrefix(strings.TrimPrefix(full, "Value"), ".")
		}
	}()
	execution.Words(reflect.Zero(wrapper).Interface())
	return
}

func TestDataLayoutTransferabilityAgreement(t *testing.T) {
	types := []struct {
		typ    reflect.Type
		accept bool
	}{
		{reflect.TypeFor[bool](), true},
		{reflect.TypeFor[int](), true}, {reflect.TypeFor[int8](), true}, {reflect.TypeFor[int16](), true}, {reflect.TypeFor[int32](), true}, {reflect.TypeFor[int64](), true},
		{reflect.TypeFor[uint](), true}, {reflect.TypeFor[uint8](), true}, {reflect.TypeFor[uint16](), true}, {reflect.TypeFor[uint32](), true}, {reflect.TypeFor[uint64](), true}, {reflect.TypeFor[uintptr](), true},
		{reflect.TypeFor[float32](), true}, {reflect.TypeFor[float64](), true},
		{reflect.TypeFor[complex64](), false}, {reflect.TypeFor[complex128](), false}, {reflect.TypeFor[string](), false},
		{reflect.TypeFor[*uint32](), false}, {reflect.TypeFor[*struct{ X uint32 }](), false}, {reflect.TypeFor[unsafe.Pointer](), false},
		{reflect.TypeFor[map[string]int](), false}, {reflect.TypeFor[chan int](), false}, {reflect.TypeFor[<-chan int](), false}, {reflect.TypeFor[func()](), false}, {reflect.TypeFor[any](), false}, {reflect.TypeFor[fmt.Stringer](), false},
		{reflect.TypeFor[[0]string](), false}, {reflect.TypeFor[[0]*int](), false}, {reflect.TypeFor[[0]struct{ Bad complex64 }](), false},
		{reflect.TypeFor[struct{}](), true}, {reflect.TypeFor[[0]uint32](), true},
		{reflect.TypeFor[struct{ Rows [2]struct{ Values []uint16 } }](), true},
		{reflect.TypeFor[struct{ Items []struct{ Bad *int } }](), false},
		{reflect.TypeFor[[]struct{ Bad string }](), false},
		{reflect.TypeFor[agreementRecursive](), true}, {reflect.TypeFor[agreementNode](), true},
		{reflect.TypeFor[agreementNamedInt](), true}, {reflect.TypeFor[agreementNamedArray](), true}, {reflect.TypeFor[agreementNamedSlice](), true},
		{reflect.TypeFor[struct {
			hidden uint32
			_      uint16
		}](), true},
		{reflect.TypeFor[struct{ hidden string }](), false}, {reflect.TypeFor[struct{ _ string }](), false},
		// The first invalid root warms the layout cache before its recursive sibling.
		{reflect.TypeFor[agreementBadA](), false}, {reflect.TypeFor[agreementBadB](), false},
	}
	for pass := 0; pass < 2; pass++ {
		for _, tc := range types {
			t.Run(fmt.Sprintf("pass%d/%s", pass, tc.typ), func(t *testing.T) {
				layout, invalid := cachedDataLayout(tc.typ)
				accepted := invalid == nil
				other, path := wordsTransferCheck(t, tc.typ)
				if accepted != tc.accept || other != tc.accept || (layout != nil) != accepted {
					t.Fatalf("%v: layout accepted %v, Words accepted %v; want %v", tc.typ, accepted, other, tc.accept)
				}
				if invalid != nil && invalid.path != path {
					t.Fatalf("%v: builder field %q, execution field %q", tc.typ, invalid.path, path)
				}
			})
		}
	}
}
