package regalloc

import (
	"errors"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
)

func TestFunctionBuilder(t *testing.T) {
	var b FunctionBuilder
	for range 2 { // the second round reuses the storage
		b.Reset()
		x, k := b.AddValue(Value{}), b.AddValue(Value{Constant: true})
		entry := b.AddBlock(x)
		sum := b.AddValue(Value{})
		b.AddPosition(Operand{Value: x}, Operand{Value: k}, Operand{Value: sum, Def: true, Tied: true})
		effect := &abi.CallEffect{}
		b.AddCall(effect, Operand{Value: sum})
		exit := b.AddBlock(sum)
		b.AddReturn(Operand{Value: sum})
		for i := range 40 { // grow params and args past the first blocks'
			b.AddValue(Value{})
			b.AddEdge(entry, exit, sum+i)
		}
		if b.Values() != 43 || b.Positions() != 3 || b.Blocks() != 2 || b.Edges() != 40 {
			t.Fatalf("counts %d %d %d %d", b.Values(), b.Positions(), b.Blocks(), b.Edges())
		}
		if got := b.Block(entry); got.Start != 0 || got.End != 2 || !slices.Equal(got.Params, []int{x}) {
			t.Fatalf("entry %+v", got)
		}
		if got := b.Block(exit); got.Start != 2 || got.End != 3 || !slices.Equal(got.Params, []int{sum}) {
			t.Fatalf("exit %+v", got)
		}
		if got := b.Edge(0); got.From != entry || got.To != exit || !slices.Equal(got.Args, []int{sum}) {
			t.Fatalf("edge %+v", got)
		}
		if b.Call(1) != effect || b.Call(0) != nil || !b.Returns(2) || b.Returns(1) {
			t.Fatal("call or return positions lost")
		}
		if ops := b.Operands(0, nil); len(ops) != 3 || cap(ops) != 3 || !ops[2].Tied {
			t.Fatalf("operands %+v", ops)
		}
		outer := b.AddScope(Scope{Parent: -1, Start: 0, End: 3, Registers: backend.NewRegisterSet(1)})
		inner := b.AddScope(Scope{Parent: outer, Start: 1, End: 2})
		if b.Scopes() != 2 || b.Scope(inner).Parent != outer || !b.Scope(outer).Registers.Contains(1) {
			t.Fatalf("scopes %d %+v", b.Scopes(), b.Scope(inner))
		}
		b.SetClobbers(0, backend.NewRegisterSet(2, 3))
		if !b.Clobbers(0).Contains(3) || b.Clobbers(1).Len() != 0 {
			t.Fatalf("clobbers %v %v", b.Clobbers(0), b.Clobbers(1))
		}
	}
}

func TestAllocatorsRejectNilFunction(t *testing.T) {
	for _, c := range testAllocators() {
		if err := c.a.Allocate(Config{Registers: gprs(2)}, nil, nil); !errors.Is(err, backend.ErrInvalidFunction) {
			t.Fatalf("%s: Allocate(nil) = %v", c.name, err)
		}
	}
}
