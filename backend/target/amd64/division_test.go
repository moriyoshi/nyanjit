package amd64

import (
	"encoding/hex"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// checkExpansion assembles ins with both assemblers and compares the bytes,
// the listing's size and the layout size.
func checkExpansion(t *testing.T, ins machine.Instruction) []byte {
	t.Helper()
	b := NewByteAssembler(abi.StackPlatform, 0)
	if err := machine.Visit(ins, b); err != nil || b.Err() != nil {
		t.Fatalf("%+v: %v %v", ins, err, b.Err())
	}
	x := NewTextAssembler(abi.StackPlatform, 0)
	if err := machine.Visit(ins, x); err != nil || x.Err() != nil {
		t.Fatalf("%+v: text %v %v", ins, err, x.Err())
	}
	size := PhysicalSize(ins, abi.StackPlatform, 0)
	if size != len(b.Bytes()) || x.AssembledSize() != size {
		t.Fatalf("%+v: size %d, bytes %d, listing %d", ins, size, len(b.Bytes()), x.AssembledSize())
	}
	return b.Bytes()
}

func TestDivideForms(t *testing.T) {
	all := machine.FlagSign | machine.FlagZero | machine.FlagCarry
	// Aliasings, RAX/RDX destinations and dividends, R11 as B and A, and
	// SPL..DIL and extended byte registers.
	registers := [][3]backend.Register{{1, 6, 7}, {6, 6, 7}, {7, 6, 7}, {8, 9, 9}, {9, 9, 9}, {0, 2, 8}, {2, 11, 10}, {11, 4, 5}, {15, 14, 13}}
	for _, w := range []uint8{8, 16, 32, 64} {
		for _, arithmetic := range []op.Opcode{op.OpDiv, op.OpDivSigned, op.OpRem, op.OpRemSigned} {
			for semantics := op.DivisionZero; semantics <= op.DivisionUnchecked; semantics++ {
				for _, r := range registers {
					ins := machine.Instruction{Op: machine.OpDivide, Arithmetic: arithmetic, Bits: w, A: r[0], B: r[1], C: r[2], Imm: uint64(semantics)}
					checkExpansion(t, ins)
					if f := FlagFacts(ins); f.Modified != all || f.Zero.Valid || f.Carry.Valid {
						t.Fatalf("%+v: flags %+v", ins, f)
					}
				}
			}
		}
	}
	for _, ins := range []machine.Instruction{
		{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 64, A: 1, B: 6, C: 0},
		{Op: machine.OpDivide, Arithmetic: op.OpDivSigned, Bits: 8, A: 1, B: 6, C: 2},
		{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 0, A: 1, B: 6, C: 7},
		{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 64, A: 1, B: 6, C: 7, Imm: 3},
		{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 64, A: 1, B: 6, C: 7, Imm: 0x100},
		{Op: machine.OpDivide, Arithmetic: op.OpAdd, Bits: 64, A: 1, B: 6, C: 7},
		{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 64, A: 16, B: 6, C: 7},
	} {
		b := NewByteAssembler(abi.StackPlatform, 0)
		if err := machine.Visit(ins, b); err == nil && b.Err() == nil || PhysicalSize(ins, abi.StackPlatform, 0) != 0 {
			t.Errorf("%+v accepted", ins)
		}
	}
	for _, c := range []struct {
		ins  machine.Instruction
		code string
	}{
		// MOV EAX, ESI; XOR EDX, EDX; DIV EDI; MOV R9D, EDX
		{machine.Instruction{Op: machine.OpDivide, Arithmetic: op.OpRem, Bits: 16, A: 9, B: 6, C: 7, Imm: uint64(op.DivisionUnchecked)}, "89f031d2f7f74189d1"},
		// TEST EDI, EDI; JZ +18; MOVSX EAX, SIL; MOVSX R11D, DIL; CDQ;
		// IDIV R11D; MOVZX R9D, AL; JMP +6; MOV R9D, 0xFF
		{machine.Instruction{Op: machine.OpDivide, Arithmetic: op.OpDivSigned, Bits: 8, A: 9, B: 6, C: 7, Imm: uint64(op.DivisionAllOnes)}, "85ff7412400fbec6440fbedf9941f7fb440fb6c8eb0641b9ff000000"},
		// TEST RDI, RDI; JZ +27; CMP RDI, -1; JE +13; MOV RAX, RSI; CQO;
		// IDIV RDI; MOV RCX, RAX; JMP +15; MOV RCX, RSI; NEG RCX; JMP +7;
		// MOV RCX, -1
		{machine.Instruction{Op: machine.OpDivide, Arithmetic: op.OpDivSigned, Bits: 64, A: 1, B: 6, C: 7, Imm: uint64(op.DivisionAllOnes)}, "4885ff741b4883ffff740d4889f0489948f7ff4889c1eb0f4889f148f7d9eb0748c7c1ffffffff"},
	} {
		if got := hex.EncodeToString(checkExpansion(t, c.ins)); got != c.code {
			t.Errorf("%+v: %s, want %s", c.ins, got, c.code)
		}
	}
}

func TestFloatConvertForms(t *testing.T) {
	v := backend.VectorRegister
	all := machine.FlagSign | machine.FlagZero | machine.FlagCarry
	for _, out := range []uint8{32, 64} {
		for _, in := range []uint64{32, 64} {
			for _, arithmetic := range []op.Opcode{op.OpFFromSigned, op.OpFFromUnsigned} {
				for _, r := range [][2]backend.Register{{v(3), 0}, {v(8), 12}, {v(15), 7}} {
					ins := machine.Instruction{Op: machine.OpIntToFloat, Arithmetic: arithmetic, Bits: out, A: r[0], B: r[1], Imm: in}
					checkExpansion(t, ins)
					want := machine.FlagMask(0)
					if arithmetic == op.OpFFromUnsigned && in == 64 {
						want = all
					}
					if f := FlagFacts(ins); f.Modified != want || FlagEffect(ins) != (want != 0) {
						t.Fatalf("%+v: flags %+v", ins, f)
					}
				}
			}
			for _, arithmetic := range []op.Opcode{op.OpFToSigned, op.OpFToUnsigned} {
				for semantics := op.ConversionSaturate; semantics <= op.ConversionNative; semantics++ {
					for _, r := range [][2]backend.Register{{0, v(3)}, {12, v(8)}, {7, v(15)}} {
						ins := machine.Instruction{Op: machine.OpFloatToInt, Arithmetic: arithmetic, Bits: out, A: r[0], B: r[1], Disp: in, Imm: uint64(semantics)}
						checkExpansion(t, ins)
						want := all
						if arithmetic == op.OpFToSigned && semantics == op.ConversionNative {
							want = 0
						}
						if f := FlagFacts(ins); f.Modified != want || FlagEffect(ins) != (want != 0) {
							t.Fatalf("%+v: flags %+v", ins, f)
						}
						if writes := machine.FloatWrites(ins, "amd64"); len(writes) != 0 {
							t.Fatalf("%+v writes %v", ins, writes)
						}
					}
				}
			}
		}
	}
	for _, ins := range []machine.Instruction{
		{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromSigned, Bits: 64, A: 1, B: 6, Imm: 64},
		{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromSigned, Bits: 64, A: v(1), B: v(6), Imm: 64},
		{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromSigned, Bits: 64, A: v(1), B: 6, Imm: 16},
		{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromUnsigned, Bits: 64, A: v(1), B: 11, Imm: 64},
		{Op: machine.OpIntToFloat, Arithmetic: op.OpFToSigned, Bits: 64, A: v(1), B: 6, Imm: 64},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 64, A: v(1), B: v(3), Disp: 64},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 16, A: 1, B: v(3), Disp: 64},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 64, A: 1, B: v(3), Disp: 16},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 64, A: 1, B: v(3), Disp: 64, Imm: 2},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFFromSigned, Bits: 64, A: 1, B: v(3), Disp: 64},
	} {
		b := NewByteAssembler(abi.StackPlatform, 0)
		if err := machine.Visit(ins, b); err == nil && b.Err() == nil || PhysicalSize(ins, abi.StackPlatform, 0) != 0 {
			t.Errorf("%+v accepted", ins)
		}
	}
	for _, c := range []struct {
		ins  machine.Instruction
		code string
	}{
		// CVTTSS2SI R9, XMM10
		{machine.Instruction{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 64, A: 9, B: v(10), Disp: 32, Imm: uint64(op.ConversionNative)}, "f34d0f2cca"},
		// XORPS XMM9, XMM9; CVTSI2SS XMM9, R12D
		{machine.Instruction{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromSigned, Bits: 32, A: v(9), B: 12, Imm: 32}, "450f57c9f3450f2acc"},
		// XORPS XMM3, XMM3; TEST RSI, RSI; JS +7; CVTSI2SD XMM3, RSI;
		// JMP +25; MOV R11D, ESI; AND R11D, 1; ADD R11D, R11D; OR R11, RSI;
		// SHR R11, 1; CVTSI2SD XMM3, R11; ADDSD XMM3, XMM3
		{machine.Instruction{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromUnsigned, Bits: 64, A: v(3), B: 6, Imm: 64}, "0f57db4885f67807f2480f2adeeb194189f34183e3014501db4909f349d1ebf2490f2adbf20f58db"},
		// CVTTSS2SI R9, XMM10; TEST R9, R9; JNS +14; MOVD R9D, XMM10;
		// SHL R9, 40; BTS R9, 63
		{machine.Instruction{Op: machine.OpFloatToInt, Arithmetic: op.OpFToUnsigned, Bits: 64, A: 9, B: v(10), Disp: 32, Imm: uint64(op.ConversionNative)}, "f34d0f2cca4d85c9790e66450f7ed149c1e128490fbae93f"},
	} {
		if got := hex.EncodeToString(checkExpansion(t, c.ins)); got != c.code {
			t.Errorf("%+v: %s, want %s", c.ins, got, c.code)
		}
	}
}
