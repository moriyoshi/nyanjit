//go:build linux && (loong64 || riscv64)

package execution

import (
	"encoding/binary"
	"runtime"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/moriyoshi/nyanjit/backend/abi"
)

// mapWords maps little-endian instruction words as a C ABI entry.
func mapWords(t *testing.T, words []uint32) uintptr {
	t.Helper()
	raw := make([]byte, 4*len(words))
	for i, w := range words {
		binary.LittleEndian.PutUint32(raw[4*i:], w)
	}
	code, err := NewCode(raw, CodeOptions{ABI: abi.C}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { code.Close() })
	entry, err := code.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

// parityThunk is a C thunk taking a gateway, its argument block and the SP
// mod 16 to give it (0 or 8). It biases SP deliberately and keeps its recovery
// state relative to the biased SP, outside the gateway's own frame. The words
// are the Go assembler's uncompressed encoding of:
//
//	loong64:                 riscv64:
//	MOVV R3, R12             MOV SP, X5
//	ADDV $-64, R3            SUB $64, SP
//	MOVV $-16, R13           AND $-16, SP
//	AND R13, R3              ADD X12, SP
//	ADDV R6, R3              MOV X5, 8(SP)
//	MOVV R12, 8(R3)          MOV RA, 0(SP)
//	MOVV R1, 0(R3)           MOV X10, X6
//	MOVV R4, R13             MOV X11, X10
//	MOVV R5, R4              JALR RA, (X6)
//	JAL (R13)                MOV 0(SP), RA
//	MOVV 0(R3), R1           MOV 8(SP), X5
//	MOVV 8(R3), R12          MOV X5, SP
//	MOVV R12, R3             RET
//	RET
var parityThunk = map[string][]uint32{
	"loong64": {
		0x0015006c, 0x02ff0063, 0x02ffc00d, 0x0014b463, 0x00109863, 0x29c0206c, 0x29c00061,
		0x0015008d, 0x001500a4, 0x4c0001a1, 0x28c00061, 0x28c0206c, 0x00150183, 0x4c000020,
	},
	"riscv64": {
		0x00010293, 0xfc010113, 0xff017113, 0x00c10133, 0x00513423, 0x00113023, 0x00050313,
		0x00058513, 0x000300e7, 0x00013083, 0x00813283, 0x00028113, 0x00008067,
	},
}

func TestForeignGatewayBothStackParities(t *testing.T) {
	// Independent C callee returns SP%16 and the eighth register argument.
	words := []uint32{0x03403c64, 0x00150165, 0x4c000020}
	if runtime.GOARCH == "riscv64" {
		words = []uint32{0x00f17513, 0x00088593, 0x00008067}
	}
	entry := mapWords(t, words)
	thunk := mapWords(t, parityThunk[runtime.GOARCH])
	for _, parity := range []uintptr{0, 8} {
		block := &alignedForeignCall{target: entry, args: ArgumentWords{101, 102, 103, 104, 105, 106, 107, 108}}
		purego.SyscallN(thunk, alignedForeignAddress, uintptr(unsafe.Pointer(block)), parity)
		runtime.KeepAlive(block)
		if block.low != 0 || block.high != 108 {
			t.Errorf("incoming SP%%16=%d: outgoing SP%%16=%d eighth arg=%d", parity, block.low, block.high)
		}
	}
}
