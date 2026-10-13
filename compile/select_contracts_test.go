package compile

import (
	"errors"
	"reflect"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/ir"
)

type comparisonRecorder struct {
	machine.Assembler

	comparison machine.Instruction
}

func (recorder *comparisonRecorder) Compare(ins machine.Instruction) error {
	recorder.comparison = ins
	return nil
}

func TestVisitPreservesComparisonInterpretation(t *testing.T) {
	for _, condition := range []op.BranchCondition{op.BranchEqual, op.BranchLess, op.BranchBelow} {
		mode := machine.ComparisonFor(condition)
		for _, op := range []machine.Opcode{machine.OpCompareImmediate, machine.OpCompareRegister} {
			ins := machine.Instruction{Op: op, A: 3, B: 4, Bits: 32, Comparison: mode, Imm: 0xffffffff}
			recorder := &comparisonRecorder{}
			if err := machine.Visit(ins, recorder); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(recorder.comparison, ins) {
				t.Fatalf("visitor changed comparison: %+v", recorder.comparison)
			}
		}
	}
	if machine.ComparisonFor(op.BranchLess) != machine.CompareSigned || machine.ComparisonFor(op.BranchBelow) != machine.CompareUnsigned || machine.ComparisonFor(op.BranchEqual) != machine.CompareEquality {
		t.Fatal("comparison domains are not distinct")
	}
}

type memoryRecorder struct {
	machine.Assembler

	loads int
}

func (*memoryRecorder) NativeByteOrder() op.ByteOrder                      { return op.ByteOrderLittleEndian }
func (recorder *memoryRecorder) Load64(backend.Register, backend.Register) { recorder.loads++ }

type orderedMemoryRecorder struct {
	machine.Assembler

	ins machine.Instruction
}

func (recorder *orderedMemoryRecorder) OrderedMemory(ins machine.Instruction) error {
	recorder.ins = ins
	return nil
}

func TestVisitNeverSilentlyDiscardsMemoryOrder(t *testing.T) {
	recorder := &memoryRecorder{}
	for _, order := range []op.ByteOrder{op.ByteOrderNative, op.ByteOrderLittleEndian} {
		if err := machine.Visit(machine.Instruction{Op: machine.OpLoad64, A: 3, B: 4, ByteOrder: order}, recorder); err != nil {
			t.Fatal(err)
		}
	}
	if recorder.loads != 2 {
		t.Fatalf("compatible loads = %d", recorder.loads)
	}
	if err := machine.Visit(machine.Instruction{Op: machine.OpLoad64, ByteOrder: op.ByteOrderBigEndian}, recorder); !errors.Is(err, machine.ErrUnsupportedOperation) {
		t.Fatalf("incompatible order = %v", err)
	}
	if recorder.loads != 2 {
		t.Fatal("unsupported order executed a native load")
	}
	ordered := &orderedMemoryRecorder{}
	ins := machine.Instruction{Op: machine.OpLoadAddress, A: 3, B: 4, C: 5, Imm: 8, Disp: 16, ByteOrder: op.ByteOrderBigEndian}
	if err := machine.Visit(ins, ordered); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ordered.ins, ins) {
		t.Fatal("ordered visitor dropped address or byte order")
	}
}

func TestSelectionVerifierDetectsLostMemoryRepresentation(t *testing.T) {
	target := incSelectionTarget{arch: "ppc64le", bits: 64}
	ops := []ir.Instruction{
		{Op: op.OpConst, Imm: 0x100}, {Op: op.OpConst, Imm: 0x0102030405060708},
		{Op: op.OpStore64, A: 0, B: 1},
		{Op: op.OpLoad64, A: 0, ByteOrder: op.ByteOrderBigEndian},
	}
	selected, err := testSelectProgram(ops, nil, 0, 3, nil, 0, target)
	if err != nil {
		t.Fatal(err)
	}
	for i := range selected.Instructions {
		if selected.Instructions[i].Op == machine.SelectedLoad64 {
			selected.Instructions[i].ByteOrder = op.ByteOrderNative
		}
	}
	if err := verifySelectedProgram(ops, selected, 3, 0, 64, op.ByteOrderLittleEndian); !errors.Is(err, errLoweringMismatch) {
		t.Fatalf("lost big-endian contract = %v", err)
	}
}

// testSelectProgram selects without a workspace.
func testSelectProgram(ops []ir.Instruction, leaves []ir.ScopeExit, arity, result int, exports []int, maxArgs int, target selectionTarget) (machine.SelectedProgram, error) {
	return selectProgramBuffers(ops, leaves, arity, result, exports, maxArgs, target, nil)
}
