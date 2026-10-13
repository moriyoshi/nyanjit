package riscv64

import (
	"slices"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// conversionForms lists every OpIntToFloat and OpFloatToInt combination.
func conversionForms() []machine.Instruction {
	var forms []machine.Instruction
	for _, bits := range []uint8{32, 64} {
		for _, other := range []uint64{32, 64} {
			for _, operation := range []op.Opcode{op.OpFFromSigned, op.OpFFromUnsigned} {
				forms = append(forms, machine.Instruction{Op: machine.OpIntToFloat, Arithmetic: operation, Bits: bits, A: backend.VectorRegister(5), B: 10, Imm: other})
			}
			for _, operation := range []op.Opcode{op.OpFToSigned, op.OpFToUnsigned} {
				for _, semantics := range []op.ConversionSemantics{op.ConversionSaturate, op.ConversionNative} {
					forms = append(forms, machine.Instruction{Op: machine.OpFloatToInt, Arithmetic: operation, Bits: bits, A: 10, B: backend.VectorRegister(5), Imm: uint64(semantics), Disp: other})
				}
			}
		}
	}
	return forms
}

// divisionForms lists every OpDivide combination with each register alias.
func divisionForms() []machine.Instruction {
	var forms []machine.Instruction
	for _, bits := range []uint8{8, 16, 32, 64} {
		for _, operation := range []op.Opcode{op.OpDiv, op.OpDivSigned, op.OpRem, op.OpRemSigned} {
			for _, semantics := range []op.DivisionSemantics{op.DivisionZero, op.DivisionAllOnes, op.DivisionUnchecked} {
				for _, regs := range [][3]backend.Register{{10, 11, 12}, {10, 10, 11}, {10, 11, 10}, {10, 11, 11}, {10, 10, 10}} {
					forms = append(forms, machine.Instruction{Op: machine.OpDivide, Arithmetic: operation, Bits: bits, A: regs[0], B: regs[1], C: regs[2], Imm: uint64(semantics)})
				}
			}
		}
	}
	return forms
}

func TestDivisionAndConversionPlans(t *testing.T) {
	for _, ins := range append(divisionForms(), conversionForms()...) {
		b := NewByteAssembler(abi.StackNoUse, 0)
		if err := machine.Visit(ins, b); err != nil || b.Err() != nil {
			t.Fatalf("%+v: %v %v", ins, err, b.Err())
		}
		text := NewTextAssembler(abi.StackNoUse, 0)
		if err := machine.Visit(ins, text); err != nil {
			t.Fatal(err)
		}
		listing := strings.TrimSpace(text.Text())
		if len(b.Bytes()) != PhysicalSize(ins, abi.StackNoUse, 0) || len(strings.Split(listing, "\n"))*4 != len(b.Bytes()) {
			t.Fatalf("size mismatch %+v", ins)
		}
		if strings.Contains(listing, ".word") {
			t.Fatalf("undecoded instruction: %s", listing)
		}
		var written [2]bool
		for _, word := range b.words {
			rd := word >> 7 & 31
			// Integer destinations: OP-IMM, OP, OP-32 and the GP-writing fcvt/feq.
			gp := word&0x7f == 0x13 || word&0x7f == 0x33 || word&0x7f == 0x3b ||
				word&0x7f == 0x53 && (word>>25 == 0x60 || word>>25 == 0x61 || word>>25 == 0x50 || word>>25 == 0x51)
			if word&0x7f == 0x53 && !gp && rd != uint32(backend.VectorNumber(ins.A)) {
				t.Fatalf("%+v writes f%d besides its destination", ins, rd)
			}
			if gp && rd >= 30 {
				written[rd-30] = true
			}
		}
		var want []backend.Register
		for i, yes := range written {
			if yes {
				want = append(want, backend.Register(30+i))
			}
		}
		if got := ImplicitWrites(ins, abi.StackNoUse, 0); !slices.Equal(got, want) {
			t.Fatalf("%+v: reported scratch writes %v, encoded %v\n%s", ins, got, want, listing)
		}
	}
}

func TestDivisionAndConversionEncodings(t *testing.T) {
	for _, tc := range []struct {
		ins  machine.Instruction
		want []string
	}{
		{machine.Instruction{Op: machine.OpDivide, Arithmetic: op.OpDivSigned, Bits: 64, A: 10, B: 11, C: 12, Imm: uint64(op.DivisionAllOnes)},
			[]string{"div x10, x11, x12"}},
		{machine.Instruction{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 64, A: 12, B: 11, C: 12, Imm: uint64(op.DivisionZero)},
			[]string{"sltiu x31, x12, 1", "divu x12, x11, x12", "add x12, x12, x31"}},
		{machine.Instruction{Op: machine.OpDivide, Arithmetic: op.OpRemSigned, Bits: 32, A: 10, B: 11, C: 12, Imm: uint64(op.DivisionZero)},
			[]string{"remw x10, x11, x12", "slli x10, x10, 32", "srli x10, x10, 32"}},
		{machine.Instruction{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 8, A: 10, B: 11, C: 12, Imm: uint64(op.DivisionAllOnes)},
			[]string{"divu x10, x11, x12", "andi x10, x10, 255"}},
		{machine.Instruction{Op: machine.OpDivide, Arithmetic: op.OpDivSigned, Bits: 16, A: 10, B: 11, C: 12, Imm: uint64(op.DivisionZero)},
			[]string{"slli x30, x11, 48", "slli x31, x12, 48", "div x30, x30, x31", "sltiu x31, x31, 1", "add x30, x30, x31", "slli x10, x30, 48", "srli x10, x10, 48"}},
		{machine.Instruction{Op: machine.OpDivide, Arithmetic: op.OpRemSigned, Bits: 8, A: 10, B: 11, C: 12, Imm: uint64(op.DivisionUnchecked)},
			[]string{"slli x30, x11, 56", "slli x31, x12, 56", "rem x30, x30, x31", "srli x10, x30, 56"}},
		{machine.Instruction{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromUnsigned, Bits: 32, A: backend.VectorRegister(5), B: 10, Imm: 64},
			[]string{"fcvt.s.lu f5, x10"}},
		{machine.Instruction{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromSigned, Bits: 64, A: backend.VectorRegister(5), B: 10, Imm: 32},
			[]string{"fcvt.d.w f5, x10"}},
		{machine.Instruction{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 64, A: 10, B: backend.VectorRegister(1), Imm: uint64(op.ConversionNative), Disp: 64},
			[]string{"fcvt.l.d x10, f1, rtz"}},
		{machine.Instruction{Op: machine.OpFloatToInt, Arithmetic: op.OpFToUnsigned, Bits: 32, A: 10, B: backend.VectorRegister(1), Imm: uint64(op.ConversionSaturate), Disp: 32},
			[]string{"fcvt.wu.s x10, f1, rtz", "feq.s x31, f1, f1", "sub x31, x0, x31", "srli x31, x31, 32", "and x10, x10, x31"}},
	} {
		b := NewByteAssembler(abi.StackNoUse, 0)
		if err := machine.Visit(tc.ins, b); err != nil || b.Err() != nil {
			t.Fatalf("%+v: %v %v", tc.ins, err, b.Err())
		}
		var got []string
		for _, word := range b.words {
			got = append(got, decodeWord(word, 0))
		}
		if !slices.Equal(got, tc.want) {
			t.Fatalf("%+v:\ngot  %q\nwant %q", tc.ins, got, tc.want)
		}
	}
	// Spot-check the listing against independently assembled words.
	for word, want := range map[uint32]string{0x02c5c533: "div x10, x11, x12", 0xc2209553: "fcvt.l.d x10, f1, rtz", 0xa2b52fd3: "feq.d x31, f10, f11"} {
		if got := decodeWord(word, 0); got != want {
			t.Fatalf("%#x decoded %q, want %q", word, got, want)
		}
	}
}

func TestDivisionAndConversionRejectInvalidForms(t *testing.T) {
	for _, ins := range []machine.Instruction{
		{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 12, A: 10, B: 11, C: 12},
		{Op: machine.OpDivide, Arithmetic: op.OpAdd, Bits: 64, A: 10, B: 11, C: 12},
		{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 64, A: 10, B: 11, C: 12, Imm: 3},
		{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 64, A: 10, B: 11, C: 12, Imm: 1 << 8},
		{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 64, A: backend.VectorRegister(1), B: 11, C: 12},
		{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromSigned, Bits: 64, A: 10, B: 11, Imm: 64},
		{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromSigned, Bits: 64, A: backend.VectorRegister(1), B: backend.VectorRegister(2), Imm: 64},
		{Op: machine.OpIntToFloat, Arithmetic: op.OpFToSigned, Bits: 64, A: backend.VectorRegister(1), B: 11, Imm: 64},
		{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromSigned, Bits: 16, A: backend.VectorRegister(1), B: 11, Imm: 64},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 64, A: 10, B: 11, Disp: 64},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 64, A: 10, B: backend.VectorRegister(1), Disp: 16},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 64, A: 10, B: backend.VectorRegister(1), Disp: 64, Imm: 2},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFFromSigned, Bits: 64, A: 10, B: backend.VectorRegister(1), Disp: 64},
	} {
		b := NewByteAssembler(abi.StackNoUse, 0)
		if err := machine.Visit(ins, b); err == nil && b.Err() == nil {
			t.Fatalf("%+v assembled", ins)
		}
		if PhysicalSize(ins, abi.StackNoUse, 0) != 0 || ImplicitWrites(ins, abi.StackNoUse, 0) != nil {
			t.Fatalf("%+v has a size or scratch writes", ins)
		}
	}
}
