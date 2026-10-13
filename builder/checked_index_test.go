package builder

import (
	"errors"
	"testing"
)

// These signatures require an explicit failure edge: the old advisory-predicate
// program cannot be expressed without handling out-of-bounds control flow.
var _ func(Data, Value, Label) (Data, error) = Data.Index
var _ func(*Builder, Value, Value, Value, uint64, Label) (Value, error) = (*Builder).IndexedAddress

func TestCheckedIndexRequiresFailureEdge(t *testing.T) {
	b := NewBuilder()
	root, err := SliceParam[uint64](b)
	if err != nil {
		t.Fatal(err)
	}
	i, _ := b.ConstNative(0)
	if _, err := root.Index(i, Label{}); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("missing failure label accepted: %v", err)
	}
	// Even an unused, unbound failure edge is rejected at Finish.
	bad := b.NewLabel()
	elem, err := root.Index(i, bad)
	if err != nil {
		t.Fatal(err)
	}
	v, err := elem.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(v); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("unbound failure edge accepted: %v", err)
	}
}

func TestIndexedDataExpiresAtCall(t *testing.T) {
	for _, op := range []string{"C", "Go", "syscall"} {
		t.Run(op, func(t *testing.T) {
			b := NewBuilder()
			root, err := SliceParam[uint64](b)
			if err != nil {
				t.Fatal(err)
			}
			bad := b.NewLabel()
			i, _ := b.ConstNative(0)
			elem, err := root.Index(i, bad)
			if err != nil {
				t.Fatal(err)
			}
			saved := elem.Base()
			target, _ := b.ConstNative(1)
			switch op {
			case "C":
				_, err = b.CallC(target)
			case "Go":
				_, err = b.CallGo(target)
			case "syscall":
				number, e := b.Const(1)
				if e != nil {
					t.Fatal(e)
				}
				_, err = b.Syscall(number)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := elem.Load(); !errors.Is(err, ErrInvalidFunction) {
				t.Fatalf("expired load accepted: %v", err)
			}
			if _, err := elem.Rebase(saved).Load(); !errors.Is(err, ErrInvalidFunction) {
				t.Fatalf("Rebase resurrected expired view: %v", err)
			}
			i, _ = b.ConstNative(0)
			fresh, err := root.Index(i, bad)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fresh.Load(); err != nil {
				t.Fatalf("fresh lookup rejected: %v", err)
			}
		})
	}
}
