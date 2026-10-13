package ppc64

import (
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

func TestRecordEncodingGoldenWords(t *testing.T) {
	// Independently checked with Debian GNU powerpc64le-linux-gnu-as -mpower9.
	for _, test := range []struct {
		ins  Instruction
		word uint32
	}{
		{Instruction{Op: Addc, A: 3, B: 4, C: 5, Record: true}, 0x7c642815},
		{Instruction{Op: Subfc, A: 3, B: 4, C: 5, Record: true}, 0x7c652011},
		{Instruction{Op: Addic, A: 3, B: 4, Imm: -1, Record: true}, 0x3464ffff},
		{Instruction{Op: Mfxer, A: 12}, 0x7d8102a6},
		{Instruction{Op: Mcrxrx, Imm: 7}, 0x7f800480},
		{Instruction{Op: Extsw, A: 4, B: 3, Record: true}, 0x7c6407b5},
		{Instruction{Op: Rldicl, A: 4, B: 3, Mask: 32, Record: true}, 0x78640021},
	} {
		word, err := Encode(test.ins)
		if err != nil || word != test.word {
			t.Fatalf("%+v=%#x,%v; want %#x", test.ins, word, err, test.word)
		}
	}
	for _, ins := range []Instruction{{Op: Addi, Record: true}, {Op: B, Record: true}, {Op: Mcrxrx, Imm: 8}, {Op: Mcrxrx, Imm: -1}} {
		if _, err := Encode(ins); err == nil {
			t.Fatalf("invalid record operands accepted: %+v", ins)
		}
	}
}

func testFlagEmitter(t *testing.T, level abi.PPC64CPU) *Emitter {
	t.Helper()
	profile, err := abi.NewPPC64Profile("ppc64", abi.C, abi.PPC64ELFv2)
	if err != nil {
		t.Fatal(err)
	}
	emitter, err := NewEmitterForCPU(profile, abi.StackNoUse, false, level)
	if err != nil {
		t.Fatal(err)
	}
	return emitter
}

func TestPPCFlagFactsAreWidthAndResourceSpecific(t *testing.T) {
	for _, cpu := range []abi.PPC64CPU{abi.PPC64Power8, abi.PPC64Power9, abi.PPC64Power10} {
		emitter := testFlagEmitter(t, cpu)
		for _, width := range []uint8{8, 16, 32, 64} {
			for _, op := range []ir.Opcode{ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpMul} {
				ins := machine.Instruction{Op: machine.OpBinary, Arithmetic: op, Bits: width, A: 3, B: 4, C: 5}
				facts := emitter.FlagFacts(ins)
				if !facts.Zero.Valid || facts.Sign.Valid != (width == 64) {
					t.Fatalf("CPU %s width %d opcode %d: %+v", cpu, width, op, facts)
				}
				wantCarry := op == ir.OpSub || op == ir.OpAdd && (width == 64 || width == 32 && cpu >= abi.PPC64Power9)
				if facts.Carry.Valid != wantCarry || wantCarry && facts.Carry.NoBorrow != (op == ir.OpSub) {
					t.Fatalf("CPU %s width %d opcode %d carry=%+v", cpu, width, op, facts.Carry)
				}
				if op == ir.OpAnd && facts.Modified&machine.FlagCarry != 0 {
					t.Fatal("AND must preserve earlier XER carry")
				}
			}
		}
		for _, imm := range []uint64{0, 1, 7, ^uint64(0)} {
			facts := emitter.FlagFacts(machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: ir.OpSub, Bits: 64, A: 3, B: 4, Imm: imm})
			if facts.Carry.Valid != (imm != 0) {
				t.Fatalf("sub immediate %#x: %+v", imm, facts)
			}
		}
		narrow := machine.Instruction{Op: machine.OpNarrow, A: 3, B: 3, Imm: 32}
		if emitter.FlagEffect(narrow) || emitter.FlagFacts(narrow).Modified != 0 {
			t.Fatal("unrequested parameter/FP normalization modifies flags")
		}
		narrow.FlagRequest = machine.FlagZero
		if facts := emitter.FlagFacts(narrow); !facts.Zero.Valid || facts.Sign.Valid || facts.Zero.Width != 32 {
			t.Fatalf("requested narrow facts=%+v", facts)
		}
		shift := machine.Instruction{Op: machine.OpShift, Arithmetic: ir.OpShiftRightArithmetic, Bits: 64, A: 3, B: 3, Imm: 1}
		if facts := emitter.FlagFacts(shift); facts.Modified != machine.FlagCarry || facts.Carry.Valid {
			t.Fatalf("SRADI must only invalidate XER carry: %+v", facts)
		}
	}
}

func TestCarryBranchPreservesArithmeticCR0AndXER(t *testing.T) {
	for _, cpu := range []abi.PPC64CPU{abi.PPC64Power8, abi.PPC64Power9, abi.PPC64Power10} {
		emitter := testFlagEmitter(t, cpu)
		profile, _ := abi.NewPPC64Profile("ppc64", abi.C, abi.PPC64ELFv2)
		policy, _ := NewPolicyForCPU(profile, abi.StackNoUse, cpu)
		for _, condition := range []ir.BranchCondition{machine.BranchCarry, machine.BranchNotCarry, machine.BranchCarry32, machine.BranchNotCarry32} {
			for _, long := range []bool{false, true} {
				ins := machine.Instruction{Op: machine.OpJumpIf, Condition: condition, Long: long, Imm: 200}
				sequence, err := emitter.Expand(ins, 100)
				if cpu == abi.PPC64Power8 && condition >= machine.BranchCarry32 {
					if err == nil {
						t.Fatal("POWER8 cannot use CA32")
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				facts := emitter.FlagFacts(ins)
				if facts.Modified != 0 {
					t.Fatalf("carry branch overwrites sign/zero/carry: %+v", facts)
				}
				writes := emitter.SpecialWrites(ins)
				if !slices.Equal(writes, []machine.Resource{{Class: machine.ResourceConditionField, Number: 7}}) {
					t.Fatalf("carry branch writes=%+v", writes)
				}
				size := emitter.Size(ins)
				if size != len(sequence)*4 || !policy.BranchFits(ins, 100, 200, size) {
					t.Fatal("carry branch sizing disagrees with native expansion")
				}
				last := sequence[len(sequence)-1]
				if last.PC+int(last.Imm) != 200 {
					t.Fatalf("branch displacement ignores XER prefix: %+v", sequence)
				}
				if cpu == abi.PPC64Power8 && len(emitter.ImplicitWrites(ins)) != 1 {
					t.Fatal("POWER8 carry branch must report its R12 temporary")
				}
				if cpu >= abi.PPC64Power9 && len(emitter.ImplicitWrites(ins)) != 0 {
					t.Fatal("MCRXRX requires no GPR temporary")
				}
			}
		}
	}
}

func TestCTRLoopExpansionAndEffects(t *testing.T) {
	emitter := testFlagEmitter(t, abi.PPC64Power8)
	for _, long := range []bool{false, true} {
		ins := machine.Instruction{Op: machine.OpLoop, Bits: 64, Imm: 0, Long: long}
		sequence, err := emitter.Expand(ins, 4)
		if err != nil {
			t.Fatal(err)
		}
		if sequence[0].Op != Bc || sequence[0].BO != 16 && !long || sequence[0].BO != 18 && long {
			t.Fatalf("CTR loop expansion=%+v", sequence)
		}
		if !long {
			word, err := Encode(sequence[0])
			if err != nil || word != 0x4200fffc {
				t.Fatalf("BDNZ encoding=%#x,%v", word, err)
			}
		}
		if emitter.FlagEffect(ins) || emitter.FlagFacts(ins).Modified != 0 || len(emitter.ImplicitWrites(ins)) != 0 {
			t.Fatal("CTR loop must preserve S/Z/C and all GPRs")
		}
		if writes := emitter.SpecialWrites(ins); !slices.Equal(writes, []machine.Resource{{Class: machine.ResourceCountRegister}}) {
			t.Fatalf("CTR clobber=%+v", writes)
		}
	}
	set := machine.Instruction{Op: machine.OpSetCount, A: 3}
	if emitter.Size(set) != 4 || emitter.FlagEffect(set) || len(emitter.ImplicitWrites(set)) != 0 {
		t.Fatal("CTR initialization changed another resource")
	}
}

func TestCTRBranchRangeAccountsForLongBranchPC(t *testing.T) {
	profile, _ := abi.NewPPC64Profile("ppc64", abi.C, abi.PPC64ELFv2)
	policy, _ := NewPolicyForCPU(profile, abi.StackNoUse, abi.PPC64Power8)
	for _, test := range []struct {
		long bool
		dest int
		want bool
	}{{false, -32768, true}, {false, 32764, true}, {false, 32768, false}, {true, (1 << 25), true}, {true, (1 << 25) + 4, false}} {
		if got := policy.BranchFits(machine.Instruction{Op: machine.OpLoop, Long: test.long}, 0, test.dest, 0); got != test.want {
			t.Fatalf("long=%t destination=%d: %t; want %t", test.long, test.dest, got, test.want)
		}
	}
}

func TestPlainArithmeticDoesNotWriteCR0OrCarry(t *testing.T) {
	emitter := testFlagEmitter(t, abi.PPC64Power8)
	for _, ins := range []machine.Instruction{
		{Op: machine.OpBinary, Arithmetic: ir.OpAdd, Bits: 64, A: 3, B: 4, C: 5, PreserveFlags: true},
		{Op: machine.OpBinary, Arithmetic: ir.OpSub, Bits: 64, A: 3, B: 4, C: 5, PreserveFlags: true},
		{Op: machine.OpBinaryImmediate, Arithmetic: ir.OpAdd, Bits: 64, A: 3, B: 4, Imm: 8, PreserveFlags: true},
		{Op: machine.OpDec, Bits: 64, A: 3, B: 4, PreserveFlags: true},
	} {
		sequence, err := emitter.Expand(ins, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(sequence) != 1 || sequence[0].Record {
			t.Fatalf("unexpected plain expansion:%+v", sequence)
		}
		if facts := emitter.FlagFacts(ins); facts.Modified != 0 {
			t.Fatalf("plain arithmetic writes flags:%+v", facts)
		}
		if _, err := Encode(sequence[0]); err != nil {
			t.Fatal(err)
		}
	}
}
