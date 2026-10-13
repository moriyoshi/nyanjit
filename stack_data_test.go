package nyanjit_test

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// histogram counts the values below 16 among its argument's nibbles in a
// stack array, then returns the counts packed one per nibble.
func histogram(b *n.Builder) {
	x := b.Param()
	counts := n.StackData[[16]uint8](b)
	i, packed := b.Var(b.Const(0)), b.Var(b.Const(0))
	fail, loop, pack := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.Jump(loop)
	b.Bind(loop)
	nibble := b.And(b.ShiftRightBy(x, b.ShiftLeft(b.Get(i), 2)), b.Const(15))
	slot := counts.Index(nibble, fail)
	slot.Store(b.Add(slot.Load(), b.ConstBits(8, 1)))
	b.Set(i, b.Add(b.Get(i), b.Const(1)))
	b.Branch(b.CompareImmediate(b.Get(i), ir.BranchBelow, 16), loop)
	b.Set(i, b.Const(0))
	b.Jump(pack)
	b.Bind(pack)
	count := b.CastUnsigned(counts.Index(b.Get(i), fail).Load(), 64)
	b.Set(packed, b.Or(b.Get(packed), b.ShiftLeftBy(b.And(count, b.Const(15)), b.ShiftLeft(b.Get(i), 2))))
	b.Set(i, b.Add(b.Get(i), b.Const(1)))
	b.Branch(b.CompareImmediate(b.Get(i), ir.BranchBelow, 16), pack)
	b.Return(b.Get(packed))
	b.Bind(fail)
	b.Return(b.Const(0xdead))
}

func histogramWant(x uint64) uint64 {
	var counts [16]uint64
	for k := 0; k < 16; k++ {
		counts[x>>(4*k)&15]++
	}
	var packed uint64
	for k, c := range counts {
		packed |= (c & 15) << (4 * k)
	}
	return packed
}

func TestStackDataBothTiers(t *testing.T) {
	if runtime.GOARCH == "386" {
		t.Skip("the fixture passes 64-bit words")
	}
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range []compile.Func{{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1024}}, {ABI: abi.C}} {
			b := n.NewBuilderFor(target, f)
			histogram(b)
			u, err := b.Lower()
			if err != nil {
				t.Fatalf("tier %v: %v", tier, err)
			}
			template, err := u.Template()
			if err != nil {
				t.Fatal(err)
			}
			code, err := template.Bind(nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, x := range []uint64{0, 0x0123456789abcdef, 0xffffffffffffffff, 0x1111222233334444} {
				if got, err := code.Call(uintptr(x)); err != nil || uint64(got) != histogramWant(x) {
					t.Errorf("tier %v stack %v: f(%#x) = %#x, %v; want %#x", tier, f.Stack.Practice, x, got, err, histogramWant(x))
				}
			}
			code.Close()
		}
	}
}

func TestStackDataErrors(t *testing.T) {
	b := n.NewBuilder()
	n.StackData[struct{ Items []uint32 }](b)
	b.Return(b.Const(0))
	if _, err := b.Finish(); !errors.Is(err, compile.ErrInvalidFunction) || !strings.Contains(err.Error(), "pointer-free") {
		t.Fatalf("slice in a stack area: %v", err)
	}
	var zero n.Data
	zero.Load()
	n.StackData[uint64](nil).Load()
}
