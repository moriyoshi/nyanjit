package riscv64

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// The words come from the Go toolchain: GOARCH=riscv64 go tool asm on
// FEQS/FLTS/FLES, FEQD/FLTD/FLED, XOR $1, AND and OR with A=x13, B=f5 and
// C=f9, disassembled with go tool objdump.
var floatCompareVectors = []struct {
	c     op.FloatCondition
	w     uint8
	words []uint32
}{
	{op.FloatEqual, 32, []uint32{0xa092a6d3}},
	{op.FloatNotEqual, 32, []uint32{0xa092a6d3, 0x0016c693}},
	{op.FloatLess, 32, []uint32{0xa09296d3}},
	{op.FloatUnorderedGreaterEqual, 32, []uint32{0xa09296d3, 0x0016c693}},
	{op.FloatLessEqual, 32, []uint32{0xa09286d3}},
	{op.FloatUnorderedGreater, 32, []uint32{0xa09286d3, 0x0016c693}},
	{op.FloatGreater, 32, []uint32{0xa05496d3}},
	{op.FloatUnorderedLessEqual, 32, []uint32{0xa05496d3, 0x0016c693}},
	{op.FloatGreaterEqual, 32, []uint32{0xa05486d3}},
	{op.FloatUnorderedLess, 32, []uint32{0xa05486d3, 0x0016c693}},
	{op.FloatOrdered, 32, []uint32{0xa052a6d3, 0xa094afd3, 0x01f6f6b3}},
	{op.FloatUnordered, 32, []uint32{0xa052a6d3, 0xa094afd3, 0x01f6f6b3, 0x0016c693}},
	{op.FloatOrderedNotEqual, 32, []uint32{0xa09296d3, 0xa0549fd3, 0x01f6e6b3}},
	{op.FloatUnorderedEqual, 32, []uint32{0xa09296d3, 0xa0549fd3, 0x01f6e6b3, 0x0016c693}},
	{op.FloatEqual, 64, []uint32{0xa292a6d3}},
	{op.FloatNotEqual, 64, []uint32{0xa292a6d3, 0x0016c693}},
	{op.FloatLess, 64, []uint32{0xa29296d3}},
	{op.FloatUnorderedGreaterEqual, 64, []uint32{0xa29296d3, 0x0016c693}},
	{op.FloatLessEqual, 64, []uint32{0xa29286d3}},
	{op.FloatUnorderedGreater, 64, []uint32{0xa29286d3, 0x0016c693}},
	{op.FloatGreater, 64, []uint32{0xa25496d3}},
	{op.FloatUnorderedLessEqual, 64, []uint32{0xa25496d3, 0x0016c693}},
	{op.FloatGreaterEqual, 64, []uint32{0xa25486d3}},
	{op.FloatUnorderedLess, 64, []uint32{0xa25486d3, 0x0016c693}},
	{op.FloatOrdered, 64, []uint32{0xa252a6d3, 0xa294afd3, 0x01f6f6b3}},
	{op.FloatUnordered, 64, []uint32{0xa252a6d3, 0xa294afd3, 0x01f6f6b3, 0x0016c693}},
	{op.FloatOrderedNotEqual, 64, []uint32{0xa29296d3, 0xa2549fd3, 0x01f6e6b3}},
	{op.FloatUnorderedEqual, 64, []uint32{0xa29296d3, 0xa2549fd3, 0x01f6e6b3, 0x0016c693}},
}

func floatCompareText(c op.FloatCondition, w uint8) string {
	s := "s"
	if w == 64 {
		s = "d"
	}
	text := map[op.FloatCondition]string{
		op.FloatEqual:           "feq.S x13, f5, f9",
		op.FloatLess:            "flt.S x13, f5, f9",
		op.FloatLessEqual:       "fle.S x13, f5, f9",
		op.FloatGreater:         "flt.S x13, f9, f5",
		op.FloatGreaterEqual:    "fle.S x13, f9, f5",
		op.FloatOrdered:         "feq.S x13, f5, f5|feq.S x31, f9, f9|and x13, x13, x31",
		op.FloatOrderedNotEqual: "flt.S x13, f5, f9|flt.S x31, f9, f5|or x13, x13, x31",
	}[c&^1]
	if c&1 != 0 {
		text += "|xori x13, x13, 1"
	}
	return strings.ReplaceAll(text, ".S", "."+s)
}

func TestFloatCompareEncoding(t *testing.T) {
	a, b := backend.VectorRegister(5), backend.VectorRegister(9)
	for _, test := range floatCompareVectors {
		e := NewByteAssembler(abi.StackPlatform, 0)
		e.FloatCompare(test.c, test.w, 13, a, b)
		if e.Err() != nil || len(e.words) != len(test.words) {
			t.Fatalf("condition %d, %d bits: %v, %d words", test.c, test.w, e.Err(), len(e.words))
		}
		for i, word := range test.words {
			if e.words[i] != word {
				t.Errorf("condition %d, %d bits word %d = %#08x, want %#08x", test.c, test.w, i, e.words[i], word)
			}
		}
		ins := machine.Instruction{Op: machine.OpFloatCompare, Bits: test.w, A: 13, B: a, C: b, Imm: uint64(test.c)}
		if got := PhysicalSize(ins, abi.StackPlatform, 0); got != 4*len(test.words) {
			t.Errorf("condition %d, %d bits: size %d, want %d", test.c, test.w, got, 4*len(test.words))
		}
		scratch := test.c >= op.FloatOrdered
		if got := ImplicitWrites(ins, abi.StackPlatform, 0); scratch != (len(got) == 1 && got[0] == 31) || !scratch && len(got) != 0 {
			t.Errorf("condition %d, %d bits: implicit writes %v", test.c, test.w, got)
		}
		text := NewTextAssembler(abi.StackPlatform, 0)
		if err := machine.Visit(ins, text); err != nil {
			t.Fatal(err)
		}
		var lines []string
		for _, line := range strings.Split(strings.TrimSpace(text.Text()), "\n") {
			lines = append(lines, strings.TrimSpace(line[strings.Index(line, " "):]))
		}
		if got, want := strings.Join(lines, "|"), floatCompareText(test.c, test.w); got != want {
			t.Errorf("condition %d, %d bits text %q, want %q", test.c, test.w, got, want)
		}
	}
	if len(floatCompareVectors) != 28 {
		t.Fatalf("%d vectors", len(floatCompareVectors))
	}
}

func TestFloatCompareRejects(t *testing.T) {
	f1, f2 := backend.VectorRegister(1), backend.VectorRegister(2)
	for _, ins := range []machine.Instruction{
		{Op: machine.OpFloatCompare, Bits: 16, A: 10, B: f1, C: f2},
		{Op: machine.OpFloatCompare, Bits: 64, A: 10, B: f1, C: f2, Imm: 14},
		{Op: machine.OpFloatCompare, Bits: 64, A: 10, B: 11, C: f2},
		{Op: machine.OpFloatCompare, Bits: 64, A: 10, B: f1, C: 12},
		{Op: machine.OpFloatCompare, Bits: 64, A: f1, B: f1, C: f2},
		{Op: machine.OpFloatCompare, Bits: 64, A: 31, B: f1, C: f2, Imm: uint64(op.FloatOrdered)},
		{Op: machine.OpFloatCompare, Bits: 32, A: 31, B: f1, C: f2, Imm: uint64(op.FloatUnorderedEqual)},
	} {
		if got := PhysicalSize(ins, abi.StackPlatform, 0); got != 0 {
			t.Errorf("%+v: size %d", ins, got)
		}
		e := NewByteAssembler(abi.StackPlatform, 0)
		if err := machine.Visit(ins, e); err == nil && e.Err() == nil {
			t.Errorf("%+v accepted", ins)
		}
	}
	// x31 is a fine destination for the single-compare conditions.
	ins := machine.Instruction{Op: machine.OpFloatCompare, Bits: 64, A: 31, B: f1, C: f2, Imm: uint64(op.FloatUnorderedLess)}
	if got := PhysicalSize(ins, abi.StackPlatform, 0); got != 8 {
		t.Errorf("x31 destination: size %d, want 8", got)
	}
}
