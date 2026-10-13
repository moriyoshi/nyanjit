package machine

import (
	"errors"
	"reflect"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

type comparisonRecorder struct {
	Assembler
	comparison Instruction
}

func (recorder *comparisonRecorder) Compare(ins Instruction) error {
	recorder.comparison = ins
	return nil
}

func TestVisitPreservesComparisonInterpretation(t *testing.T) {
	for _, condition := range []ir.BranchCondition{ir.BranchEqual, ir.BranchLess, ir.BranchBelow} {
		mode := ComparisonFor(condition)
		for _, op := range []Opcode{OpCompareImmediate, OpCompareRegister} {
			ins := Instruction{Op: op, A: 3, B: 4, Bits: 32, Comparison: mode, Imm: 0xffffffff}
			recorder := &comparisonRecorder{}
			if err := Visit(ins, recorder); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(recorder.comparison, ins) {
				t.Fatalf("visitor changed comparison: %+v", recorder.comparison)
			}
		}
	}
	if ComparisonFor(ir.BranchLess) != CompareSigned || ComparisonFor(ir.BranchBelow) != CompareUnsigned || ComparisonFor(ir.BranchEqual) != CompareEquality {
		t.Fatal("comparison domains are not distinct")
	}
}

type memoryRecorder struct {
	Assembler
	loads int
}

func (*memoryRecorder) NativeByteOrder() ir.ByteOrder      { return ir.ByteOrderLittleEndian }
func (recorder *memoryRecorder) Load64(Register, Register) { recorder.loads++ }

type orderedMemoryRecorder struct {
	Assembler
	ins Instruction
}

func (recorder *orderedMemoryRecorder) OrderedMemory(ins Instruction) error {
	recorder.ins = ins
	return nil
}

func TestVisitNeverSilentlyDiscardsMemoryOrder(t *testing.T) {
	recorder := &memoryRecorder{}
	for _, order := range []ir.ByteOrder{ir.ByteOrderNative, ir.ByteOrderLittleEndian} {
		if err := Visit(Instruction{Op: OpLoad64, A: 3, B: 4, ByteOrder: order}, recorder); err != nil {
			t.Fatal(err)
		}
	}
	if recorder.loads != 2 {
		t.Fatalf("compatible loads = %d", recorder.loads)
	}
	if err := Visit(Instruction{Op: OpLoad64, ByteOrder: ir.ByteOrderBigEndian}, recorder); !errors.Is(err, ErrUnsupportedOperation) {
		t.Fatalf("incompatible order = %v", err)
	}
	if recorder.loads != 2 {
		t.Fatal("unsupported order executed a native load")
	}
	ordered := &orderedMemoryRecorder{}
	ins := Instruction{Op: OpLoadAddress, A: 3, B: 4, C: 5, Imm: 8, Disp: 16, ByteOrder: ir.ByteOrderBigEndian}
	if err := Visit(ins, ordered); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ordered.ins, ins) {
		t.Fatal("ordered visitor dropped address or byte order")
	}
}

type bigEndianSelectionTarget struct{ incSelectionTarget }

func (bigEndianSelectionTarget) NativeByteOrder() ir.ByteOrder { return ir.ByteOrderBigEndian }

func TestSelectionVerifierDetectsLostMemoryRepresentation(t *testing.T) {
	target := bigEndianSelectionTarget{incSelectionTarget{arch: "ppc64", bits: 64}}
	ops := []ir.Instruction{
		{Op: ir.OpConst, Imm: 0x100}, {Op: ir.OpConst, Imm: 0x0102030405060708},
		{Op: ir.OpStore64, A: 0, B: 1},
		{Op: ir.OpLoad64, A: 0, ByteOrder: ir.ByteOrderLittleEndian},
	}
	selected, err := SelectProgram(ops, nil, 0, 3, nil, 0, target)
	if err != nil {
		t.Fatal(err)
	}
	for i := range selected.Instructions {
		if selected.Instructions[i].Op == SelectedLoad64 {
			selected.Instructions[i].ByteOrder = ir.ByteOrderNative
		}
	}
	if err := verifySelectedProgram(ops, selected, 3, 0, 64, ir.ByteOrderBigEndian); !errors.Is(err, ErrLoweringMismatch) {
		t.Fatalf("lost little-endian contract = %v", err)
	}
}
