package compile

import (
	"bytes"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/target/arm64"
	"testing"
)

func TestLiteralPoolAlignmentDedupAndImmutableInput(t *testing.T) {
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	p := physicalProgram{codeBytes: 13, insns: []locatedInsn{{pc: 0, ins: physicalInsn{Op: machine.OpLiteralLoad, Data: append([]byte(nil), data...)}}, {pc: 8, ins: physicalInsn{Op: machine.OpLiteralLoad, Data: append([]byte(nil), data...)}}}}
	finishLiterals(&p)
	if len(p.insns) != 3 || p.insns[0].ins.Disp != 16 || p.insns[1].ins.Disp != 16 || p.codeBytes != 32 {
		t.Fatalf("bad pool layout: %+v", p)
	}
	pool := p.insns[2].ins.Data
	if !bytes.Equal(pool[3:11], data) {
		t.Fatal("literal content changed")
	}
	data[0] = 99
	if pool[3] != 1 {
		t.Fatal("pool aliases input")
	}
}

func TestLiteralExpansionRemapsControlLabels(t *testing.T) {
	e := newARM64MachineEmitter(CompileOptions{})
	s := machine.NewStream(machine.StreamOptions{Size: e.instructionSize, FlagEffect: func(machine.Instruction) bool { return false }})
	s.Append(machine.Instruction{Op: machine.OpBranchZero, A: 0, Bits: 64, Long: true, Imm: 1})
	s.Append(machine.Instruction{Op: machine.OpLiteralLoad, A: machine.VectorRegister(3), Bits: 64, Data: make([]byte, 8)})
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
	got := foldFloatLiterals(p)
	if len(got.Instructions) != 1 || got.Scopes[0].End != 1 || got.Scopes[1].Start != 0 || got.Scopes[1].End != 1 {
		t.Fatalf("scope positions did not follow removed constants: %+v", got.Scopes)
	}
	if p.Scopes[0].End != 2 || p.Scopes[1].Start != 1 {
		t.Fatal("literal folding mutated the input scopes")
	}
}
