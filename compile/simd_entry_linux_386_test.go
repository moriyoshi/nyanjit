//go:build linux && 386

package compile_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestSIMD386TypedEntryCAndGoABI(t *testing.T) {
	m, address := simdMemory(t)
	for i := 0; i < 16; i++ {
		m[i] = byte(i + 1)
	}
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		b := nyanjit.NewBuilder()
		p, count := b.ParamNative(), b.ParamNative()
		label := b.NewLabel()
		shape := ir.Vec(16, 8)
		// The vector and count cross the edge by dominance.
		vector := b.VLoad(shape, p)
		b.Jump(label)
		b.Bind(label)
		b.Return(b.Add(b.CastUnsigned(b.ExtractLane(vector, 9), 32), count))
		program, err := testFinishRaw(b)
		if err != nil {
			t.Fatal(err)
		}
		code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: flavor, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
		if err != nil {
			t.Fatal(err)
		}
		lo, hi, err := code.CallPair(address, 32)
		if got := execution.Wide(lo, hi); err != nil || got != 42 {
			t.Fatalf("entry ABI%d got%d,%v;want42", flavor, got, err)
		}
		code.Close()
	}
}
