//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"bytes"
	"fmt"
	"runtime"
	"testing"
	"unsafe"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// Check the public arbitrary-alignment contract, truncation of paired 64-bit
// values on 386, widening, and the exact set of bytes changed by stores.
func TestNarrowMemoryRoundTrip(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		for _, bits := range []uint8{16, 32} {
			for _, typed := range []bool{false, true} {
				for _, addressBits := range []uint8{builder.NativeBits, 64} {
					t.Run(fmt.Sprintf("abi%d/bits%d/typed%v/address%d", flavor, bits, typed, addressBits), func(t *testing.T) {
						b := n.NewBuilder()
						a, v := b.ParamBits(addressBits), b.ParamBits(64)
						var loaded n.Value
						if typed {
							b.StoreBits(a, b.CastUnsigned(v, bits))
							loaded = b.LoadBits(a, bits)
						} else if bits == 16 {
							b.Store16(a, v)
							loaded = b.Load16(a)
						} else {
							b.Store32(a, v)
							loaded = b.Load32(a)
						}
						if loaded.Bits() != uint16(bits) {
							t.Fatalf("load width %d", loaded.Bits())
						}
						b.Return(b.CastUnsigned(loaded, 64))
						p, e := b.Finish()
						if e != nil {
							t.Fatal(e)
						}
						c, e := n.Compile(p, compile.TargetSpec{}, structParamOptions(flavor))
						if e != nil {
							t.Fatal(e)
						}
						defer c.Close()
						data := bytes.Repeat([]byte{0xa5}, 80)
						var pin runtime.Pinner
						pin.Pin(&data[0])
						defer pin.Unpin()
						for offset := 8; offset < 16; offset++ {
							for _, v := range []uint64{0, 1, 0xfeedbeef89abcdef, ^uint64(0)} {
								for i := range data {
									data[i] = 0xa5
								}
								want := append([]byte(nil), data...)
								for i := 0; i < int(bits/8); i++ {
									want[offset+i] = byte(v >> uint(8*i))
								}
								a := uintptr(unsafe.Pointer(&data[offset]))
								args := execution.Words(a, v)
								if addressBits == 64 {
									args = execution.Words(uint64(a), v)
								}
								lo, hi, e := c.CallPair(args...)
								mask := uint64(1)<<bits - 1
								if e != nil || execution.Wide(lo, hi) != v&mask || !bytes.Equal(data, want) {
									t.Fatalf("offset%d value%x got%x data%x want%x err%v", offset, v, execution.Wide(lo, hi), data, want, e)
								}
							}
						}
					})
				}
			}
		}
	}
}

// Many simultaneously live narrow loads force scalar spill/reload paths. The
// Data accesses are aligned, so this also executes every native narrow opcode
// on strict-alignment targets. Stores consume reloaded narrow operands.
func TestNarrowMemoryDataPressure(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		for _, bits := range []uint8{16, 32} {
			t.Run(fmt.Sprintf("abi%d/bits%d", flavor, bits), func(t *testing.T) {
				b := n.NewBuilder()
				var fields []n.Data
				if bits == 16 {
					d := n.PointerParam[[40]uint16](b)
					for i := 0; i < 40; i++ {
						fields = append(fields, d.At(fmt.Sprintf("[%d]", i)))
					}
				} else {
					d := n.PointerParam[[40]uint32](b)
					for i := 0; i < 40; i++ {
						fields = append(fields, d.At(fmt.Sprintf("[%d]", i)))
					}
				}
				b.Enter()
				values := make([]n.Value, len(fields))
				for i, f := range fields {
					values[i] = f.Load()
				}
				b.Leave(values...)
				sum := b.Const(0)
				for i, v := range values {
					fields[i].Store(v)
					sum = b.Add(sum, b.CastUnsigned(v, 64))
				}
				b.Return(sum)
				p, e := b.Finish()
				if e != nil {
					t.Fatal(e)
				}
				report, e := lowerReport(p, compile.TargetSpec{}, structParamOptions(flavor))
				if e != nil {
					t.Fatal(e)
				}
				if report.SpillStorage() == abi.SpillNone {
					t.Fatal("pressure fixture did not spill")
				}
				if !report.ExplicitMemoryReads() || !report.ExplicitMemoryWrites() || !report.MayReadMemory() || !report.MayWriteMemory() {
					t.Fatalf("missing memory effects: %+v", report.Snapshot())
				}
				c, e := n.Compile(p, compile.TargetSpec{}, structParamOptions(flavor))
				if e != nil {
					t.Fatal(e)
				}
				defer c.Close()
				var a16 [40]uint16
				var a32 [40]uint32
				var want uint64
				for i := range a16 {
					a16[i] = uint16(0x8000 + i*17)
					a32[i] = uint32(0x80000000) + uint32(i*17)
					if bits == 16 {
						want += uint64(a16[i])
					} else {
						want += uint64(a32[i])
					}
				}
				original16, original32 := a16, a32
				var arg any = &a32
				if bits == 16 {
					arg = &a16
				}
				lo, hi, e := c.CallPair(execution.Words(arg)...)
				if e != nil || execution.Wide(lo, hi) != want || a16 != original16 || a32 != original32 {
					t.Fatalf("got%x want%x err%v", execution.Wide(lo, hi), want, e)
				}
			})
		}
	}
}

// Exercise explicit byte order and a load before an overlapping store. The
// loaded value must survive legalization and memory scheduling unchanged.
func TestNarrowMemoryEndianAndOrdering(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		p := &ir.Program{Arity: 2, Result: 6, Instructions: []ir.Instruction{
			{Op: ir.OpParam, Bits: builder.NativeBits}, {Op: ir.OpParam, Bits: 64},
			{Op: ir.OpConst, Bits: builder.NativeBits, Imm: 1}, {Op: ir.OpAdd, Bits: builder.NativeBits, A: 0, B: 2},
			{Op: ir.OpLoad32, Bits: 32, A: 0, ByteOrder: ir.ByteOrderBigEndian},
			{Op: ir.OpStore16, Bits: 16, A: 3, B: 1, ByteOrder: ir.ByteOrderBigEndian},
			{Op: ir.OpCastUnsigned, Bits: 64, A: 4},
		}}
		c, e := n.Compile(p, compile.TargetSpec{}, structParamOptions(flavor))
		if e != nil {
			t.Fatal(e)
		}
		defer c.Close()
		data := []byte{0x81, 0xfe, 0xab, 0xcd, 0x55}
		var pin runtime.Pinner
		pin.Pin(&data[0])
		defer pin.Unpin()
		lo, hi, e := c.CallPair(execution.Words(uintptr(unsafe.Pointer(&data[0])), uint64(0xbeef000080ff))...)
		if e != nil || execution.Wide(lo, hi) != 0x81feabcd || !bytes.Equal(data, []byte{0x81, 0x80, 0xff, 0xcd, 0x55}) {
			t.Fatalf("got%x data%x err%v", execution.Wide(lo, hi), data, e)
		}
	}
}
