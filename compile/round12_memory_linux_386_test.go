//go:build linux && 386

package compile

import (
	"fmt"
	"runtime"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestRound12ByteMemoryAddressHalves386(t *testing.T) {
	for _, addressBits := range []uint8{32, 64} {
		for _, valueBits := range []uint8{8, 64} {
			for _, resultBits := range []uint8{8, 64} {
				for _, harden := range []bool{false, true} {
					t.Run(fmt.Sprintf("addr%d/value%d/result%d/harden%v", addressBits, valueBits, resultBits, harden), func(t *testing.T) {
						data := []byte{0x5a, 0, 0xa5}
						var pin runtime.Pinner
						pin.Pin(&data[0])
						defer pin.Unpin()
						value := uint64(0xcd)
						if valueBits == 64 {
							value = 0x11223344556677cd
						}
						p := &ir.Program{Result: 3, Instructions: []ir.Instruction{
							{Op: ir.OpConst, Bits: addressBits, Imm: uint64(uintptr(unsafe.Pointer(&data[1])))},
							{Op: ir.OpConst, Bits: valueBits, Imm: value},
							{Op: ir.OpStore8, Bits: 8, A: 0, B: 1},
							{Op: ir.OpLoad8, Bits: resultBits, A: 0},
						}}
						code, err := compileCode(p, TargetSpec{}, Func{ABI: abi.C, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}, Harden: harden})
						if err != nil {
							t.Fatal(err)
						}
						defer code.Close()
						lo, hi, err := code.CallPair()
						if got := execution.Wide(lo, hi); err != nil || got != 0xcd || data[0] != 0x5a || data[1] != 0xcd || data[2] != 0xa5 {
							t.Fatalf("got%#x,%v bytes%x", got, err, data)
						}
					})
				}
			}
		}
	}
}
