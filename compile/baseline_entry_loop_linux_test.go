package compile_test

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

var entryLoopCells [4]uint64

// A label bound before the first instruction lands on the entry block, so
// a branch back to it makes the entry block a loop header. The loop must
// run after the prologue, at both tiers, hardened or not.
func TestBaselineEntryBlockLoop(t *testing.T) {
	cells := &entryLoopCells
	base := uintptr(unsafe.Pointer(&cells[0]))
	build := func(b *nyanjit.Builder, prefix, withVar bool) {
		top := b.NewLabel()
		if prefix {
			// The label then starts a second block instead.
			b.Store64(b.ConstNative(base+24), b.Const(7))
		}
		b.Bind(top)
		addr := b.ConstNative(base)
		x := b.Load64(addr)
		b.Store64(addr, b.Sub(x, b.Const(1)))
		// Twenty values live at once need saved registers and a frame, so
		// a branch into the prologue would unbalance the stack.
		live := make([]nyanjit.Value, 20)
		for i := range live {
			live[i] = b.Add(x, b.Const(uint64(i)))
		}
		acc := live[0]
		for _, v := range live[1:] {
			acc = b.Xor(acc, v)
		}
		if withVar {
			v := b.Var(acc)
			b.Set(v, b.Add(b.Get(v), b.Const(1)))
			acc = b.Get(v)
		}
		b.Store64(b.ConstNative(base+8), acc)
		b.Branch(b.CompareImmediate(x, ir.BranchNotEqual, 1), top)
		b.Return(x)
	}
	for _, harden := range []bool{false, true} {
		for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
			for _, prefix := range []bool{false, true} {
				for _, withVar := range []bool{false, true} {
					if prefix && withVar {
						continue // Vars belong to the scope's entry sequence, which the label ends
					}
					target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
					if err != nil {
						t.Fatal(err)
					}
					f := compile.Func{ABI: abi.C, Harden: harden, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}}
					b := nyanjit.NewBuilderFor(target, f)
					build(b, prefix, withVar)
					u, err := b.Lower()
					if harden && errors.Is(err, compile.ErrUnsupported) {
						continue // riscv64 and loong64 cannot harden control flow at either tier
					}
					if err != nil {
						t.Fatalf("harden=%v tier=%d prefix=%v var=%v: %v", harden, tier, prefix, withVar, err)
					}
					tmpl, err := u.Template()
					if err != nil {
						t.Fatal(err)
					}
					code, err := tmpl.Bind(nil)
					if err != nil {
						t.Fatal(err)
					}
					cells[0] = 5
					got, err := code.Call()
					code.Close()
					if err != nil || got != 1 || cells[0] != 0 {
						t.Fatalf("harden=%v tier=%d prefix=%v var=%v: got %d, %v; counter %d, want 1 and 0", harden, tier, prefix, withVar, got, err, cells[0])
					}
				}
			}
		}
	}
}
