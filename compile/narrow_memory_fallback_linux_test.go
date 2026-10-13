//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile

import (
	"bytes"
	"fmt"
	"runtime"
	"strconv"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/ir"
)

// Execute the byte retry plan directly so the fallback's truncation, endian
// handling, and exact write footprint are checked even on roomy native hosts.
func TestNarrowMemoryByteFallbackRoundTrip(t *testing.T) {
	for _, bits := range []uint8{16, 32} {
		for _, order := range []ir.ByteOrder{ir.ByteOrderLittleEndian, ir.ByteOrderBigEndian} {
			t.Run(fmt.Sprintf("bits%d/order%d", bits, order), func(t *testing.T) {
				load, store := ir.OpLoad16, ir.OpStore16
				if bits == 32 {
					load, store = ir.OpLoad32, ir.OpStore32
				}
				program := &ir.Program{Arity: 2, Result: 4, Instructions: []ir.Instruction{
					{Op: ir.OpParam, Bits: strconv.IntSize},
					{Op: ir.OpParam, Bits: 64},
					{Op: store, Bits: bits, A: 0, B: 1, ByteOrder: order},
					{Op: load, Bits: bits, A: 0, ByteOrder: order},
					{Op: ir.OpCastUnsigned, Bits: 64, A: 3},
				}}
				spec := TargetSpec{Architecture: runtime.GOARCH}
				f := Func{Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}}
				fallback, _, err := legalizeVectorsWithMemoryFallback(program, mustTarget(t, spec).options(f), true)
				if err != nil {
					t.Fatal(err)
				}
				code, err := compileCode(fallback, spec, f)
				if err != nil {
					t.Fatal(err)
				}
				defer code.Close()
				for _, value := range []uint64{0, 1, 0xfeedbeef89abcdef, ^uint64(0)} {
					data := bytes.Repeat([]byte{0xa5}, 16)
					want := append([]byte(nil), data...)
					for offset := 0; offset < int(bits/8); offset++ {
						shift := offset * 8
						if order == ir.ByteOrderBigEndian {
							shift = int(bits) - 8 - shift
						}
						want[3+offset] = byte(value >> shift)
					}
					args := execution.Words(&data[3], value)
					low, high, err := code.CallPair(args...)
					if err != nil || execution.Wide(low, high) != value&(uint64(1)<<bits-1) || !bytes.Equal(data, want) {
						t.Fatalf("value%x got%x data%x want%x error%v", value, execution.Wide(low, high), data, want, err)
					}
					runtime.KeepAlive(args)
				}
			})
		}
	}
}
