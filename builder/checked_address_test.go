package builder

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func TestCheckedAddressCannotCrossControlEdge(t *testing.T) {
	for _, kind := range []string{"jump", "branch", "var", "derived", "expression"} {
		t.Run(kind, func(t *testing.T) {
			b := NewBuilder()
			base, _ := b.ParamNative()
			index, _ := b.ParamNative()
			slot, _ := b.Var(base)
			bad, next := b.NewLabel(), b.NewLabel()
			count, _ := b.ConstNative(4)
			address, err := b.IndexedAddress(base, index, count, 8, bad)
			if err != nil {
				t.Fatal(err)
			}
			// Each case ends the checked block and uses the address, or a
			// value derived from it, in the next block.
			var use error
			cross := func() {
				if err := b.Jump(next); err != nil {
					t.Fatal(err)
				}
				if err := b.Bind(next); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "jump":
				cross()
				_, use = b.Add(address, address)
			case "branch":
				zero, _ := b.ConstNative(0)
				pred, _ := b.CompareImmediate(zero, ir.BranchEqual, 0)
				if err := b.Branch(pred, bad); err != nil {
					t.Fatal(err)
				}
				_, use = b.Add(address, address)
			case "var":
				use = b.Set(slot, address)
			case "derived":
				delta, _ := b.ConstNative(8)
				derived, e := b.Add(address, delta)
				if e != nil {
					t.Fatal(e)
				}
				cross()
				_, use = b.Add(derived, derived)
			case "expression":
				delta, _ := b.ConstNative(8)
				expression := address.Add(delta)
				cross()
				_, use = b.Add(expression, expression)
			}
			if !errors.Is(use, ErrInvalidFunction) {
				t.Fatalf("checked address escaped through %s: %v", kind, use)
			}
		})
	}
}

func TestCheckedAddressExpressionsExpireAtCalls(t *testing.T) {
	b := NewBuilder()
	base, _ := b.ParamNative()
	index, _ := b.ParamNative()
	count, _ := b.ConstNative(4)
	address, err := b.IndexedAddress(base, index, count, 8, b.NewLabel())
	if err != nil {
		t.Fatal(err)
	}
	zero, _ := b.ConstNative(0)
	expression := address.Add(zero)
	if !b.Valid(expression) {
		t.Fatal("valid expression rejected")
	}
	fn, _ := b.ConstNative(1)
	if _, err := b.CallGo(fn); err != nil {
		t.Fatal(err)
	}
	if b.Valid(expression) {
		t.Fatal("cached expression survived a call")
	}
}

func TestCheckedAddressCannotEnterPrivateBlock(t *testing.T) {
	b := NewBuilder()
	base, _ := b.ParamNative()
	index, _ := b.ParamNative()
	root, _ := b.Var(base)
	bad := b.NewLabel()
	count, _ := b.ConstNative(4)
	if _, err := b.IndexedAddress(base, index, count, 8, bad); err != nil {
		t.Fatal(err)
	}
	// Index allocates its continuation immediately after bad. A Label
	// forged with that index must not enter it without the bounds check.
	_ = root
	private := Label{builder: bad.builder, index: bad.index + 1}
	if err := b.Jump(private); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("entered private checked continuation: %v", err)
	}
	if err := b.Bind(private); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("bound private checked continuation: %v", err)
	}
}
