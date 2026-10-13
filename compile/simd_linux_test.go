//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"bytes"
	"encoding/binary"
	"math/rand/v2"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func simdMemory(t *testing.T) ([]byte, uintptr) {
	t.Helper()
	m, err := execution.Scratch(4096)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { execution.FreeScratch(m) })
	return m, uintptr(unsafe.Pointer(&m[0]))
}
func nativeLaneOrder() binary.ByteOrder { return binary.LittleEndian }
func readSIMDLane(data []byte, bits uint8) uint64 {
	switch bits {
	case 8:
		return uint64(data[0])
	case 16:
		return uint64(nativeLaneOrder().Uint16(data))
	case 32:
		return uint64(nativeLaneOrder().Uint32(data))
	case 64:
		return nativeLaneOrder().Uint64(data)
	}
	panic("bad lane")
}
func writeSIMDLane(data []byte, bits uint8, value uint64) {
	switch bits {
	case 8:
		data[0] = byte(value)
	case 16:
		nativeLaneOrder().PutUint16(data, uint16(value))
	case 32:
		nativeLaneOrder().PutUint32(data, uint32(value))
	case 64:
		nativeLaneOrder().PutUint64(data, value)
	}
}
func scalarSIMDResult(operation string, width uint8, x, y, count uint64) uint64 {
	mask := ^uint64(0) >> (64 - width)
	x, y = x&mask, y&mask
	switch operation {
	case "add":
		return (x + y) & mask
	case "sub":
		return (x - y) & mask
	case "mul":
		return (x * y) & mask
	case "and":
		return x & y
	case "or":
		return x | y
	case "xor":
		return x ^ y
	case "shl":
		return x << count & mask
	case "shr":
		return x >> count
	case "sar":
		return uint64((int64(x<<(64-width))>>(64-width))>>count) & mask
	case "rol":
		count %= uint64(width)
		return (x<<count | x>>(uint64(width)-count)) & mask
	case "ror":
		count %= uint64(width)
		return (x>>count | x<<(uint64(width)-count)) & mask
	}
	panic("bad operation")
}
func TestSIMDIntegerDifferential(t *testing.T) {
	m, address := simdMemory(t)
	random := rand.New(rand.NewPCG(789, 123))
	operations := []string{"add", "sub", "mul", "and", "or", "xor", "shl", "shr", "sar", "rol", "ror"}
	for _, width := range []uint8{8, 16, 32, 64} {
		for _, total := range []uint16{128, 256, 512} {
			for _, op := range operations {
				shape := ir.Vec(total/uint16(width), width)
				b := nyanjit.NewBuilder()
				out, a, c, count := b.ParamNative(), b.ParamNative(), b.ParamNative(), b.Param()
				x, y := b.VLoad(shape, a), b.VLoad(shape, c)
				var result nyanjit.Value
				switch op {
				case "add":
					result = x.Add(y)
				case "sub":
					result = b.Sub(x, y)
				case "mul":
					result = b.Mul(x, y)
				case "and":
					result = b.And(x, y)
				case "or":
					result = b.Or(x, y)
				case "xor":
					result = b.Xor(x, y)
				case "shl":
					result = b.ShiftLeftBy(x, count)
				case "shr":
					result = b.ShiftRightBy(x, count)
				case "sar":
					result = b.ShiftRightArithmeticBy(x, count)
				case "rol":
					result = b.RotateLeftBy(x, count)
				case "ror":
					result = b.RotateRightBy(x, count)
				}
				b.VStore(out, result)
				b.Return(b.Literal(0))
				program, err := testFinishRaw(b)
				if err != nil {
					t.Fatal(err)
				}
				codes := []*execution.Code{}
				for _, vectors := range []compile.VectorPolicy{compile.VectorsAuto(), compile.VectorsOff()} {
					code, err := nyanjit.Compile(program, compile.TargetSpec{Vectors: vectors}, compile.Func{Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
					if err != nil {
						t.Fatalf("width%d total%d %s: %v", width, total, op, err)
					}
					codes = append(codes, code)
				}
				size, step := int(total/8), int(width/8)
				for sample, count := range []uint64{0, 1, uint64(width - 1), uint64(width), uint64(width + 1), 128, 256, 1 << 63, ^uint64(0)} {
					for n := 0; n < size; n += step {
						writeSIMDLane(m[65+n:], width, random.Uint64())
						writeSIMDLane(m[193+n:], width, random.Uint64())
						if n/step < 4 {
							mask := ^uint64(0) >> (64 - width)
							minimum := uint64(1) << (width - 1)
							edge := (n/step + sample) % 4
							writeSIMDLane(m[65+n:], width, []uint64{0, mask, minimum, minimum}[edge])
							writeSIMDLane(m[193+n:], width, []uint64{mask, 1, mask, minimum}[edge])
						}
					}
					want := make([]byte, size)
					for n := 0; n < size; n += step {
						writeSIMDLane(want[n:], width, scalarSIMDResult(op, width, readSIMDLane(m[65+n:], width), readSIMDLane(m[193+n:], width), count))
					}
					for mode, code := range codes {
						for n := 320; n < 320+size+2; n++ {
							m[n] = 0xa5
						}
						_, err := code.Call(append([]uintptr{address + 321, address + 65, address + 193}, execution.Words(count)...)...)
						if err != nil {
							t.Fatal(err)
						}
						if !bytes.Equal(m[321:321+size], want) || m[320] != 0xa5 || m[321+size] != 0xa5 {
							t.Fatalf("%s width%d total%d count%#x mode%d\ngot %x\nwant%x", op, width, total, count, mode, m[321:321+size], want)
						}
					}
				}
				for _, code := range codes {
					code.Close()
				}
			}
		}
	}
}
func TestSIMDByteMask9249AndRandomMasks(t *testing.T) {
	m, address := simdMemory(t)
	program := simdExample(t, 3)
	random := rand.New(rand.NewPCG(321, 654))
	for _, vectors := range []compile.VectorPolicy{compile.VectorsAuto(), compile.VectorsOff()} {
		template, err := lowerTemplate(program, compile.TargetSpec{Vectors: vectors}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
		if err != nil {
			t.Fatal(err)
		}
		code, err := template.Bind(nil)
		if err != nil {
			t.Fatal(err)
		}
		for sample := 0; sample < 80; sample++ {
			want := uint64(0)
			needle := byte(9)
			for i := 0; i < 16; i++ {
				m[1+i] = byte(random.Uint32())
				if sample == 0 {
					m[1+i] = 0
				}
				if sample == 0 && (i == 0 || i == 5 || i == 10 || i == 13) {
					m[1+i] = needle
				}
				if m[1+i] == needle {
					want |= 1 << i
				}
			}
			if sample == 0 && want != 9249 {
				t.Fatal("bad review vector")
			}
			got, err := code.Call(execution.Words(uint64(address+1), uintptr(needle)|0xffff00)...)
			if err != nil || uint64(got) != want {
				t.Fatalf("mask sample%d got%d,%v;want%d", sample, got, err, want)
			}
		}
		report := template.Report()
		if len(report.VectorOperations()) != 4 {
			t.Fatalf("missing semantic lowering entries: %+v", report.VectorOperations())
		}
		if vectors == compile.VectorsOff() {
			for _, entry := range report.VectorOperations() {
				if entry.Lowering != abi.Scalarized {
					t.Fatal("forced scalar operation reported native")
				}
			}
		}
		if len(report.VectorOperations()) > 0 {
			report.VectorOperations()[0].Position = -100
			if template.Report().VectorOperations()[0].Position == -100 {
				t.Fatal("report shares mutable vector entries")
			}
		}
		code.Close()
	}
}
func TestSIMDConstantsInsertionAndLaneExtraction(t *testing.T) {
	m, address := simdMemory(t)
	for _, width := range []uint8{8, 16, 32, 64} {
		for _, vectors := range []compile.VectorPolicy{compile.VectorsAuto(), compile.VectorsOff()} {
			shape := ir.Vec(128/uint16(width), width)
			literal := make([]byte, 16)
			for i := range literal {
				literal[i] = byte(i + 1)
			}
			b := nyanjit.NewBuilder()
			out := b.ParamNative()
			v := b.ConstVector(shape, literal)
			literal[0] = 0 // Builder must own its data.
			last := shape.Lanes - 1
			v = b.InsertLane(v, last, b.ConstBits(width, ^uint64(0)))
			b.VStore(out, v)
			b.Return(b.ExtractLane(v, last))
			program, err := testFinishRaw(b)
			if err != nil {
				t.Fatal(err)
			}
			code, err := nyanjit.Compile(program, compile.TargetSpec{Vectors: vectors}, compile.Func{Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
			if err != nil {
				t.Fatal(err)
			}
			lo, hi, err := code.CallPair(address + 1)
			got := uint64(lo)
			if width == 64 {
				got = execution.Wide(lo, hi)
			}
			mask := ^uint64(0) >> (64 - width)
			if err != nil || got != mask {
				t.Fatalf("insert/extract width%d got%#x,%v;want%#x", width, got, err, mask)
			}
			for lane := 0; lane < int(shape.Lanes)-1; lane++ {
				var bytes [8]byte
				for n := 0; n < int(width/8); n++ {
					bytes[n] = byte(lane*int(width/8) + n + 1)
				}
				want := binary.LittleEndian.Uint64(bytes[:])
				if readSIMDLane(m[1+lane*int(width/8):], width) != want {
					t.Fatal("canonical constant lane bytes changed")
				}
			}
			code.Close()
		}
	}
}

func TestSIMDSignedUnsignedComparisonMasks(t *testing.T) {
	m, address := simdMemory(t)
	random := rand.New(rand.NewPCG(421, 999))
	for _, width := range []uint8{8, 16, 32, 64} {
		for _, condition := range []ir.BranchCondition{ir.BranchEqual, ir.BranchNotEqual, ir.BranchLess, ir.BranchGreaterEqual, ir.BranchLessEqual, ir.BranchGreater, ir.BranchBelow, ir.BranchAboveEqual, ir.BranchBelowEqual, ir.BranchAbove} {
			shape := ir.Vec(128/uint16(width), width)
			b := nyanjit.NewBuilder()
			a, c := b.ParamNative(), b.ParamNative()
			b.Return(b.MaskBits(b.VCompare(b.VLoad(shape, a), condition, b.VLoad(shape, c))))
			p, err := testFinishRaw(b)
			if err != nil {
				t.Fatal(err)
			}
			for _, vectors := range []compile.VectorPolicy{compile.VectorsAuto(), compile.VectorsOff()} {
				code, err := nyanjit.Compile(p, compile.TargetSpec{Vectors: vectors}, compile.Func{Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
				if err != nil {
					t.Fatal(err)
				}
				mask := ^uint64(0) >> (64 - width)
				for sample := 0; sample < 40; sample++ {
					want := uint64(0)
					for n := 0; n < int(shape.Lanes); n++ {
						x := []uint64{0, 1, mask, 1 << (width - 1)}[n%4]
						y := []uint64{mask, 1, 0, 1 << (width - 1)}[n%4]
						if sample > 0 {
							x, y = random.Uint64()&mask, random.Uint64()&mask
							if n%4 == 0 {
								y = x
							}
						}
						writeSIMDLane(m[1+n*int(width/8):], width, x)
						writeSIMDLane(m[129+n*int(width/8):], width, y)
						sx, sy := int64(x<<(64-width))>>(64-width), int64(y<<(64-width))>>(64-width)
						match := false
						switch condition {
						case ir.BranchEqual:
							match = x == y
						case ir.BranchNotEqual:
							match = x != y
						case ir.BranchLess:
							match = sx < sy
						case ir.BranchGreaterEqual:
							match = sx >= sy
						case ir.BranchLessEqual:
							match = sx <= sy
						case ir.BranchGreater:
							match = sx > sy
						case ir.BranchBelow:
							match = x < y
						case ir.BranchAboveEqual:
							match = x >= y
						case ir.BranchBelowEqual:
							match = x <= y
						case ir.BranchAbove:
							match = x > y
						}
						if match {
							want |= 1 << n
						}
					}
					got, err := code.Call(address+1, address+129)
					if err != nil || uint64(got) != want {
						t.Fatalf("comparison width%d condition%d got%#x,%v;want%#x", width, condition, got, err, want)
					}
				}
				code.Close()
			}
		}
	}
}

func TestSIMDPerLaneCountsImmediateCountsAndShortShape(t *testing.T) {
	m, address := simdMemory(t)
	for _, shape := range []ir.Type{ir.Vec(3, 32), ir.Vec(16, 8), ir.Vec(2, 64)} {
		width, step := shape.LaneBits, int(shape.LaneBits/8)
		for _, mode := range []string{"per-lane", "immediate", "mul-const"} {
			b := nyanjit.NewBuilder()
			out, a, c := b.ParamNative(), b.ParamNative(), b.ParamNative()
			x := b.VLoad(shape, a)
			var result nyanjit.Value
			switch mode {
			case "per-lane":
				result = b.ShiftRightArithmeticBy(x, b.VLoad(shape, c))
			case "immediate":
				result = b.RotateRight(x, 129)
			case "mul-const":
				result = x.MulConst(3)
			}
			b.VStore(out, result)
			b.Return(b.Literal(0))
			p, err := testFinishRaw(b)
			if err != nil {
				t.Fatal(err)
			}
			for _, vectors := range []compile.VectorPolicy{compile.VectorsAuto(), compile.VectorsOff()} {
				code, err := nyanjit.Compile(p, compile.TargetSpec{Vectors: vectors}, compile.Func{Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
				if err != nil {
					t.Fatal(err)
				}
				for n := 0; n < int(shape.Lanes); n++ {
					writeSIMDLane(m[1+n*step:], width, 0x8123456789abcdef)
					writeSIMDLane(m[129+n*step:], width, uint64(n)*uint64(width))
				}
				_, err = code.Call(address+257, address+1, address+129)
				if err != nil {
					t.Fatal(err)
				}
				for n := 0; n < int(shape.Lanes); n++ {
					operation, count := "sar", uint64(n)*uint64(width)
					if mode == "immediate" {
						operation, count = "ror", 129
					}
					if mode == "mul-const" {
						operation = "mul"
					}
					want := scalarSIMDResult(operation, width, 0x8123456789abcdef, 3, count)
					got := readSIMDLane(m[257+n*step:], width)
					if got != want {
						t.Fatalf("%s shape%+v lane%d got%#x;want%#x", mode, shape, n, got, want)
					}
				}
				code.Close()
			}
		}
	}
}

func TestSIMDOverlappingAndInPlaceBuffers(t *testing.T) {
	m, address := simdMemory(t)
	for _, width := range []uint8{8, 16, 32, 64} {
		for _, total := range []uint16{128, 256, 512} {
			b := nyanjit.NewBuilder()
			out, a, c := b.ParamNative(), b.ParamNative(), b.ParamNative()
			typ := ir.Vec(total/uint16(width), width)
			b.VStore(out, b.Sub(b.VLoad(typ, a), b.VLoad(typ, c)))
			b.Return(b.Literal(0))
			p, err := testFinishRaw(b)
			if err != nil {
				t.Fatal(err)
			}
			size, step := int(total/8), int(width/8)
			for _, vectors := range []compile.VectorPolicy{compile.VectorsAuto(), compile.VectorsOff()} {
				code, err := nyanjit.Compile(p, compile.TargetSpec{Vectors: vectors}, compile.Func{Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
				if err != nil {
					t.Fatal(err)
				}
				for _, dst := range []int{65, 65 - step, 65 + step, 193, 193 - step, 193 + step} {
					for i := range m {
						m[i] = byte(i*37 + 11)
					}
					want := append([]byte(nil), m...)
					for n := 0; n < size; n += step {
						writeSIMDLane(want[dst+n:], width, scalarSIMDResult("sub", width, readSIMDLane(m[65+n:], width), readSIMDLane(m[193+n:], width), 0))
					}
					if _, err := code.Call(address+uintptr(dst), address+65, address+193); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(m, want) {
						t.Fatalf("overlap width%d total%d dst%d native=%v", width, total, dst, vectors == compile.VectorsAuto())
					}
				}
				code.Close()
			}
		}
	}
}
