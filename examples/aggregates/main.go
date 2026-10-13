package main

import (
	"fmt"
	"log"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
)

type Item struct {
	ID    uint32
	Score float64
}
type Args struct {
	Bonus uint32
	Items []Item
}

func build() (*execution.Code, error) {
	b := n.NewBuilder()
	args := n.ValueParam[Args](b) // read-only root, shared writable slice backing array
	output := n.PointerParam[[2]uint64](b)
	ids := n.SliceParam[uint32](b)
	items := args.At("Items")
	index, total := b.Var(b.ConstNative(0)), b.Var(b.Const(0))
	head, done := b.NewLabel(), b.NewLabel()
	b.Jump(head)
	b.Bind(head)
	elem := items.Index(b.Get(index), done)
	add := b.Add(b.CastUnsigned(elem.Field("ID"), 64), b.CastUnsigned(args.Field("Bonus"), 64))
	b.Set(total, b.Add(b.Get(total), add))
	elem.At("Score").Store(b.FAdd(elem.Field("Score"), b.Float64(1)))
	b.Set(index, b.Add(b.Get(index), b.ConstNative(1)))
	b.Jump(head)
	b.Bind(done)
	output.At("[0]").Store(b.Get(total))
	output.At("[1]").Store(b.CastUnsigned(ids.Len(), 64))
	b.Return(output.Field("[0]"))
	program, err := b.Finish()
	if err != nil {
		return nil, err
	}
	return n.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640}})
}

func main() {
	code, err := build()
	if err != nil {
		log.Fatal(err)
	}
	defer code.Close()
	args := Args{Items: []Item{{ID: 20, Score: 1.5}, {ID: 22, Score: 2.5}}}
	var output [2]uint64
	lo, hi, err := code.CallPair(execution.Words(args, &output, []uint32{7, 8, 9})...)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("sum=%d, output=%v, scores=%.1f/%.1f\n", execution.Wide(lo, hi), output, args.Items[0].Score, args.Items[1].Score)
}
