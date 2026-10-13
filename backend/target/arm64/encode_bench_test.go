package arm64

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func benchmarkBinaryImmediate(b *testing.B, operation op.Opcode, value uint64) {
	e := NewByteAssembler(abi.StackNoUse, 0)
	e.words = make([]uint32, 0, 1024)
	b.ReportAllocs()
	for b.Loop() {
		if len(e.words) == cap(e.words) {
			e.words = e.words[:0]
		}
		e.BinaryImmediate(operation, 64, 1, 2, value)
	}
	if e.Err() != nil {
		b.Fatal(e.Err())
	}
}

func BenchmarkBinaryImmediateXor(b *testing.B) { benchmarkBinaryImmediate(b, op.OpXor, 0xff) }
func BenchmarkBinaryImmediateAnd(b *testing.B) { benchmarkBinaryImmediate(b, op.OpAnd, 0xff00) }
func BenchmarkBinaryImmediateOr(b *testing.B) {
	benchmarkBinaryImmediate(b, op.OpOr, 0x0f0f0f0f0f0f0f0f)
}
func BenchmarkBinaryImmediateAdd(b *testing.B) { benchmarkBinaryImmediate(b, op.OpAdd, 42) }

func BenchmarkConstant(b *testing.B) {
	e := NewByteAssembler(abi.StackNoUse, 0)
	e.words = make([]uint32, 0, 1024)
	values := [...]uint64{0, 42, 0x123456789abcdef0, ^uint64(0x1234), 0xffff0000ffff}
	b.ReportAllocs()
	n := 0
	for b.Loop() {
		if len(e.words)+4 > cap(e.words) {
			e.words = e.words[:0]
		}
		e.Constant(1, values[n%len(values)])
		n++
	}
}

func BenchmarkPhysicalSizeConstant(b *testing.B) {
	values := [...]uint64{0, 42, 0x123456789abcdef0, ^uint64(0x1234), 0xffff0000ffff}
	b.ReportAllocs()
	n, total := 0, 0
	for b.Loop() {
		total += PhysicalSize(machine.Instruction{Op: machine.OpConst, Imm: values[n%len(values)]}, abi.StackNoUse, 0)
		n++
	}
	if total == 0 {
		b.Fatal("no size")
	}
}
