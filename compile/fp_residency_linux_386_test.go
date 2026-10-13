package compile_test

import (
	"fmt"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// Rotate six FP label arguments: five XMM homes plus an overflow slot. The
// FP32 case uses direct edge homes; FP64 also exercises paired slot fallback.
func Test386FPLabelOverflowCycle(t *testing.T) {
	for _, w := range []uint8{32, 64} {
		for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
			t.Run(fmt.Sprintf("%d/%d", w, stack), func(t *testing.T) {
				// Raw IR: the FP values are block parameters.
				r := newRawIR()
				pb := r.pb
				entry := pb.Entry()
				count := pb.Param(entry, ir.Scalar(32))
				fp := ir.Scalar(w)
				loop, done := pb.NewBlock(fp, fp, fp, fp, fp, fp, ir.Scalar(32)), pb.NewBlock(fp, fp, fp, fp, fp, fp)
				initial := make([]int, 7)
				for i := 0; i < 6; i++ {
					initial[i] = r.constant(entry, w, fpBits(w, float64(i+1)))
				}
				initial[6] = count
				pb.Jump(entry, loop, initial...)
				ps := r.params(loop, 7)
				rotate := func(i int) int { return r.op(loop, ir.OpFAdd, w, ps[i], r.constant(loop, w, fpBits(w, 0))) }
				pb.Branch(loop, rawIsZero(ps[6]), done, ps[:6], loop, []int{
					rotate(1), rotate(2), rotate(3), rotate(4), rotate(5), rotate(0), r.op(loop, ir.OpSub, 32, ps[6], r.constant(loop, 32, 1))})
				ps = r.params(done, 6)
				sum := ps[0]
				for i, v := range ps[1:] {
					sum = r.op(done, ir.OpFAdd, w, sum, r.op(done, ir.OpFMul, w, v, r.constant(done, w, fpBits(w, float64(uint64(1)<<uint(i+1))))))
				}
				pb.Return(done, sum)
				p := r.seal()
				fn := compile.Func{Stack: compile.StackPolicy{Practice: stack}}
				if stack == abi.StackPlatform {
					fn.Stack.BytesAvailable = 8192
					fn.Context = compile.ContextForeign
				}
				code, e := n.Compile(p, compile.TargetSpec{}, fn)
				if e != nil {
					t.Fatal(e)
				}
				defer code.Close()
				for _, rotations := range []int{0, 1, 2, 7} {
					want := 0.0
					for i := 0; i < 6; i++ {
						want += float64((i+rotations)%6+1) * float64(uint64(1)<<uint(i))
					}
					lo, hi, e := code.CallPair(uintptr(rotations))
					got := execution.Wide(lo, hi)
					if e != nil || got != fpBits(w, want) {
						t.Fatalf("rotations%d got%#x %v want%#x", rotations, got, e, fpBits(w, want))
					}
				}
			})
		}
	}
}
