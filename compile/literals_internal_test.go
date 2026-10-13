package compile

import (
	"bytes"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/target/arm64"
)

func TestLiteralPoolAlignmentDedupAndImmutableInput(t *testing.T) {
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	p := physicalProgram{codeBytes: 13, code: codeOf(locatedInsn{pc: 0, ins: physicalInsn{Op: machine.OpLiteralLoad, Data: append([]byte(nil), data...)}}, locatedInsn{pc: 8, ins: physicalInsn{Op: machine.OpLiteralLoad, Data: append([]byte(nil), data...)}})}
	finishLiterals(&p)
	insns := p.code.decoded()
	if len(insns) != 3 || insns[0].ins.Disp != 16 || insns[1].ins.Disp != 16 || p.codeBytes != 32 {
		t.Fatalf("bad pool layout: %+v", insns)
	}
	pool := insns[2].ins.Data
	if !bytes.Equal(pool[3:11], data) {
		t.Fatal("literal content changed")
	}
	data[0] = 99
	if pool[3] != 1 {
		t.Fatal("pool aliases input")
	}
}

func TestMixedLiteralPoolNaturalAlignment(t *testing.T) {
	var p physicalProgram
	p.codeBytes = 12
	for n, size := range []int{4, 8, 4, 16, 8} {
		data := bytes.Repeat([]byte{byte(n + 1)}, size)
		if n == 4 {
			data = bytes.Clone(p.code.at(1).Data)
		}
		p.code.add(&physicalInsn{Op: machine.OpLiteralLoad, Bits: uint8(size * 8), Data: data}, 0)
	}
	measured := measureLiteralBytes(p)
	finishLiterals(&p)
	if measured != 64 || p.codeBytes != measured {
		t.Fatalf("measured %d, finished %d; want 64", measured, p.codeBytes)
	}
	insns := p.code.decoded()
	pool := insns[len(insns)-1].ins.Data
	for n, want := range []uint64{16, 24, 32, 48, 24} {
		i := &insns[n].ins
		if i.Disp != want || i.Disp%uint64(literalAlignment(i.Data)) != 0 {
			t.Fatalf("literal %d at %d; want %d", n, i.Disp, want)
		}
		start := int(i.Disp) - 12
		if !bytes.Equal(pool[start:start+len(i.Data)], i.Data) {
			t.Fatalf("literal %d payload changed", n)
		}
	}
}

func TestLiteralExpansionRemapsControlLabels(t *testing.T) {
	e := new(physicalEmitter)
	initARM64MachineEmitter(e, uncheckedTarget(TargetSpec{}).options(Func{}))
	s := machine.NewStream(machine.StreamOptions{Size: e.instructionSize, FlagEffect: func(machine.Instruction) bool { return false }})
	s.Append(machine.Instruction{Op: machine.OpBranchZero, A: 0, Bits: 64, Long: true, Imm: 1})
	s.Append(machine.Instruction{Op: machine.OpLiteralLoad, A: backend.VectorRegister(3), Bits: 64, Data: make([]byte, 8)})
	s.Append(machine.Instruction{Op: machine.OpLiteralData, Data: make([]byte, 1<<20)})
	old := s.PC()
	s.Append(machine.Instruction{Op: machine.OpReturn})
	labels, err := relaxLiteralStream(s, []int{0, old}, []bool{true, true}, "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if labels[1] != old+16 || !s.Instructions()[1].Long {
		t.Fatalf("far literal did not rebase labels: %v", labels)
	}
	policy := arm64.NewPolicy(abi.C, abi.StackNoUse)
	resolved, err := machine.ResolveBranches(s, labels, []bool{true, true}, policy.BranchFits)
	if err != nil || resolved[0].Instruction.Imm != uint64(labels[1]) {
		t.Fatalf("branch missed rebased label: %v", err)
	}
}

func TestFoldFloatLiteralsRemapsScopes(t *testing.T) {
	p := machineProgram{
		Values:       []machineValue{{Constant: true, Imm: 1}, {Constant: true, Imm: 2}},
		Instructions: []machineInstruction{{Op: mConst, Dst: 0}, {Op: mConst, Dst: 1}},
		Result:       1,
		Scopes:       []machine.SelectedScope{{ID: 0, End: 2}, {ID: 1, Parent: 0, Start: 1, End: 2}},
	}
	got := foldFloatLiterals(p, new(allocationTemps))
	if len(got.Instructions) != 1 || got.Scopes[0].End != 1 || got.Scopes[1].Start != 0 || got.Scopes[1].End != 1 {
		t.Fatalf("scope positions did not follow removed constants: %+v", got.Scopes)
	}
	if p.Scopes[0].End != 2 || p.Scopes[1].Start != 1 {
		t.Fatal("literal folding mutated the input scopes")
	}
}
