package ppc64

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/machine"
)

func TestPPCFlagReadsIncludeCarryConsumers(t *testing.T) {
	for _, op := range []machine.Opcode{machine.OpCarryBit, machine.OpWordCarry} {
		if ppcFlagReads(machine.Instruction{Op: op, CarryMode: machine.CarryFlags}) != machine.FlagCarry {
			t.Fatal("carry consumer missing flag dependency")
		}
		if ppcFlagReads(machine.Instruction{Op: op, CarryMode: machine.CarryOperands}) != 0 {
			t.Fatal("operand carry unexpectedly consumes flags")
		}
	}
}
