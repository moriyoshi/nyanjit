package amd64_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/amd64"
)

func TestSSE2VectorEncodingAndPCs(t *testing.T) {
	v := backend.VectorRegister
	for _, test := range []struct {
		ins  machine.Instruction
		want []byte
	}{
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVLoad, A: v(3), B: 6}, []byte{0xf3, 0x0f, 0x6f, 0x1e}},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVStore, A: 7, B: v(3)}, []byte{0xf3, 0x0f, 0x7f, 0x1f}},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpAdd, Bits: 32, A: v(3), B: v(3), C: v(4)}, []byte{0x66, 0x0f, 0xfe, 0xdc}},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVLoad, A: v(11), B: 13}, []byte{0xf3, 0x45, 0x0f, 0x6f, 0x5d, 0}},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVMaskBits, A: 12, B: v(11)}, []byte{0x66, 0x45, 0x0f, 0xd7, 0xe3}},
	} {
		binary := amd64.NewByteAssembler(abi.StackNoUse, 0)
		text := amd64.NewTextAssembler(abi.StackNoUse, 0)
		if err := binary.Vector(test.ins); err != nil {
			t.Fatal(err)
		}
		if err := text.Vector(test.ins); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(binary.Bytes(), test.want) {
			t.Fatalf("%+v =%x;want%x", test.ins, binary.Bytes(), test.want)
		}
		if len(test.want) != text.AssembledSize() || len(test.want) != amd64.VectorSize(test.ins, abi.StackNoUse, 0) {
			t.Fatal("SIMD emitter/byte/text sizes diverge")
		}
		if amd64.FlagEffect(test.ins) {
			t.Fatal("integer SIMD changes GPR flags")
		}
	}
}

func TestSSE2ImmediateVectorShifts(t *testing.T) {
	// SSE2 encodings: MOVDQA 66 0F 6F /r; PSLLD/PSRLD 66 0F 72 /6,/2 ib;
	// PXOR/POR 66 0F EF,EB /r. These expectations do not use vectorPlan.
	for _, test := range []struct {
		name     string
		op       op.Opcode
		count    uint64
		dst, src uint8
		hex      string
		listing  []string
	}{
		{"shl", op.OpShiftLeft, 7, 3, 4, "660f6fdc660f72f307", []string{"movdqa xmm3, xmm4", "pslld xmm3, 7"}},
		{"shr", op.OpShiftRight, 7, 3, 4, "660f6fdc660f72d307", []string{"movdqa xmm3, xmm4", "psrld xmm3, 7"}},
		{"rol", op.OpRotateLeft, 7, 3, 4, "660f6fc4660f6fdc660f72f307660f72d019660febd8", []string{"movdqa xmm0, xmm4", "pslld xmm3, 7", "psrld xmm0, 25", "por xmm3, xmm0"}},
		{"ror", op.OpRotateRight, 7, 3, 4, "660f6fc4660f6fdc660f72d307660f72f019660febd8", []string{"movdqa xmm0, xmm4", "psrld xmm3, 7", "pslld xmm0, 25", "por xmm3, xmm0"}},
		{"rol-alias", op.OpRotateLeft, 7, 3, 3, "660f6fc3660f72f307660f72d019660febd8", []string{"movdqa xmm0, xmm3", "pslld xmm3, 7", "psrld xmm0, 25", "por xmm3, xmm0"}},
		{"ror-alias", op.OpRotateRight, 7, 3, 3, "660f6fc3660f72d307660f72f019660febd8", []string{"movdqa xmm0, xmm3", "psrld xmm3, 7", "pslld xmm0, 25", "por xmm3, xmm0"}},
		{"shl-alias", op.OpShiftLeft, 7, 3, 3, "660f72f307", []string{"pslld xmm3, 7"}},
		{"shr-alias", op.OpShiftRight, 7, 3, 3, "660f72d307", []string{"psrld xmm3, 7"}},
		{"high-register", op.OpShiftLeft, 31, 11, 12, "66450f6fdc66410f72f31f", []string{"movdqa xmm11, xmm12", "pslld xmm11, 31"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ins := machine.Instruction{Op: machine.OpVector, Arithmetic: test.op, Bits: 32, A: backend.VectorRegister(test.dst), B: backend.VectorRegister(test.src), Imm: test.count}
			checkSSE2VectorEncoding(t, ins, test.hex, test.listing)
		})
	}
	for _, operation := range []op.Opcode{op.OpShiftLeft, op.OpShiftRight, op.OpRotateLeft, op.OpRotateRight} {
		for _, alias := range []bool{false, true} {
			src := uint8(4)
			want := "660f6fdc"
			listing := []string{"movdqa xmm3, xmm4"}
			if alias {
				src = 3
				want = ""
				listing = nil
			}
			ins := machine.Instruction{Op: machine.OpVector, Arithmetic: operation, Bits: 32, A: backend.VectorRegister(3), B: backend.VectorRegister(src)}
			checkSSE2VectorEncoding(t, ins, want, listing)
			for _, count := range []uint64{32, 33, 64, 255} {
				ins.Imm = count
				if operation == op.OpShiftLeft || operation == op.OpShiftRight {
					checkSSE2VectorEncoding(t, ins, "660fefdb", []string{"pxor xmm3, xmm3"})
				} else {
					reduced := ins
					reduced.Imm = count % 32
					b := amd64.NewByteAssembler(abi.StackNoUse, 0)
					if err := b.Vector(reduced); err != nil {
						t.Fatal(err)
					}
					checkSSE2VectorEncoding(t, ins, hex.EncodeToString(b.Bytes()), nil)
				}
			}
		}
		for _, bits := range []uint8{8, 16, 64} {
			ins := machine.Instruction{Op: machine.OpVector, Arithmetic: operation, Bits: bits, A: backend.VectorRegister(3), B: backend.VectorRegister(4), Imm: 7}
			b, text := amd64.NewByteAssembler(abi.StackNoUse, 0), amd64.NewTextAssembler(abi.StackNoUse, 0)
			if !errors.Is(b.Vector(ins), machine.ErrUnsupportedOperation) || !errors.Is(text.Vector(ins), machine.ErrUnsupportedOperation) {
				t.Fatalf("op=%v bits=%d should be unsupported", operation, bits)
			}
			if len(b.Bytes()) != 0 || text.AssembledSize() != 0 {
				t.Fatal("unsupported operation emitted partial output")
			}
		}
	}
}

func checkSSE2VectorEncoding(t *testing.T, ins machine.Instruction, wantHex string, listing []string) {
	t.Helper()
	b, text := amd64.NewByteAssembler(abi.StackNoUse, 0), amd64.NewTextAssembler(abi.StackNoUse, 0)
	if err := b.Vector(ins); err != nil {
		t.Fatal(err)
	}
	if err := text.Vector(ins); err != nil {
		t.Fatal(err)
	}
	want, err := hex.DecodeString(wantHex)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b.Bytes(), want) {
		t.Fatalf("%+v: got %x want %x", ins, b.Bytes(), want)
	}
	if text.AssembledSize() != len(want) || amd64.VectorSize(ins, abi.StackNoUse, 0) != len(want) {
		t.Fatal("byte/text/vector PC mismatch")
	}
	if amd64.FlagEffect(ins) {
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

func TestPCLMULQDQEncodingAndListing(t *testing.T) {
	for _, test := range []struct {
		name                string
		dst, first, second  uint8
		copyHex, productHex string
	}{
		{"low-alias", 3, 3, 4, "", "660f3a44dc"},
		{"rex-B", 3, 3, 12, "", "66410f3a44dc"},
		{"rex-R", 12, 12, 3, "", "66440f3a44e3"},
		{"rex-RB", 9, 9, 14, "", "66450f3a44ce"},
		{"copy-high-source", 3, 12, 4, "66410f6fdc", "660f3a44dc"},
		{"copy-high-destination", 12, 3, 4, "66440f6fe3", "66440f3a44e4"},
		{"copy-both-high", 9, 14, 12, "66450f6fce", "66450f3a44cc"},
	} {
		for selector, immediate := range []byte{0x00, 0x01, 0x10, 0x11} {
			t.Run(fmt.Sprintf("%s/selector=%d", test.name, selector), func(t *testing.T) {
				ins := machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVCarrylessMul, Bits: 64, A: backend.VectorRegister(test.dst), B: backend.VectorRegister(test.first), C: backend.VectorRegister(test.second), Imm: uint64(selector)}
				listing := []string{fmt.Sprintf("pclmulqdq xmm%d, xmm%d, %#x", test.dst, test.second, immediate)}
				if test.copyHex != "" {
					listing = append(listing, fmt.Sprintf("movdqa xmm%d, xmm%d", test.dst, test.first))
				}
				checkSSE2VectorEncoding(t, ins, test.copyHex+test.productHex+fmt.Sprintf("%02x", immediate), listing)
				text := amd64.NewTextAssembler(abi.StackNoUse, 0)
				if err := text.Vector(ins); err != nil {
					t.Fatal(err)
				}
				if test.dst == test.first && strings.Contains(text.Text(), "movdqa") {
					t.Fatal("aliased first input emitted a copy")
				}
			})
		}
	}
	for _, bits := range []uint8{0, 8, 16, 32, 64, 128} {
		for _, selector := range []uint64{0, 4, ^uint64(0)} {
			if bits == 64 && selector == 0 {
				continue
			}
			ins := machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVCarrylessMul, Bits: bits, A: backend.VectorRegister(3), B: backend.VectorRegister(4), C: backend.VectorRegister(5), Imm: selector}
			b, text := amd64.NewByteAssembler(abi.StackNoUse, 0), amd64.NewTextAssembler(abi.StackNoUse, 0)
			if !errors.Is(b.Vector(ins), machine.ErrUnsupportedOperation) || !errors.Is(text.Vector(ins), machine.ErrUnsupportedOperation) {
				t.Fatalf("bits=%d selector=%d should be rejected", bits, selector)
			}
			if len(b.Bytes()) != 0 || text.AssembledSize() != 0 {
				t.Fatal("invalid PCLMULQDQ emitted partial output")
			}
		}
	}
}
