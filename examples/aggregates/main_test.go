//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package main

import (
	"github.com/moriyoshi/nyanjit/backend/execution"
	"testing"
)

func TestAggregateExample(t *testing.T) {
	code, err := build()
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	args := Args{Bonus: 2, Items: []Item{{ID: 20, Score: 1.5}, {ID: 22, Score: 2.5}}}
	var out [2]uint64
	lo, hi, err := code.CallPair(execution.Words(args, &out, []uint32{7, 8, 9})...)
	if err != nil || execution.Wide(lo, hi) != 46 || out != [2]uint64{46, 3} || args.Bonus != 2 || args.Items[0].Score != 2.5 || args.Items[1].Score != 3.5 {
		t.Fatalf("result %d out %v args %+v err %v", execution.Wide(lo, hi), out, args, err)
	}
}
