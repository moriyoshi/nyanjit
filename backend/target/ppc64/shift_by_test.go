package ppc64_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
)

func TestRegisterCountEncodingGoldenWords(t *testing.T) {
	// Independently assembled with GNU powerpc64le-linux-gnu-as -mpower8.
	for _, test := range []struct {
		ins  ppc64.Instruction
		word uint32
	}{
		{ppc64.Instruction{Op: ppc64.Sld, A: 3, B: 4, C: 5}, 0x7c832836},
		{ppc64.Instruction{Op: ppc64.Srd, A: 3, B: 4, C: 5}, 0x7c832c36},
		{ppc64.Instruction{Op: ppc64.Srad, A: 3, B: 4, C: 5}, 0x7c832e34},
		{ppc64.Instruction{Op: ppc64.Slw, A: 3, B: 4, C: 5}, 0x7c832830},
		{ppc64.Instruction{Op: ppc64.Srw, A: 3, B: 4, C: 5}, 0x7c832c30},
		{ppc64.Instruction{Op: ppc64.Sraw, A: 3, B: 4, C: 5}, 0x7c832e30},
		{ppc64.Instruction{Op: ppc64.Rldcl, A: 3, B: 4, C: 5}, 0x78832810},
		{ppc64.Instruction{Op: ppc64.Rldcl, A: 3, B: 4, C: 5, Mask: 47}, 0x78832bf0},
		{ppc64.Instruction{Op: ppc64.Rlwnm, A: 3, B: 4, C: 5, MaskEnd: 31}, 0x5c83283e},
		{ppc64.Instruction{Op: ppc64.Rlwimi, A: 3, B: 4, Shift: 8, Mask: 16, MaskEnd: 23}, 0x5083442e},
		{ppc64.Instruction{Op: ppc64.Rlwimi, A: 3, B: 4, Shift: 16, MaskEnd: 15}, 0x5083801e},
	} {
		got, err := ppc64.Encode(test.ins)
		if err != nil || got != test.word {
			t.Fatalf("%+v = %#08x, %v; want %#08x", test.ins, got, err, test.word)
		}
		test.ins.Record = true
		got, err = ppc64.Encode(test.ins)
		if err != nil || got != test.word|1 {
			t.Fatalf("record %+v = %#08x, %v", test.ins, got, err)
		}
	}
	for _, ins := range []ppc64.Instruction{{Op: ppc64.Rldcl, Mask: 64}, {Op: ppc64.Rlwnm, MaskEnd: 32}, {Op: ppc64.Rlwimi, Shift: 32}} {
		if _, err := ppc64.Encode(ins); !errors.Is(err, machine.ErrUnsupportedOperation) {
			t.Fatalf("invalid encoding %+v: %v", ins, err)
		}
	}
}

func TestRegisterCountExpansionSizeAliasesAndEffects(t *testing.T) {
	for _, architecture := range []string{"ppc64le"} {
		e := emitterFor(t, architecture, abi.StackNoUse)
		for _, width := range []uint8{8, 16, 32, 64} {
			for _, operation := range []op.Opcode{op.OpShiftLeft, op.OpShiftRight, op.OpShiftRightArithmetic, op.OpRotateLeft, op.OpRotateRight} {
				for _, dst := range []backend.Register{3, 4, 5} {
					ins := machine.Instruction{Op: machine.OpShiftBy, Arithmetic: operation, Bits: width, A: dst, B: 4, C: 5}
					sequence, err := e.Expand(ins, 16)
					if err != nil {
						t.Fatal(err)
					}
					bytes := ppc64.NewByteAssembler()
					text := ppc64.NewTextAssembler()
					for i, native := range sequence {
						if native.PC != 16+4*i {
							t.Fatal("incorrect expansion PC")
						}
						if err := bytes.Visit(native); err != nil {
							t.Fatal(err)
						}
						if err := text.Visit(native); err != nil {
							t.Fatal(err)
						}
					}
					if e.Size(ins) != bytes.AssembledSize() || e.Size(ins) != text.AssembledSize() {
						t.Fatal("size planning differs from visitors")
					}
					if !slices.Contains(e.ImplicitWrites(ins), dst) {
						t.Fatal("missing destination clobber")
					}
					modified := e.FlagFacts(ins).Modified
					want := machine.FlagSign | machine.FlagZero
					if operation == op.OpShiftRightArithmetic {
						want |= machine.FlagCarry
					}
					if operation == op.OpRotateLeft || operation == op.OpRotateRight {
						want = 0
					}
					if modified != want {
						t.Fatalf("op %d width %d flags=%d; want %d", operation, width, modified, want)
					}
				}
			}
		}
		for _, ins := range []machine.Instruction{{Op: machine.OpShiftBy, Bits: 128}, {Op: machine.OpShiftBy, Bits: 64, Arithmetic: op.OpAdd}} {
			if _, err := e.Expand(ins, 0); !errors.Is(err, machine.ErrUnsupportedOperation) {
				t.Fatalf("invalid shift accepted: %+v", ins)
			}
		}
	}
}
