package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/regalloc"
)

func TestFPBankCopyUsesConsumerWidth(t *testing.T) {
	for _, metadata := range []uint8{0, 64} {
		p := machineProgram{Result: 2, Values: []machineValue{{Bits: metadata}, {Bits: metadata}, {Bits: metadata}},
			Instructions: []machineInstruction{{Op: mFloatBinary, Arithmetic: ir.OpFAdd, Bits: 32, Dst: 1, A: 0, B: 0},
				{Op: mFloatUnary, Arithmetic: ir.OpFConvert, Bits: 64, Dst: 2, A: 1}}}
		q := assignFloatResidency(p)
		first := q.Instructions[0]
		if first.Op != machine.SelectedFloatMove || first.Bits != 32 || q.Values[first.Dst].Bits != 32 {
			t.Fatalf("metadata%d produced wrong binary32 bank copy: %+v", metadata, first)
		}
		if q.Values[1].Bits != 32 || q.Values[1].Class != regalloc.Float {
			t.Fatal("producer width was not inferred from FP operation")
		}
		last := q.Instructions[len(q.Instructions)-1]
		if last.Op != machine.SelectedFloatMove || last.Bits != 64 {
			t.Fatal("return copy lost binary64 producer width")
		}
	}
}

func TestFConvertCopyUsesSourceWidth(t *testing.T) {
	for _, dstWidth := range []uint8{32, 64} {
		p := machineProgram{Result: 1, Values: []machineValue{{Bits: 0}, {Bits: 0}}, Instructions: []machineInstruction{{Op: mFloatUnary, Arithmetic: ir.OpFConvert, Bits: dstWidth, Dst: 1, A: 0}}}
		q := assignFloatResidency(p)
		if q.Instructions[0].Bits != 96-dstWidth {
			t.Fatal("FConvert bank copy used destination width")
		}
	}
}

func TestFPBankCopiesCacheByWidth(t *testing.T) {
	p := machineProgram{Result: 2, Exports: []int{1}, Values: []machineValue{{Bits: 0}, {Bits: 0}, {Bits: 0}}, Instructions: []machineInstruction{
		{Op: mFloatBinary, Arithmetic: ir.OpFAdd, Bits: 32, Dst: 1, A: 0, B: 0},
		{Op: mFloatBinary, Arithmetic: ir.OpFAdd, Bits: 64, Dst: 2, A: 0, B: 0}}}
	q := assignFloatResidency(p)
	counts := map[uint8]int{}
	for _, i := range q.Instructions {
		if i.Op == machine.SelectedFloatMove && i.A == 0 {
			counts[i.Bits]++
		}
	}
	if counts[32] != 1 || counts[64] != 1 {
		t.Fatalf("bank copies aliased widths or missed reuse: %v", counts)
	}
}
