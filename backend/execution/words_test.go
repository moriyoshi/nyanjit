package execution

import (
	"errors"
	"math"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"
)

type namedWord uint16

type wordsRecord struct {
	A uint32
	B uint64
	C struct {
		D [3]uint16
		E float64
	}
}

// wide returns the words Words produces for a 64-bit value on this host.
func wide(v uint64) []uintptr {
	if strconv.IntSize == 32 {
		return []uintptr{uintptr(uint32(v)), uintptr(uint32(v >> 32))}
	}
	return []uintptr{uintptr(v)}
}

// wordPointer reads an address word as a pointer without a uintptr
// conversion; the Words result keeps the target alive.
func wordPointer(word *uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(word))
}

func TestWordsScalars(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []any
		want   []uintptr
	}{
		{"empty", nil, []uintptr{}},
		{"native", []any{int(7), uint(8), uintptr(9)}, []uintptr{7, 8, 9}},
		{"negative int", []any{-1}, []uintptr{^uintptr(0)}},
		{"narrow signed", []any{int8(-1), int16(-2), int32(-3)}, []uintptr{0xff, 0xfffe, 0xfffffffd}},
		{"narrow unsigned", []any{uint8(1), uint16(2), uint32(0xffffffff)}, []uintptr{1, 2, 0xffffffff}},
		{"bool", []any{true, false}, []uintptr{1, 0}},
		{"named", []any{namedWord(0xbeef)}, []uintptr{0xbeef}},
		{"float32", []any{float32(1.5)}, []uintptr{uintptr(math.Float32bits(1.5))}},
		{"uint64", []any{uint64(0x1234567800000010)}, wide(0x1234567800000010)},
		{"int64", []any{int64(-2)}, wide(0xfffffffffffffffe)},
		{"float64", []any{2.25}, wide(math.Float64bits(2.25))},
		{"mixed", []any{3, uint64(1 << 40), uint32(5)}, slices.Concat([]uintptr{3}, wide(1<<40), []uintptr{5})},
	} {
		if got := Words(tc.values...); !slices.Equal(got, tc.want) {
			t.Errorf("%s: Words = %#x, want %#x", tc.name, got, tc.want)
		}
	}
}

func TestWordsStructs(t *testing.T) {
	value := wordsRecord{A: 1, B: 2}
	value.C.D[2] = 3
	value.C.E = 4.5
	pointer := &wordsRecord{A: 5}
	words := Words(uint32(9), value, pointer)
	if len(words) != 3 || words[0] != 9 {
		t.Fatalf("Words = %#x", words)
	}
	if words[2] != uintptr(unsafe.Pointer(pointer)) {
		t.Fatalf("pointer word %#x, want %p", words[2], pointer)
	}
	copied := (*wordsRecord)(wordPointer(&words[1]))
	if *copied != value || copied == &value {
		t.Fatalf("struct word addresses %+v, want a copy of %+v", *copied, value)
	}
	runtime.KeepAlive(words)

	// Zero-size and small structs may box to shared static data.
	if words := Words(struct{}{}, struct{ A uint8 }{7}); len(words) != 2 || *(*uint8)(wordPointer(&words[1])) != 7 {
		t.Fatalf("small struct words %#x", words)
	}
}

func TestWordsRejectsUnsupportedValues(t *testing.T) {
	for _, value := range []any{
		nil,
		"text",
		struct{ C complex128 }{},
		[]*int{nil},
		[0]*int{},
		struct{ P *int }{},
		struct{ A [1]any }{},
	} {
		func() {
			defer func() {
				err, _ := recover().(error)
				if !errors.Is(err, ErrArgumentType) {
					t.Errorf("Words(%T) panic = %v, want ErrArgumentType", value, err)
				}
			}()
			Words(value)
		}()
	}
}

func TestWordsReferenceLimit(t *testing.T) {
	values := make([]any, maxArguments+1)
	for i := range values {
		values[i] = struct{ A uint8 }{}
	}
	defer func() {
		if err, _ := recover().(error); !errors.Is(err, ErrArity) {
			t.Fatalf("panic = %v, want ErrArity", err)
		}
	}()
	Words(values...)
}

// A Words result must keep struct pointer targets reachable on its own.
func TestWordsKeepsTargetsAlive(t *testing.T) {
	collected := make(chan struct{})
	words := func() []uintptr {
		target := &wordsRecord{A: 0x5a5a}
		runtime.SetFinalizer(target, func(*wordsRecord) { close(collected) })
		return Words(target)
	}()
	for range 5 {
		runtime.GC()
		select {
		case <-collected:
			t.Fatal("pointer target collected while its words were reachable")
		case <-time.After(time.Millisecond):
		}
	}
	if (*wordsRecord)(wordPointer(&words[0])).A != 0x5a5a {
		t.Fatal("pointer target changed")
	}
	runtime.KeepAlive(words)
}

func TestWordsArraysAndSlices(t *testing.T) {
	type numbers [3]uint16
	type list []uint32
	array := numbers{11, 22, 33}
	pointer := &numbers{44, 55, 66}
	slice := list{7, 8, 9}
	var nilSlice []uint64
	words := Words(array, pointer, slice, nilSlice, [2][]uint32{{1}, {2, 3}})
	if len(words) != 5 {
		t.Fatalf("words %#x", words)
	}
	copy := (*numbers)(wordPointer(&words[0]))
	if *copy != array || copy == &array {
		t.Fatalf("array copy %v", copy)
	}
	if words[1] != uintptr(unsafe.Pointer(pointer)) {
		t.Fatal("array pointer differs")
	}
	values := *(*list)(wordPointer(&words[2]))
	if len(values) != 3 || values[1] != 8 {
		t.Fatalf("slice header %+v", values)
	}
	values[1] = 99
	if slice[1] != 99 {
		t.Fatal("slice did not share backing array")
	}
	if got := *(*[]uint64)(wordPointer(&words[3])); got != nil {
		t.Fatalf("nil slice %v", got)
	}
	nested := *(*[2][]uint32)(wordPointer(&words[4]))
	if nested[1][1] != 3 {
		t.Fatal("array of slice values changed")
	}
	runtime.KeepAlive(words)
}

func TestWordsTransferErrorsHavePaths(t *testing.T) {
	for _, value := range []any{
		struct{ Items []struct{ Bad *int } }{},
		&struct{ Items [2]struct{ Bad string } }{},
		[]struct{ Bad complex64 }{},
	} {
		func() {
			defer func() {
				err, _ := recover().(error)
				if !errors.Is(err, ErrArgumentType) || !strings.Contains(err.Error(), "[0].Bad") {
					t.Errorf("%T: %v", value, err)
				}
			}()
			Words(value)
		}()
	}
}

func TestWordsKeepsSliceGraphAlive(t *testing.T) {
	collected := make(chan struct{}, 1)
	words := func() []uintptr {
		backing := new([4]wordsRecord)
		backing[0].A = 0x5a5a
		runtime.SetFinalizer(backing, func(*[4]wordsRecord) { collected <- struct{}{} })
		return Words(struct{ Rows [1][]wordsRecord }{Rows: [1][]wordsRecord{backing[:]}})
	}()
	for range 5 {
		runtime.GC()
		select {
		case <-collected:
			t.Fatal("backing array collected while Words was reachable")
		case <-time.After(time.Millisecond):
		}
	}
	root := (*struct{ Rows [1][]wordsRecord })(wordPointer(&words[0]))
	if root.Rows[0][0].A != 0x5a5a {
		t.Fatal("backing memory changed")
	}
	runtime.KeepAlive(words)
}

func TestWordsRecursiveTransferTypes(t *testing.T) {
	type node struct {
		Children []node
		Value    uint32
	}
	root := node{Children: []node{{Value: 42}}}
	words := Words(root, []node{root})
	if got := (*node)(wordPointer(&words[0])); got.Children[0].Value != 42 {
		t.Fatal("recursive value changed")
	}
	type bad struct {
		Children []bad
		Pointer  *int
	}
	func() {
		defer func() {
			err, _ := recover().(error)
			if !errors.Is(err, ErrArgumentType) {
				t.Fatalf("recursive rejection: %v", err)
			}
		}()
		Words(bad{})
	}()
	runtime.KeepAlive(words)
}
