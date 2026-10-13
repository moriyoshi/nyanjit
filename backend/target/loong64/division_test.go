package loong64_test

import (
	"encoding/binary"
	"reflect"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
)

// Opcode fields independently match Go's cmd/internal/obj/loong64 table
// (DIVV/DIVVU/REMV/REMVU, FFINT*, MASKEQZ/MASKNEZ).
func TestDivideConvertEncodings(t *testing.T) {
	f := backend.VectorRegister
	for _, tc := range []struct {
		i     machine.Instruction
		words []uint32
		text  string
	}{
		{machine.Instruction{Op: machine.OpDivide, Arithmetic: op.OpDivSigned, Bits: 64, A: 4, B: 5, C: 6, Imm: uint64(op.DivisionUnchecked)}, []uint32{0x002218a4}, "div.d r4, r5, r6"},
		{machine.Instruction{Op: machine.OpDivide, Arithmetic: op.OpRem, Bits: 64, A: 4, B: 5, C: 6, Imm: uint64(op.DivisionUnchecked)}, []uint32{0x002398a4}, "mod.du r4, r5, r6"},
		{machine.Instruction{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 32, A: 4, B: 5, C: 6, Imm: uint64(op.DivisionZero)},
			[]uint32{0x002318b3, 0x00131a64}, "maskeqz r4, r19, r6"},
		{machine.Instruction{Op: machine.OpDivide, Arithmetic: op.OpRemSigned, Bits: 64, A: 4, B: 5, C: 6, Imm: uint64(op.DivisionZero)},
			[]uint32{0x002298b3, 0x02c004d4, 0x00135273, 0x00131a73, 0x001398b4, 0x00155264}, "or r4, r19, r20"},
		{machine.Instruction{Op: machine.OpDivide, Arithmetic: op.OpDivSigned, Bits: 8, A: 4, B: 5, C: 6, Imm: uint64(op.DivisionAllOnes)},
			[]uint32{0x00005cb3, 0x00005cd4, 0x00225264, 0x02400694, 0x0011d014, 0x00155084, 0x00c70084}, "bstrpick.d r4, r4, 7, 0"},
		{machine.Instruction{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromSigned, Bits: 64, A: f(8), B: 5, Imm: 32}, []uint32{0x0114a4a8, 0x011d2108}, "ffint.d.w f8, f8"},
		{machine.Instruction{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromUnsigned, Bits: 32, A: f(8), B: 5, Imm: 32}, []uint32{0x0114a8a8, 0x011d1908}, "ffint.s.l f8, f8"},
		{machine.Instruction{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromUnsigned, Bits: 64, A: f(8), B: 5, Imm: 64},
			[]uint32{0x0114a8a8, 0x640014a0, 0x004504b3, 0x034004b4, 0x00155273, 0x0114aa68, 0x011d2908, 0x640008a0, 0x01012108}, "fadd.d f8, f8, f8"},
	} {
		b := loong64.NewByteAssembler(abi.StackNoUse, 0)
		txt := loong64.NewTextAssembler(abi.StackNoUse, 0)
		for _, a := range []machine.Assembler{b, txt} {
			if err := machine.Visit(tc.i, a); err != nil {
				t.Fatal(err)
			}
		}
		var words []uint32
		for n := 0; n < len(b.Bytes()); n += 4 {
			words = append(words, binary.LittleEndian.Uint32(b.Bytes()[n:]))
		}
		if !reflect.DeepEqual(words, tc.words) || !strings.Contains(txt.Text(), tc.text) || len(b.Bytes()) != loong64.PhysicalSize(tc.i, abi.StackNoUse, 0) || len(b.Bytes()) != txt.AssembledSize() {
			t.Errorf("%s got %08x want %08x", txt.Text(), words, tc.words)
		}
	}
}

// divideConvertForms enumerates every valid OpDivide and conversion form.
func divideConvertForms() []machine.Instruction {
	f := backend.VectorRegister
	var forms []machine.Instruction
	for _, arithmetic := range []op.Opcode{op.OpDiv, op.OpDivSigned, op.OpRem, op.OpRemSigned} {
		for _, w := range []uint8{8, 16, 32, 64} {
			for s := op.DivisionZero; s.Valid(); s++ {
				forms = append(forms, machine.Instruction{Op: machine.OpDivide, Arithmetic: arithmetic, Bits: w, A: 4, B: 5, C: 6, Imm: uint64(s)})
			}
		}
	}
	for _, w := range []uint8{32, 64} {
		for _, in := range []uint64{32, 64} {
			for _, arithmetic := range []op.Opcode{op.OpFFromSigned, op.OpFFromUnsigned} {
				forms = append(forms, machine.Instruction{Op: machine.OpIntToFloat, Arithmetic: arithmetic, Bits: w, A: f(8), B: 5, Imm: in})
			}
			for _, arithmetic := range []op.Opcode{op.OpFToSigned, op.OpFToUnsigned} {
				for s := op.ConversionSaturate; s.Valid(); s++ {
					forms = append(forms, machine.Instruction{Op: machine.OpFloatToInt, Arithmetic: arithmetic, Bits: w, A: 4, B: f(9), Disp: in, Imm: uint64(s)})
				}
			}
		}
	}
	return forms
}

// Internal branches must stay inside their expansion, sizes must agree, and
// the conversions must write no FP register but the one machine.FloatWrites
// reports: A for OpIntToFloat, f0 for OpFloatToInt.
func TestDivideConvertPlans(t *testing.T) {
	for _, i := range divideConvertForms() {
		b := loong64.NewByteAssembler(abi.StackNoUse, 0)
		txt := loong64.NewTextAssembler(abi.StackNoUse, 0)
		for _, a := range []machine.Assembler{b, txt} {
			if err := machine.Visit(i, a); err != nil {
				t.Fatal(err)
			}
		}
		code := b.Bytes()
		if len(code) == 0 || len(code) != loong64.PhysicalSize(i, abi.StackNoUse, 0) || len(code) != txt.AssembledSize() || strings.Contains(txt.Text(), "%!") {
			t.Fatalf("%+v size%d:\n%s", i, len(code), txt.Text())
		}
		for n := 0; n < len(code); n += 4 {
			w := binary.LittleEndian.Uint32(code[n:])
			switch w >> 26 {
			case 0x14: // b
				if to := n + 4*int(w>>10&0xffff|(w&0x3ff)<<16); to <= n || to > len(code) {
					t.Fatalf("%+v branch at %d leaves the expansion:\n%s", i, n, txt.Text())
				}
			case 0x18, 0x19: // blt, bge
				if to := n + 4*int(w>>10&0xffff); to <= n || to > len(code) {
					t.Fatalf("%+v branch at %d leaves the expansion:\n%s", i, n, txt.Text())
				}
			}
			// Every 0x01xxxxxx word but movfr2gr writes an FP register.
			if w>>24 == 0x01 && w&0xfffffc00 != 0x452d<<10 && w&0xfffffc00 != 0x452e<<10 {
				if f := backend.VectorRegister(uint8(w & 31)); !reflect.DeepEqual(machine.FloatWrites(i, "loong64"), []backend.Register{f}) {
					t.Fatalf("%+v writes f%d:\n%s", i, w&31, txt.Text())
				}
			}
		}
		writes := loong64.ImplicitWrites(i, abi.StackNoUse, 0)
		for _, r := range writes {
			if r != 12 && r != 19 && r != 20 {
				t.Fatalf("%+v writes r%d", i, r)
			}
		}
	}
}

func TestDivideConvertRejected(t *testing.T) {
	f := backend.VectorRegister
	for _, i := range []machine.Instruction{
		{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 12, A: 4, B: 5, C: 6},
		{Op: machine.OpDivide, Arithmetic: op.OpAdd, Bits: 64, A: 4, B: 5, C: 6},
		{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 64, A: 4, B: 5, C: 6, Imm: 3},
		{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 64, A: f(8), B: 5, C: 6},
		{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromSigned, Bits: 64, A: 4, B: 5, Imm: 64},
		{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromSigned, Bits: 64, A: f(8), B: 5, Imm: 16},
		{Op: machine.OpIntToFloat, Arithmetic: op.OpFToSigned, Bits: 64, A: f(8), B: 5, Imm: 64},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 64, A: 4, B: 5, Disp: 64},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 16, A: 4, B: f(9), Disp: 64},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 64, A: 4, B: f(9), Disp: 64, Imm: 2},
	} {
		if a := loong64.NewByteAssembler(abi.StackNoUse, 0); machine.Visit(i, a) == nil && a.Err() == nil {
			t.Fatalf("accepted %+v", i)
		}
		if loong64.PhysicalSize(i, abi.StackNoUse, 0) != 0 {
			t.Fatalf("sized %+v", i)
		}
	}
}

func TestDivideConvertEffects(t *testing.T) {
	p := loong64.NewPolicy(abi.C, abi.StackNoUse)
	for _, o := range []machine.Opcode{machine.OpIntToFloat, machine.OpFloatToInt} {
		if writes := p.SpecialWrites(machine.Instruction{Op: o}); len(writes) != 1 || p.ResourceName(writes[0]) != "FCSR0" {
			t.Fatalf("op%d must change FCSR0: %v", o, writes)
		}
	}
	if writes := p.SpecialWrites(machine.Instruction{Op: machine.OpDivide}); len(writes) != 0 {
		t.Fatalf("division spuriously changes FP status: %v", writes)
	}
	divide := machine.SelectedInstruction{Op: machine.SelectedDivide}
	if !p.CanReuse(divide, 0) || !p.CanReuse(divide, 1) {
		t.Fatal("division expansions read both inputs before writing A")
	}
}
