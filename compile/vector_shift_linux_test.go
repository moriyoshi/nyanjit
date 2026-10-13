//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"runtime"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func immediateVectorShift(b *nyanjit.Builder, op string, x nyanjit.Value, count uint8) nyanjit.Value {
	switch op {
	case "shl":
		return b.ShiftLeft(x, count)
	case "shr":
		return b.ShiftRight(x, count)
	case "rol":
		return b.RotateLeft(x, count)
	case "ror":
		return b.RotateRight(x, count)
	}
	panic(op)
}

// Batch uint8 counts into small programs. One enormous scalarized function
// makes register allocation expensive, particularly on emulated 32-bit hosts.
func TestSIMDImmediateShiftDifferential(t *testing.T) {
	for _, shape := range []struct {
		lanes uint16
		bits  uint8
	}{{4, 32}, {8, 32}, {16, 32}, {2, 64}, {8, 16}, {16, 8}} {
		for _, op := range []string{"shl", "shr", "rol", "ror"} {
			for _, scalar := range []bool{false, true} {
				t.Run(fmt.Sprintf("%dx%d/%s/scalar=%t", shape.lanes, shape.bits, op, scalar), func(t *testing.T) {
					const batchSize = 16
					for first := 0; first < 256; first += batchSize {
						t.Run(fmt.Sprintf("counts=%d-%d", first, first+batchSize-1), func(t *testing.T) {
							random := rand.New(rand.NewPCG(0x7157, uint64(shape.lanes)<<8|uint64(shape.bits)))
							typ := ir.Vec(shape.lanes, shape.bits)
							size := int(shape.lanes) * int(shape.bits) / 8
							b := nyanjit.NewBuilder()
							in, out := b.ParamNative(), b.ParamNative()
							x := b.VLoad(typ, in)
							for count := first; count < first+batchSize; count++ {
								b.VStore(b.Add(out, b.ConstNative(uintptr((count-first)*size))), immediateVectorShift(b, op, x, uint8(count)))
							}
							b.Return(b.ConstNative(0))
							p, err := testFinishRaw(b)
							if err != nil {
								t.Fatal(err)
							}
							var spec compile.TargetSpec
							opts := compile.Func{ABI: abi.C}
							if runtime.GOARCH == "386" && shape.bits == 64 {
								// Paired 64-bit shifts use the existing scalar helper-call
								// path, which requires a platform stack frame on 386.
								opts.Stack = compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}
							}
							if scalar {
								spec.Vectors = compile.VectorsOff()
							}
							code, err := nyanjit.Compile(p, spec, opts)
							if err != nil {
								t.Fatal(err)
							}
							defer code.Close()
							m, err := execution.Scratch(size*(batchSize+1) + 3)
							if err != nil {
								t.Fatal(err)
							}
							defer execution.FreeScratch(m)
							input, output := m[1:1+size], m[2+size:2+size+batchSize*size]
							address := uintptr(unsafe.Pointer(&m[0]))
							step := int(shape.bits) / 8
							mask := ^uint64(0) >> (64 - shape.bits)
							for sample := 0; sample < 5; sample++ {
								for i := range m {
									m[i] = 0xa5
								}
								for lane := 0; lane < int(shape.lanes); lane++ {
									value := random.Uint64()
									if sample < 2 {
										value = []uint64{0, mask, 1, uint64(1) << (shape.bits - 1)}[(lane+2*sample)%4]
									}
									writeSIMDLane(input[lane*step:], shape.bits, value)
								}
								original := bytes.Clone(input)
								if _, err := code.Call(address+1, address+uintptr(2+size)); err != nil {
									t.Fatal(err)
								}
								if m[0] != 0xa5 || m[1+size] != 0xa5 || m[len(m)-1] != 0xa5 || !bytes.Equal(input, original) {
									t.Fatal("shift overwrote input or guard")
								}
								for count := first; count < first+batchSize; count++ {
									for lane := 0; lane < int(shape.lanes); lane++ {
										x := readSIMDLane(original[lane*step:], shape.bits)
										want := scalarSIMDResult(op, shape.bits, x, 0, uint64(count))
										got := readSIMDLane(output[(count-first)*size+lane*step:], shape.bits)
										if got != want {
											t.Fatalf("sample=%d lane=%d count=%d x=%x: got %x want %x", sample, lane, count, x, got, want)
										}
									}
								}
							}
						})
					}

				})
			}
		}
	}
}

// Self-feeding chains force destructive destinations and, with twenty live
// vectors, spilling. Replay the exact chain independently for each input lane.
func TestSIMDImmediateShiftPressureChains(t *testing.T) {
	type operation struct {
		op    string
		count uint8
		slot  int
	}
	for trial := 0; trial < 24; trial++ {
		random := rand.New(rand.NewPCG(71, uint64(trial)))
		lanes := uint16(4 + 4*(trial%2))
		live := 1 + random.IntN(20)
		if trial < 4 {
			live = 20
		}
		scalar := trial%4 >= 2
		t.Run(fmt.Sprintf("trial=%d/lanes=%d/live=%d/scalar=%t", trial, lanes, live, scalar), func(t *testing.T) {
			size := int(lanes) * 4
			b := nyanjit.NewBuilder()
			in, out := b.ParamNative(), b.ParamNative()
			values := make([]nyanjit.Value, live)
			for i := range values {
				values[i] = b.VLoad(ir.Vec(lanes, 32), b.Add(in, b.ConstNative(uintptr(i*size))))
			}
			chain := make([]operation, 40+random.IntN(21))
			for i := range chain {
				k := operation{[]string{"shl", "shr", "rol", "ror"}[random.IntN(4)], []uint8{0, 1, 3, 7, 13, 18, 25, 31, 32, 33, 40, 255}[random.IntN(12)], i % live}
				chain[i] = k
				values[k.slot] = immediateVectorShift(b, k.op, values[k.slot], k.count)
			}
			for i, v := range values {
				b.VStore(b.Add(out, b.ConstNative(uintptr(i*size))), v)
			}
			b.Return(b.ConstNative(0))
			p, err := testFinishRaw(b)
			if err != nil {
				t.Fatal(err)
			}
			var spec compile.TargetSpec
			if scalar {
				spec.Vectors = compile.VectorsOff()
			}
			code, err := nyanjit.Compile(p, spec, compile.Func{ABI: abi.C})
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			m, err := execution.Scratch(2 * live * size)
			if err != nil {
				t.Fatal(err)
			}
			defer execution.FreeScratch(m)
			for i := 0; i < live*int(lanes); i++ {
				writeSIMDLane(m[i*4:], 32, random.Uint64())
			}
			expected := bytes.Clone(m[:live*size])
			for _, k := range chain {
				for lane := 0; lane < int(lanes); lane++ {
					offset := k.slot*size + lane*4
					x := readSIMDLane(expected[offset:], 32)
					writeSIMDLane(expected[offset:], 32, scalarSIMDResult(k.op, 32, x, 0, uint64(k.count)))
				}
			}
			address := uintptr(unsafe.Pointer(&m[0]))
			if _, err := code.Call(address, address+uintptr(live*size)); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(m[live*size:], expected) {
				t.Fatalf("chain mismatch\ngot  %x\nwant %x", m[live*size:], expected)
			}
		})
	}
}
