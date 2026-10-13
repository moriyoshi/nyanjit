//go:build linux && 386

package compile_test

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestRound13MemoryExecution386(t *testing.T) {
	for _, pair := range []struct {
		load, store ir.Opcode
		bits        uint8
	}{{ir.OpLoad8, ir.OpStore8, 8}, {ir.OpLoad16, ir.OpStore16, 16}, {ir.OpLoad32, ir.OpStore32, 32}, {ir.OpLoad64, ir.OpStore64, 64}} {
		for _, op := range []ir.Opcode{pair.load, pair.store} {
			for _, width := range []uint8{8, 16, 32, 64} {
				load := op == pair.load
				if load && pair.bits == 8 && (width == 16 || width == 32) {
					continue
				}
				if pair.bits != 8 && (load && width != pair.bits || !load && width != pair.bits && width != 64) {
					continue
				}
				for _, addressBits := range []uint8{32, 64} {
					for _, param := range []bool{false, true} {
						for _, result := range []round13Result{round13OperationResult, round13AddressResult, round13CombinedResult} {
							for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
								for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
									for _, harden := range []bool{false, true} {
										name := fmt.Sprintf("%s/w%d/a%d/param%v/result%d/stack%d/abi%d/harden%v", op, width, addressBits, param, result, stack, flavor, harden)
										t.Run(name, func(t *testing.T) {
											data := []byte{0x5a, 0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0, 0xa5}
											original := [10]byte{}
											copy(original[:], data)
											var pin runtime.Pinner
											pin.Pin(&data[0])
											defer pin.Unpin()
											address := uint64(uintptr(unsafe.Pointer(&data[1])))
											if addressBits == 64 {
												address |= 0xabcdef1200000000
											}
											value := uint64(0x11223344556677cd) & (^uint64(0) >> (64 - width))
											p := round13MemoryProgram(op, pair.bits, width, addressBits, param, result)
											if !param {
												p.Instructions[0].Imm = address
												p.Instructions[1].Imm = value
											}
											options := compile.Func{ABI: flavor, Stack: compile.StackPolicy{Practice: stack}, Harden: harden}
											if stack == abi.StackPlatform {
												options.Stack.BytesAvailable = 8192
											}
											code, err := nyanjit.Compile(p, compile.TargetSpec{}, options)
											if err != nil {
												t.Fatal(err)
											}
											defer code.Close()
											var args []uintptr
											if param {
												var a, v any = uint32(address), value
												if addressBits == 64 {
													a = address
												}
												switch width {
												case 8:
													v = uint8(value)
												case 16:
													v = uint16(value)
												case 32:
													v = uint32(value)
												}
												args = execution.Words(a, v)
											}
											// Raw StackPlatform code runs on the goroutine stack; grow it
											// past StackBytesAvailable first.
											securityGrowStack(16)
											lo, hi, err := code.CallPair(args...)
											if err != nil {
												t.Fatal(err)
											}
											want := value
											if load {
												want = binary.LittleEndian.Uint64(original[1:9]) & (^uint64(0) >> (64 - pair.bits))
											}
											switch result {
											case round13AddressResult:
												want = address
											case round13CombinedResult:
												want ^= address
											}
											if got := execution.Wide(lo, hi); got != want {
												t.Fatalf("result %#x, want %#x", got, want)
											}
											expected := original
											if !load {
												for offset := 0; offset < int(pair.bits/8); offset++ {
													expected[offset+1] = byte(value >> (8 * offset))
												}
											}
											if string(data) != string(expected[:]) {
												t.Fatalf("memory %x, want %x", data, expected)
											}
										})
									}
								}
							}
						}
					}
				}
			}
		}
	}
}
