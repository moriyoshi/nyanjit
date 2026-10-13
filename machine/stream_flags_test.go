package machine_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/target/ppc64"
)

func flagStream(t *testing.T) *machine.Stream {
	t.Helper()
	profile, _ := abi.NewPPC64Profile("ppc64", abi.C, abi.PPC64ELFv2)
	emitter, err := ppc64.NewEmitterForCPU(profile, abi.StackNoUse, false, abi.PPC64Power9)
	if err != nil {
		t.Fatal(err)
	}
	return machine.NewStream(machine.StreamOptions{Size: emitter.Size, FlagEffect: emitter.FlagEffect, FlagFacts: emitter.FlagFacts})
}

func TestStreamMergesPartialPPCFlagWrites(t *testing.T) {
	stream := flagStream(t)
	stream.Append(machine.Instruction{Op: machine.OpBinary, Arithmetic: ir.OpAdd, Bits: 64, A: 3, B: 4, C: 5})
	stream.BindFlagValue(2, 0, 1, ir.OpAdd, false)
	stream.Append(machine.Instruction{Op: machine.OpBinary, Arithmetic: ir.OpAnd, Bits: 64, A: 6, B: 7, C: 8})
	stream.BindFlagValue(5, 3, 4, ir.OpAnd, false)
	if facts := stream.Flags(); !facts.Carry.MatchesValue(2) || !facts.Zero.MatchesValue(5) || !facts.Sign.MatchesValue(5) {
		t.Fatalf("logical op lost independent carry: %+v", facts)
	}
	stream.Append(machine.Instruction{Op: machine.OpShift, Arithmetic: ir.OpShiftRightArithmetic, Bits: 64, A: 9, B: 10, Imm: 1})
	if facts := stream.Flags(); facts.Carry.Valid || !facts.Zero.MatchesValue(5) || !facts.Sign.MatchesValue(5) {
		t.Fatalf("SRADI lost surviving CR0 facts: %+v", facts)
	}
	stream.Append(machine.Instruction{Op: machine.OpCallC})
	if facts := stream.Flags(); facts.Sign.Valid || facts.Zero.Valid || facts.Carry.Valid {
		t.Fatalf("call left stale flags: %+v", facts)
	}
}

func TestStreamNarrowCarryAndZeroAliases(t *testing.T) {
	stream := flagStream(t)
	stream.Append(machine.Instruction{Op: machine.OpBinary, Arithmetic: ir.OpAdd, Bits: 32, A: 3, B: 4, C: 5})
	stream.BindFlagValue(10, 0, 1, ir.OpAdd, false)
	stream.Append(machine.Instruction{Op: machine.OpNarrow, A: 3, B: 3, Imm: 32, FlagRequest: machine.FlagZero})
	stream.BindFlagValue(2, 10, -1, ir.OpCastUnsigned, false)
	stream.AliasCarryValue(2, 10, 32)
	if flags := stream.Flags(); !flags.Zero.MatchesValue(2) || !flags.Carry.MatchesValue(2) || flags.Carry.Condition != machine.BranchCarry32 {
		t.Fatalf("32-bit arithmetic facts=%+v", flags)
	}
	stream.Append(machine.Instruction{Op: machine.OpSignExtend, A: 3, B: 3, Imm: 32, FlagRequest: machine.FlagSign | machine.FlagZero})
	stream.BindFlagValue(3, 2, -1, ir.OpSignExtend, false)
	flags := stream.Flags()
	if !flags.Sign.MatchesValue(3) || !flags.Zero.MatchesValue(2) || !flags.Zero.MatchesValue(3) || !flags.Carry.MatchesValue(2) {
		t.Fatalf("extension lost equivalent predicates: %+v", flags)
	}
	stream.Append(machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: ir.OpSub, Bits: 64, A: 3, B: 4, Imm: 7})
	stream.BindFlagValue(4, 0, -1, ir.OpSub, true)
	if flags := stream.Flags(); flags.Carry.Immediate != 7 || !flags.Carry.NoBorrow || flags.Zero.MatchesValue(2) {
		t.Fatalf("new arithmetic retained stale aliases: %+v", flags)
	}
	stream.ForgetFlags()
	if flags := stream.Flags(); flags.Sign.Valid || flags.Zero.Valid || flags.Carry.Valid {
		t.Fatalf("forgotten facts survived: %+v", flags)
	}
}

func TestSignExtensionTruncationDoesNotAliasOldZero(t *testing.T) {
	stream := flagStream(t)
	stream.Append(machine.Instruction{Op: machine.OpBinary, Arithmetic: ir.OpAdd, Bits: 64, A: 3, B: 4, C: 5})
	stream.BindFlagValue(2, 0, 1, ir.OpAdd, false)
	stream.Append(machine.Instruction{Op: machine.OpSignExtend, A: 3, B: 3, Imm: 8, FlagRequest: machine.FlagSign | machine.FlagZero})
	stream.BindFlagValue(3, 2, -1, ir.OpSignExtend, false)
	if flags := stream.Flags(); flags.Zero.MatchesValue(2) || !flags.Zero.MatchesValue(3) {
		t.Fatalf("truncating extension invented zero equivalence: %+v", flags)
	}
}

func TestFlagSnapshotsDetachAliases(t *testing.T) {
	stream := flagStream(t)
	stream.Append(machine.Instruction{Op: machine.OpBinary, Arithmetic: ir.OpAdd, Bits: 32, A: 3, B: 4, C: 5})
	stream.BindFlagValue(10, 0, 1, ir.OpAdd, false)
	stream.Append(machine.Instruction{Op: machine.OpNarrow, A: 3, B: 3, Imm: 32, FlagRequest: machine.FlagZero})
	stream.BindFlagValue(2, 10, -1, ir.OpCastUnsigned, false)
	stream.AliasCarryValue(2, 10, 32)
	flags := stream.Flags()
	flags.Carry.Aliases[0] = 99
	if stream.Flags().Carry.MatchesValue(99) {
		t.Fatal("modifying a flag snapshot mutated the emitter's facts")
	}
}

func TestIdentityAndWideningFlagAliasesAreWidthProven(t *testing.T) {
	for _, width := range []uint8{8, 16, 32, 64} {
		for _, destination := range []uint8{8, 16, 32, 64} {
			s := machine.NewStream(machine.StreamOptions{FlagEffect: func(machine.Instruction) bool { return true }, Size: func(machine.Instruction) int { return 4 }, FlagFacts: func(machine.Instruction) machine.FlagState {
				return machine.FlagState{Modified: machine.FlagSign | machine.FlagZero | machine.FlagCarry, Zero: machine.FlagFact{Valid: true, Width: width}, Sign: machine.FlagFact{Valid: true, Width: width}, Carry: machine.FlagFact{Valid: true, Width: width}}
			}})
			s.Append(machine.Instruction{Op: machine.OpBinary})
			s.BindFlagValue(1, 0, 0, ir.OpAdd, false)
			before := s.Flags()
			s.AliasFlagValue(2, 1, destination)
			after := s.Flags()
			if after.Zero.MatchesValue(2) != (width <= destination) || after.Sign.MatchesValue(2) != (width == destination) || after.Carry.MatchesValue(2) != (width == destination) {
				t.Fatalf("width%d -> %d: %+v", width, destination, after)
			}
			if !after.Zero.MatchesValue(1) || before.Zero.MatchesValue(2) {
				t.Fatal("alias lost its source or mutated a prior snapshot")
			}
		}
	}
}
