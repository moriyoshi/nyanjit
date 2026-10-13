package compile_test

import (
	"strings"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestUnreachableFlagsAreRemovedFromReport(t *testing.T) {
	for variant := 0; variant < 3; variant++ {
		b := n.NewBuilder()
		px, py := b.Param(), b.Param()
		vx, vy := b.Var(px), b.Var(py)
		dead, live := b.NewLabel(), b.NewLabel()
		switch variant {
		case 0:
			b.Jump(live)
			b.Bind(dead)
			b.Return(b.Add(b.Mul(b.Get(vx), b.Get(vy)), b.Const(7)))
			b.Bind(live)
			b.Return(b.Get(vx))
		case 1:
			other := b.NewLabel()
			b.Branch(b.IsZero(px), live)
			b.Jump(other)
			b.Bind(dead)
			b.Return(b.Sub(b.Get(vx), b.Get(vy)))
			b.Bind(live)
			b.Return(b.Get(vx))
			b.Bind(other)
			b.Return(b.Get(vy))
		default:
			b.Jump(live)
			b.Bind(live)
			b.Return(b.Get(vx))
			b.Bind(dead)
			b.Return(b.Xor(b.Get(vx), b.Get(vy)))
		}
		p, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		u, err := lowerUnit(p, compile.TargetSpec{Architecture: "arm64"}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
		if err != nil {
			t.Fatal(err)
		}
		listing, err := u.Listing()
		if err != nil {
			t.Fatal(err)
		}
		if u.Report().FlagsClobbered() {
			t.Fatalf("variant %d retained unreachable flag writer:\n%s", variant, listing)
		}
		for _, deadOp := range []string{"adds ", "subs ", "mul "} {
			if strings.Contains(strings.ToLower(listing), deadOp) {
				t.Fatalf("dead operation %q survived:\n%s", deadOp, listing)
			}
		}
	}
}
