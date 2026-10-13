package compile

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

func TestARM64LiteralBranchJointFixedPoint(t *testing.T) {
	for _, scenario := range []string{"forward-after-load", "backward-after-load", "before-load", "literal-growth-crosses-branch-limit"} {
		t.Run(scenario, func(t *testing.T) {
			options := mustTarget(t, TargetSpec{Architecture: "arm64"}).options(Func{})
			target, emitter := newMachineTarget(options), newMachineEmitter(options)
			s := machine.NewStream(machine.StreamOptions{Size: emitter.instructionSize, FlagEffect: func(machine.Instruction) bool { return false }})
			load := machine.Instruction{Op: machine.OpLiteralLoad, A: backend.VectorRegister(3), Bits: 64, Data: make([]byte, 8)}
			branch := machine.Instruction{Op: machine.OpBranchBit, A: 0, Bits: 64, Imm: 1}
			// Initial pool base is 16 bytes below the literal's 1 MB boundary.
			// A four-byte branch promotion advances the aligned pool by 16.
			filler := machine.Instruction{Op: machine.OpLiteralData, Data: make([]byte, (1<<20)-28)}
			switch scenario {
			case "literal-growth-crosses-branch-limit":
				// Initially the long branch can shrink to a TBZ at its exact
				// positive limit. Growing the intervening literal invalidates it.
				branch.Long = true
				s.Append(branch)
				s.Append(load)
				s.Append(machine.Instruction{Op: machine.OpLiteralData, Data: make([]byte, 32756)})
			case "before-load":
				s.Append(branch)
				s.Append(load)
				s.Append(filler)
			case "backward-after-load":
				s.Append(load)
				s.Append(filler)
				branch.Imm = 0
				s.Append(branch)
			default:
				s.Append(load)
				s.Append(branch)
				s.Append(filler)
			}
			end := s.PC()
			s.Append(machine.Instruction{Op: machine.OpReturn})
			if scenario == "literal-growth-crosses-branch-limit" {
				s.Append(machine.Instruction{Op: machine.OpLiteralData, Data: make([]byte, 1<<20)})
			}
			labels, bound := []int{0, end}, []bool{true, true}
			labels, err := relaxControlLayout(s, labels, bound, target)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := machine.ResolveBranches(s, labels, bound, target.BranchFits)
			if err != nil {
				t.Fatalf("joint layout rejected valid branch: %v", err)
			}
			// Keep the resolved layout across the next fixed-point layout pass.
			resolved = append([]machine.LocatedInstruction(nil), resolved...)
			var literalLong, branchLong bool
			for _, i := range s.Instructions() {
				if i.Op == machine.OpLiteralLoad {
					literalLong = i.Long
				}
				if i.Op == machine.OpBranchBit {
					branchLong = i.Long
				}
			}
			if !branchLong || literalLong != (scenario != "before-load") {
				t.Fatalf("wrong forms: literal long=%t branch long=%t", literalLong, branchLong)
			}
			pc := s.PC()
			previousLabels := append([]int(nil), labels...)
			labels, err = relaxControlLayout(s, labels, bound, target)
			if err != nil || s.PC() != pc {
				t.Fatalf("layout did not reach fixed point: %v", err)
			}
			for n, pc := range previousLabels {
				if labels[n] != pc {
					t.Fatal("fixed point changed label positions")
				}
			}
			physical := physicalProgram{codeBytes: s.PC()}
			for _, i := range resolved {
				physical.insns = append(physical.insns, locatedInsn{pc: i.PC, ins: i.Instruction})
			}
			finishLiterals(&physical)
			bytes, err := assembleProgram(physical, newMachineAssembler(options, emitNative))
			if err != nil {
				t.Fatal(err)
			}
			text, err := assembleProgram(physical, newMachineAssembler(options, emitText))
			if err != nil {
				t.Fatal(err)
			}
			if len(bytes.code) != physical.codeBytes || strings.Contains(text.assembly, "adr X16, .") != literalLong {
				t.Fatal("byte/text layout disagreed")
			}
		})
	}
}

func TestARM64StraightLineLiteralLayoutIsFinal(t *testing.T) {
	options := mustTarget(t, TargetSpec{Architecture: "arm64"}).options(Func{})
	e := newMachineEmitter(options)
	s := machine.NewStream(machine.StreamOptions{Size: e.instructionSize, FlagEffect: func(machine.Instruction) bool { return false }})
	s.Append(machine.Instruction{Op: machine.OpLiteralLoad, A: backend.VectorRegister(3), Bits: 64, Data: make([]byte, 8)})
	s.Append(machine.Instruction{Op: machine.OpLiteralData, Data: make([]byte, (1<<20)-12)})
	s.Append(machine.Instruction{Op: machine.OpReturn})
	p := physicalProgram{codeBytes: s.PC()}
	pc := 0
	for _, i := range s.Instructions() {
		p.insns = append(p.insns, locatedInsn{pc: pc, ins: i})
		pc += s.Size(i)
	}
	if err := relaxFunctionLiterals(&p, e, "arm64"); err != nil {
		t.Fatal(err)
	}
	if !p.insns[0].ins.Long {
		t.Fatal("pool alignment did not promote the borderline load")
	}
	before := append([]locatedInsn(nil), p.insns...)
	finishLiterals(&p)
	for n, i := range before {
		if p.insns[n].pc != i.pc || e.instructionSize(p.insns[n].ins) != e.instructionSize(i.ins) {
			t.Fatal("pool finalization changed an existing instruction's size or PC")
		}
	}
	for _, kind := range []emissionKind{emitNative, emitText} {
		if _, err := assembleProgram(p, newMachineAssembler(options, kind)); err != nil {
			t.Fatal(err)
		}
	}
}
