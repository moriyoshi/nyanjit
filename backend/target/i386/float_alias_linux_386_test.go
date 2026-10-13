//go:build linux && 386

package i386_test

import (
	"math"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
)

func TestFloatBinaryRightHomeAliasExecution(t *testing.T) {
	for _, w := range []uint8{32, 64} {
		for _, operation := range []op.Opcode{op.OpFSub, op.OpFDiv} {
			e := i386.NewByteAssembler(abi.StackNoUse)
			for n, v := range []float64{21, 3} {
				vbits := math.Float64bits(v)
				if w == 32 {
					vbits = uint64(math.Float32bits(float32(v)))
				}
				e.Constant(0, uint64(uint32(vbits)))
				e.Constant(2, vbits>>32)
				e.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: w, A: backend.VectorRegister(uint8(3 + n*4)), B: 0, HighB: 2})
			}
			e.FloatBinary(operation, w, backend.VectorRegister(7), backend.VectorRegister(3), backend.VectorRegister(7))
			e.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: w, A: 0, HighA: 2, B: backend.VectorRegister(7)})
			e.Ret()
			code, err := execution.NewCode(e.Bytes(), execution.CodeOptions{ABI: abi.C}, nil)
			if err != nil {
				t.Fatal(err)
			}
			lo, hi, err := code.CallPair()
			code.Close()
			want := 18.0
			if operation == op.OpFDiv {
				want = 7
			}
			got := uint64(lo)
			expected := uint64(math.Float32bits(float32(want)))
			if w == 64 {
				got = execution.Wide(lo, hi)
				expected = math.Float64bits(want)
			}
			if err != nil || got != expected {
				t.Fatalf("op%d width%d got%#x want%#x err%v", operation, w, got, expected, err)
			}
		}
	}
}
