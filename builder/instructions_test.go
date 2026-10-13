package builder

import (
	"reflect"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestCompactInstructionsPreserveEveryIRField(t *testing.T) {
	for _, extra := range []bool{false, true} {
		original := instruction{Variable: 7, Type: ir.Scalar(32), Op: ir.OpAdd, Bits: 32, EntryReg: -1, A: 2, B: 3, C: 5, Imm: 0xfedcba9876543210, ByteOrder: ir.ByteOrderBigEndian, Contract: true, Aligned: true}
		if extra {
			original.Data = []byte{1, 2, 3}
			original.Symbol = "callee"
			original.Args = []int{9, 8}
			original.Effect = &abi.CallEffect{ClobberedRegisters: []string{"X0"}}
		}
		compact := storeInstruction(original)
		if got := compact.expand(); !reflect.DeepEqual(got, original) {
			t.Fatalf("expanded IR differs: %#v", got)
		}
		var written instruction
		compact.writeTo(&written)
		if !reflect.DeepEqual(written, original) {
			t.Fatalf("written IR differs: %#v", written)
		}
		var copied instruction
		copyInstruction(&copied, &original)
		if !reflect.DeepEqual(copied, original) {
			t.Fatalf("copied IR differs: %#v", copied)
		}
	}
}
