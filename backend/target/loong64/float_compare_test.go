package loong64

import (
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// The fcmp words compare B=f9 with C=f10 into fcc0. fcmp.ceq.{s,d} and
// movcf2gr r4, fcc0 match the Go assembler's CMPEQF/CMPEQD F10, F9, FCC0 and
// MOVV FCC0, R4. Go has no other quiet condition (CMPGT/CMPGE are the
// signaling slt/sle), so the remaining words follow the LA64 manual's cond
// table (clt 0x2, ceq 0x4, cle 0x6, cun 0x8, cult 0xa, cueq 0xc, cule 0xe,
// cne 0x10, cor 0x14, cune 0x18); go tool objdump (x/arch loong64asm) decodes
// each as the listed condition and operand order.
func TestFloatCompareEncoding(t *testing.T) {
	for _, test := range []struct {
		c    op.FloatCondition
		w    uint8
		word uint32
		text string
	}{
		{op.FloatEqual, 32, 0x0c122920, "fcmp.ceq.s fcc0, f9, f10"},
		{op.FloatNotEqual, 32, 0x0c1c2920, "fcmp.cune.s fcc0, f9, f10"},
		{op.FloatLess, 32, 0x0c112920, "fcmp.clt.s fcc0, f9, f10"},
		{op.FloatUnorderedGreaterEqual, 32, 0x0c172540, "fcmp.cule.s fcc0, f10, f9"},
		{op.FloatLessEqual, 32, 0x0c132920, "fcmp.cle.s fcc0, f9, f10"},
		{op.FloatUnorderedGreater, 32, 0x0c152540, "fcmp.cult.s fcc0, f10, f9"},
		{op.FloatGreater, 32, 0x0c112540, "fcmp.clt.s fcc0, f10, f9"},
		{op.FloatUnorderedLessEqual, 32, 0x0c172920, "fcmp.cule.s fcc0, f9, f10"},
		{op.FloatGreaterEqual, 32, 0x0c132540, "fcmp.cle.s fcc0, f10, f9"},
		{op.FloatUnorderedLess, 32, 0x0c152920, "fcmp.cult.s fcc0, f9, f10"},
		{op.FloatOrdered, 32, 0x0c1a2920, "fcmp.cor.s fcc0, f9, f10"},
		{op.FloatUnordered, 32, 0x0c142920, "fcmp.cun.s fcc0, f9, f10"},
		{op.FloatOrderedNotEqual, 32, 0x0c182920, "fcmp.cne.s fcc0, f9, f10"},
		{op.FloatUnorderedEqual, 32, 0x0c162920, "fcmp.cueq.s fcc0, f9, f10"},
		{op.FloatEqual, 64, 0x0c222920, "fcmp.ceq.d fcc0, f9, f10"},
		{op.FloatNotEqual, 64, 0x0c2c2920, "fcmp.cune.d fcc0, f9, f10"},
		{op.FloatLess, 64, 0x0c212920, "fcmp.clt.d fcc0, f9, f10"},
		{op.FloatUnorderedGreaterEqual, 64, 0x0c272540, "fcmp.cule.d fcc0, f10, f9"},
		{op.FloatLessEqual, 64, 0x0c232920, "fcmp.cle.d fcc0, f9, f10"},
		{op.FloatUnorderedGreater, 64, 0x0c252540, "fcmp.cult.d fcc0, f10, f9"},
		{op.FloatGreater, 64, 0x0c212540, "fcmp.clt.d fcc0, f10, f9"},
		{op.FloatUnorderedLessEqual, 64, 0x0c272920, "fcmp.cule.d fcc0, f9, f10"},
		{op.FloatGreaterEqual, 64, 0x0c232540, "fcmp.cle.d fcc0, f10, f9"},
		{op.FloatUnorderedLess, 64, 0x0c252920, "fcmp.cult.d fcc0, f9, f10"},
		{op.FloatOrdered, 64, 0x0c2a2920, "fcmp.cor.d fcc0, f9, f10"},
		{op.FloatUnordered, 64, 0x0c242920, "fcmp.cun.d fcc0, f9, f10"},
		{op.FloatOrderedNotEqual, 64, 0x0c282920, "fcmp.cne.d fcc0, f9, f10"},
		{op.FloatUnorderedEqual, 64, 0x0c262920, "fcmp.cueq.d fcc0, f9, f10"},
	} {
		i := machine.Instruction{Op: machine.OpFloatCompare, Bits: test.w, A: 4, B: backend.VectorRegister(9), C: backend.VectorRegister(10), Imm: uint64(test.c)}
		e := NewTextAssembler(abi.StackPlatform, 0)
		if err := machine.Visit(i, e); err != nil || e.Err() != nil {
			t.Fatalf("condition %d, %d bits: %v %v", test.c, test.w, err, e.Err())
		}
		code := e.Bytes()
		want := []uint32{test.word, 0x0114dc04}
		if len(code) != 8 || PhysicalSize(i, abi.StackPlatform, 0) != 8 {
			t.Fatalf("condition %d, %d bits: %d bytes, size %d", test.c, test.w, len(code), PhysicalSize(i, abi.StackPlatform, 0))
		}
		for n, word := range want {
			if got := binary.LittleEndian.Uint32(code[4*n:]); got != word {
				t.Errorf("condition %d, %d bits word %d = %#08x, want %#08x", test.c, test.w, n, got, word)
			}
		}
		if got, want := e.Text(), "0000  "+test.text+"\n0004  movcf2gr r4, fcc0\n"; got != want {
			t.Errorf("condition %d, %d bits text %q, want %q", test.c, test.w, got, want)
		}
		if w := machine.FloatWrites(i, "loong64"); len(w) != 0 {
			t.Errorf("condition %d, %d bits writes FP %v", test.c, test.w, w)
		}
		if w := ImplicitWrites(i, abi.StackPlatform, 0); len(w) != 0 {
			t.Errorf("condition %d, %d bits implicit writes %v", test.c, test.w, w)
		}
	}
	// A reserved destination is reported through movcf2gr.
	i := machine.Instruction{Op: machine.OpFloatCompare, Bits: 64, A: 19, B: backend.VectorRegister(9), C: backend.VectorRegister(10)}
	if w := ImplicitWrites(i, abi.StackPlatform, 0); !reflect.DeepEqual(w, []backend.Register{19}) {
		t.Errorf("A=r19 implicit writes %v", w)
	}
	if w := (Policy{}).SpecialWrites(i); len(w) != 1 || w[0].Class != machine.ResourceFloatingStatus {
		t.Errorf("special writes %v", w)
	}
}

func TestFloatCompareRejected(t *testing.T) {
	f := backend.VectorRegister
	for _, i := range []machine.Instruction{
		{Op: machine.OpFloatCompare, Bits: 16, A: 4, B: f(9), C: f(10)},
		{Op: machine.OpFloatCompare, Bits: 64, A: 4, B: f(9), C: f(10), Imm: 14},
		{Op: machine.OpFloatCompare, Bits: 64, A: 4, B: 5, C: f(10)},
		{Op: machine.OpFloatCompare, Bits: 64, A: 4, B: f(9), C: 6},
		{Op: machine.OpFloatCompare, Bits: 64, A: f(8), B: f(9), C: f(10)},
	} {
		if a := NewByteAssembler(abi.StackNoUse, 0); machine.Visit(i, a) == nil && a.Err() == nil {
			t.Errorf("accepted %+v", i)
		}
		if PhysicalSize(i, abi.StackNoUse, 0) != 0 {
			t.Errorf("sized %+v", i)
		}
	}
	// machine.Visit narrows Imm to an op.FloatCondition, so only sizing sees
	// an Imm beyond eight bits.
	if i := (machine.Instruction{Op: machine.OpFloatCompare, Bits: 64, A: 4, B: f(9), C: f(10), Imm: 1 << 8}); PhysicalSize(i, abi.StackNoUse, 0) != 0 {
		t.Errorf("sized %+v", i)
	}
}
