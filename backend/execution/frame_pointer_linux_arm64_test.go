package execution_test

import (
	"encoding/binary"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
)

// An AAPCS64 callee may store anything below its entry SP. The trampoline's
// frame pointer, which addresses the caller's saved R29, must therefore not
// point below it.
func TestCallKeepsSavedFramePointerAboveCallee(t *testing.T) {
	var code []byte
	for _, word := range []uint32{
		0x910003e1, // MOV X1, SP
		0xcb0103a0, // SUB X0, X29, X1
		0xd65f03c0, // RET
	} {
		code = binary.LittleEndian.AppendUint32(code, word)
	}
	c, err := execution.NewCode(code, execution.CodeOptions{ABI: abi.C}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	distance, err := c.Call()
	if err != nil {
		t.Fatal(err)
	}
	if int64(distance) < 0 {
		t.Fatalf("saved frame pointer is %d bytes below the callee's SP", -int64(distance))
	}
}
