package i386_test

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
)

func TestDivideAndConvertEncodings(t *testing.T) {
	xmm := backend.VectorRegister
	for _, tt := range []struct {
		name       string
		ins        machine.Instruction
		code, text string
	}{
		{"div32-unchecked", machine.Instruction{Op: machine.OpDivide, Bits: 32, Arithmetic: op.OpDiv, Imm: uint64(op.DivisionUnchecked), A: 3, B: 6, C: 7},
			"89f031d2f7f789c3", "mov EAX, ESI\nxor EDX, EDX\ndiv EDI\nmov EBX, EAX\n"},
		{"div32-zero", machine.Instruction{Op: machine.OpDivide, Bits: 32, Arithmetic: op.OpDiv, A: 3, B: 6, C: 7},
			"89f085ff740831d2f7f789c3eb0231db", "mov EAX, ESI\ntest EDI, EDI\nje 0xe\nxor EDX, EDX\ndiv EDI\nmov EBX, EAX\njmp 0x10\nxor EBX, EBX\n"},
		{"rem8-signed", machine.Instruction{Op: machine.OpDivide, Bits: 8, Arithmetic: op.OpRemSigned, Imm: uint64(op.DivisionUnchecked), A: 3, B: 6, C: 7},
			"89f0669889faf6fa0fb6dc", "mov EAX, ESI\ncbw\nmov EDX, EDI\nidiv DL\nmovzx EBX, AH\n"},
		{"u32-to-f64", machine.Instruction{Op: machine.OpIntToFloat, Bits: 64, Arithmetic: op.OpFFromUnsigned, Imm: 32, A: xmm(4), B: 3},
			"85db7806f20f2ae3eb19660f6ee3660f73f415660fc5c40381c0d0410000660fc4e003",
			"test EBX, EBX\njs 0xa\ncvtsi2sd xmm4, EBX\njmp 0x23\nmovd xmm4, EBX\npsllq xmm4, 21\npextrw EAX, xmm4, 3\nadd EAX, 0x41d0\npinsrw xmm4, EAX, 3\n"},
		{"f32-to-s32-saturate", machine.Instruction{Op: machine.OpFloatToInt, Bits: 32, Arithmetic: op.OpFToSigned, Disp: 32, A: 3, B: xmm(5)},
			"f30f2cdd81fb0000008075150f2eed7a0e0f50dd83e30181c3ffffff7feb0231db",
			"cvttss2si EBX, xmm5\ncmp EBX, 0x80000000\njne 0x21\nucomiss xmm5, xmm5\njp 0x1f\nmovmskps EBX, xmm5\nand EBX, 1\nadd EBX, 0x7fffffff\njmp 0x21\nxor EBX, EBX\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b, a := i386.NewByteAssembler(abi.StackPlatform), i386.NewTextAssembler(abi.StackPlatform)
			if err := machine.Visit(tt.ins, b); err != nil || b.Err() != nil {
				t.Fatal(err, b.Err())
			}
			if err := machine.Visit(tt.ins, a); err != nil {
				t.Fatal(err)
			}
			want, err := hex.DecodeString(tt.code)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(b.Bytes(), want) || !bytes.Equal(a.Bytes(), want) || a.Text() != tt.text {
				t.Fatalf("bytes=%x listing=%s; want %x %s", b.Bytes(), a.Text(), want, tt.text)
			}
			if !i386.FlagEffect(tt.ins) {
				t.Fatal("the expansion writes flags")
			}
		})
	}
}

// TestDivideAndConvertSizes checks every form: both assemblers agree, the
// size is the encoding's under either stack practice, and the slot operands
// follow Spill's base.
func TestDivideAndConvertSizes(t *testing.T) {
	xmm := backend.VectorRegister
	var forms []machine.Instruction
	for _, w := range []uint8{8, 16, 32, 64} {
		for _, operation := range []op.Opcode{op.OpDiv, op.OpDivSigned, op.OpRem, op.OpRemSigned} {
			for semantics := range uint64(3) {
				ins := machine.Instruction{Op: machine.OpDivide, Bits: w, Arithmetic: operation, Imm: semantics, A: 3, B: 6, C: 7}
				if w == 64 {
					ins = machine.Instruction{Op: machine.OpDivide, Bits: w, Arithmetic: operation, Imm: semantics, A: 0, HighA: 2, Slot: 40}
				}
				forms = append(forms, ins)
			}
		}
	}
	for _, source := range []uint64{32, 64} {
		for _, w := range []uint8{32, 64} {
			for _, operation := range []op.Opcode{op.OpFFromSigned, op.OpFFromUnsigned} {
				forms = append(forms, machine.Instruction{Op: machine.OpIntToFloat, Bits: w, Arithmetic: operation, Imm: source, A: xmm(7), B: 1, HighB: 3, Slot: 40})
			}
			for _, operation := range []op.Opcode{op.OpFToSigned, op.OpFToUnsigned} {
				for semantics := range uint64(2) {
					ins := machine.Instruction{Op: machine.OpFloatToInt, Bits: w, Arithmetic: operation, Imm: semantics, Disp: source, A: 6, B: xmm(7)}
					if w == 64 {
						ins.A, ins.HighA = 0, 2
					}
					forms = append(forms, ins)
				}
			}
		}
	}
	for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
		base := "ESP"
		if stack == abi.StackNoUse {
			base = "EBP"
		}
		for _, ins := range forms {
			b, a := i386.NewByteAssembler(stack), i386.NewTextAssembler(stack)
			if err := machine.Visit(ins, b); err != nil || b.Err() != nil {
				t.Fatalf("%+v: %v %v", ins, err, b.Err())
			}
			if err := machine.Visit(ins, a); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(b.Bytes(), a.Bytes()) || i386.PhysicalSize(ins, stack, 0) != len(b.Bytes()) {
				t.Fatalf("%+v: byte %x, text %x, size %d", ins, b.Bytes(), a.Bytes(), i386.PhysicalSize(ins, stack, 0))
			}
			if strings.Contains(a.Text(), "ptr [") && !strings.Contains(a.Text(), "["+base+" + 160]") {
				t.Fatalf("%+v: slot 40 is not at %s + 160:\n%s", ins, base, a.Text())
			}
		}
	}
}
