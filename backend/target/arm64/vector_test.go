package arm64_test

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/arm64"
)

func TestNEONVectorGNUAssemblerWords(t *testing.T) {
	// Independently assembled using GNU as on the arm64 host.
	v := backend.VectorRegister
	for _, test := range []struct {
		ins  machine.Instruction
		word uint32
	}{
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVLoad, A: v(3), B: 1}, 0x3dc00023},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVStore, A: 1, B: v(3)}, 0x3d800023},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpAdd, Bits: 32, A: v(3), B: v(4), C: v(5)}, 0x4ea58483},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpSub, Bits: 64, A: v(3), B: v(4), C: v(5)}, 0x6ee58483},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpMul, Bits: 16, A: v(3), B: v(4), C: v(5)}, 0x4e659c83},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVSplat, Bits: 8, A: v(3), B: 1}, 0x4e010c23},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVExtract, Bits: 8, A: 1, B: v(3), Imm: 7}, 0x0e0f3c61},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVExtract, Bits: 64, A: 1, B: v(3), Imm: 1}, 0x4e183c61},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVInsert, Bits: 16, A: v(3), B: v(3), C: 1, Imm: 7}, 0x4e1e1c23},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVCompare, Bits: 8, A: v(3), B: v(4), C: v(5)}, 0x6e258c83},
	} {
		bytes := arm64.NewByteAssembler(abi.StackNoUse, 0)
		text := arm64.NewTextAssembler(abi.StackNoUse, 0)
		if err := bytes.Vector(test.ins); err != nil {
			t.Fatal(err)
		}
		if err := text.Vector(test.ins); err != nil {
			t.Fatal(err)
		}
		if len(bytes.Bytes()) != 4 || binary.LittleEndian.Uint32(bytes.Bytes()) != test.word {
			t.Fatalf("%+v got%x;want%#x", test.ins, bytes.Bytes(), test.word)
		}
		if text.AssembledSize() != 4 || arm64.VectorSize(test.ins, abi.StackNoUse, 0) != 4 {
			t.Fatal("SIMD PC sizing mismatch")
		}
	}
}

func TestNEONImmediateVectorShifts(t *testing.T) {
	// Independently assembled with GNU as (including SLI/SRI and aliased ORR).
	for _, test := range []struct {
		name     string
		op       op.Opcode
		dst, src uint8
		words    []uint32
		listing  []string
	}{
		{"shl", op.OpShiftLeft, 3, 4, []uint32{0x4f275483}, []string{"shl V3.4S, V4.4S, #7"}},
		{"shr", op.OpShiftRight, 3, 4, []uint32{0x6f390483}, []string{"ushr V3.4S, V4.4S, #7"}},
		{"rol", op.OpRotateLeft, 3, 4, []uint32{0x4f275483, 0x6f274483}, []string{"shl V3.4S, V4.4S, #7", "sri V3.4S, V4.4S, #25"}},
		{"ror", op.OpRotateRight, 3, 4, []uint32{0x6f390483, 0x6f395483}, []string{"ushr V3.4S, V4.4S, #7", "sli V3.4S, V4.4S, #25"}},
		{"rol-alias", op.OpRotateLeft, 3, 3, []uint32{0x6f27047e, 0x4f275463, 0x4ebe1c63}, []string{"ushr V30.4S, V3.4S, #25", "shl V3.4S, V3.4S, #7", "orr V3.16B, V3.16B, V30.16B"}},
		{"ror-alias", op.OpRotateRight, 3, 3, []uint32{0x4f39547e, 0x6f390463, 0x4ebe1c63}, []string{"shl V30.4S, V3.4S, #25", "ushr V3.4S, V3.4S, #7", "orr V3.16B, V3.16B, V30.16B"}},
		{"shl-alias", op.OpShiftLeft, 3, 3, []uint32{0x4f275463}, []string{"shl V3.4S, V3.4S, #7"}},
		{"shr-alias", op.OpShiftRight, 3, 3, []uint32{0x6f390463}, []string{"ushr V3.4S, V3.4S, #7"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ins := machine.Instruction{Op: machine.OpVector, Arithmetic: test.op, Bits: 32, A: backend.VectorRegister(test.dst), B: backend.VectorRegister(test.src), Imm: 7}
			checkNEONShift(t, ins, test.words, test.listing)
		})
	}
	checkNEONShift(t, machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpShiftLeft, Bits: 32, A: backend.VectorRegister(19), B: backend.VectorRegister(29), Imm: 31}, []uint32{0x4f3f57b3}, []string{"shl V19.4S, V29.4S, #31"})
	for _, operation := range []op.Opcode{op.OpShiftLeft, op.OpShiftRight, op.OpRotateLeft, op.OpRotateRight} {
		for _, alias := range []bool{false, true} {
			src := uint8(4)
			words := []uint32{0x4ea41c83}
			listing := []string{"mov V3.16B, V4.16B"}
			if alias {
				src = 3
				words = nil
				listing = nil
			}
			ins := machine.Instruction{Op: machine.OpVector, Arithmetic: operation, Bits: 32, A: backend.VectorRegister(3), B: backend.VectorRegister(src)}
			checkNEONShift(t, ins, words, listing)
			for _, count := range []uint64{32, 33, 64, 255} {
				ins.Imm = count
				if operation == op.OpShiftLeft || operation == op.OpShiftRight {
					checkNEONShift(t, ins, []uint32{0x4f00e403}, []string{"movi V3.16B, #0"})
				} else {
					reduced := ins
					reduced.Imm = count % 32
					b := arm64.NewByteAssembler(abi.StackNoUse, 0)
					if err := b.Vector(reduced); err != nil {
						t.Fatal(err)
					}
					expected := make([]uint32, len(b.Bytes())/4)
					for i := range expected {
						expected[i] = binary.LittleEndian.Uint32(b.Bytes()[i*4:])
					}
					checkNEONShift(t, ins, expected, nil)
				}
			}
		}
		for _, bits := range []uint8{8, 16, 64} {
			ins := machine.Instruction{Op: machine.OpVector, Arithmetic: operation, Bits: bits, A: backend.VectorRegister(3), B: backend.VectorRegister(4), Imm: 7}
			b, text := arm64.NewByteAssembler(abi.StackNoUse, 0), arm64.NewTextAssembler(abi.StackNoUse, 0)
			if !errors.Is(b.Vector(ins), machine.ErrUnsupportedOperation) || !errors.Is(text.Vector(ins), machine.ErrUnsupportedOperation) {
				t.Fatalf("op=%v bits=%d should be unsupported", operation, bits)
			}
			if len(b.Bytes()) != 0 || text.AssembledSize() != 0 {
				t.Fatal("unsupported operation emitted partial output")
			}
		}
	}
}

func checkNEONShift(t *testing.T, ins machine.Instruction, want []uint32, listing []string) {
	t.Helper()
	b, text := arm64.NewByteAssembler(abi.StackNoUse, 0), arm64.NewTextAssembler(abi.StackNoUse, 0)
	if err := b.Vector(ins); err != nil {
		t.Fatal(err)
	}
	if err := text.Vector(ins); err != nil {
		t.Fatal(err)
	}
	if len(b.Bytes()) != len(want)*4 {
		t.Fatalf("%+v: %x, want %x", ins, b.Bytes(), want)
	}
	for i, word := range want {
		if got := binary.LittleEndian.Uint32(b.Bytes()[i*4:]); got != word {
			t.Fatalf("%+v word %d: %08x want %08x", ins, i, got, word)
		}
	}
	if text.AssembledSize() != len(want)*4 || arm64.VectorSize(ins, abi.StackNoUse, 0) != len(want)*4 {
		t.Fatal("byte/text/vector PC mismatch")
	}
	if arm64.FlagEffect(ins) {
		t.Fatal("SIMD shift changed scalar flags")
	}
	for _, line := range listing {
		if !strings.Contains(text.Text(), line) {
			t.Fatalf("missing %q in %s", line, text.Text())
		}
	}
	if len(want) == 0 && strings.TrimSpace(text.Text()) != "" {
		t.Fatalf("identity alias emitted text: %q", text.Text())
	}
}
