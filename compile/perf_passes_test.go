package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestFloatResidencyInitializesHomeWithoutArithmetic(t *testing.T) {
	p := machineProgram{
		Values:        []machineValue{{Class: backend.General, Words: 2, Bits: 64, Param: 0}},
		Instructions:  []machineInstruction{{Op: machine.SelectedHomeSet, Variable: 0, A: 0, Bits: 64}},
		HomePlan:      &machine.HomePlan{FloatHomes: []bool{true}},
		DiscardResult: true,
	}
	q := assignFloatResidency(p, 32)
	if len(q.Instructions) != 2 || q.Instructions[0].Op != machine.SelectedFloatMove || q.Instructions[1].A != q.Instructions[0].Dst || q.Values[q.Instructions[1].A].Class != backend.Float {
		t.Fatalf("home initialization must pack both GP words into one FP value: %+v", q.Instructions)
	}
	if p.Values[0].Class != backend.General || p.Instructions[0].A != 0 {
		t.Fatal("residency mutated its input")
	}
}

func TestFlagTrialEligibility(t *testing.T) {
	p := machineProgram{Instructions: []machineInstruction{{Op: mBinary, Arithmetic: ir.OpAdd}}}
	for _, arch := range []string{"amd64", "386", "arm64", "ppc64le"} {
		if !mayEstablishBranchFlags(p, arch) {
			t.Fatalf("lost arithmetic trial for %s", arch)
		}
	}
	for _, arch := range []string{"riscv64", "loong64"} {
		if mayEstablishBranchFlags(p, arch) {
			t.Fatalf("flagless %s entered a trial", arch)
		}
	}
	if mayEstablishBranchFlags(machineProgram{Instructions: []machineInstruction{{Op: mLoad64}}}, "arm64") {
		t.Fatal("a load cannot establish branch facts")
	}
}

// PPC reports condition-field effects beyond the S/Z/C facts used by branches.
func TestEmitterRetainsFlagEffectsOutsideArithmeticFacts(t *testing.T) {
	e := &physicalEmitter{
		size:       func(physicalInsn) int { return 4 },
		flagEffect: func(physicalInsn) bool { return true },
		flagFacts:  func(physicalInsn) flagState { return flagState{} },
	}
	e.append(physicalInsn{Op: pJumpIf})
	if !e.flagsClobbered() {
		t.Fatal("lost a condition-field write outside arithmetic flag facts")
	}
}
