package ir

import (
	"strings"
	"testing"
)

// TestOpcodeEndMatchesOp keeps opcodeEnd at the end of op's opcode list.
func TestOpcodeEndMatchesOp(t *testing.T) {
	if strings.HasPrefix((opcodeEnd-1).String(), "Opcode(") || !strings.HasPrefix(opcodeEnd.String(), "Opcode(") || opcodeEnd.ProducesValue() {
		t.Fatalf("opcodeEnd = %d does not end op's opcodes", opcodeEnd)
	}
}
