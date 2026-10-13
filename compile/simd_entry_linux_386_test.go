//go:build linux && 386

package compile_test

import (
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/execution"
	"testing"
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
		shape := nyanjit.Vec(16, 8)
		b.JumpArgs(label, b.VLoad(shape, p), count)
		state := b.BindParamsTypes(label, shape, nyanjit.Scalar(32))
		b.Return(b.Add(b.CastUnsigned(b.ExtractLane(state[0], 9), 32), state[1]))
		program, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		code, err := compile.Compile(program, abi.CompileOptions{ABI: flavor, Stack: abi.StackPlatform, StackBytesAvailable: 65536})
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
