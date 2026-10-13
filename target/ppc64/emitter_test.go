package ppc64_test

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/regalloc"
	"github.com/moriyoshi/nyanjit/target/ppc64"
)

func emitterFor(t *testing.T, architecture string, stack abi.StackPractice) *ppc64.Emitter {
	t.Helper()
	profile, err := abi.NewPPC64Profile(architecture, abi.C, abi.PPC64ELFv2)
	if err != nil {
		t.Fatal(err)
	}
	emitter, err := ppc64.NewEmitter(profile, stack)
	if err != nil {
		t.Fatal(err)
	}
	return emitter
}

// These words are independent golden vectors from Go's assembler testdata,
// src/cmd/asm/internal/asm/testdata/ppc64.s. Operand order here is nyanjit's
// destination-first order rather than Go assembly syntax.
func TestEncodingMatchesGoAssemblerVectors(t *testing.T) {
	for _, test := range []struct {
		ins  ppc64.Instruction
		word uint32
	}{
		{ppc64.Instruction{Op: ppc64.Add, A: 5, B: 4, C: 3}, 0x7ca41a14},
		{ppc64.Instruction{Op: ppc64.Mflr, A: 3}, 0x7c6802a6},
		{ppc64.Instruction{Op: ppc64.Mtlr, A: 3}, 0x7c6803a6},
		{ppc64.Instruction{Op: ppc64.Mtctr, A: 3}, 0x7c6903a6},
		{ppc64.Instruction{Op: ppc64.Neg, A: 4, B: 3}, 0x7c8300d0},
		{ppc64.Instruction{Op: ppc64.Mfcr, A: 1}, 0x7c200026},
		{ppc64.Instruction{Op: ppc64.Mtcrf, A: 1, Imm: 255}, 0x7c2ff120},
		{ppc64.Instruction{Op: ppc64.Mtcrf, A: 1, Imm: 3}, 0x7c203120},
		{ppc64.Instruction{Op: ppc64.Lfd, A: 1, B: 3, Imm: 8}, 0xc8230008},
		{ppc64.Instruction{Op: ppc64.Stfd, A: 1, B: 3, Imm: 8}, 0xd8230008},
		{ppc64.Instruction{Op: ppc64.Lvx, A: 1, B: 4, C: 3}, 0x7c2418ce},
		{ppc64.Instruction{Op: ppc64.Stvx, A: 1, B: 4, C: 3}, 0x7c2419ce},
		{ppc64.Instruction{Op: ppc64.Subf, A: 5, B: 4, C: 3}, 0x7ca32050},
		{ppc64.Instruction{Op: ppc64.And, A: 5, B: 4, C: 3}, 0x7c851838},
		{ppc64.Instruction{Op: ppc64.Or, A: 5, B: 4, C: 3}, 0x7c851b78},
		{ppc64.Instruction{Op: ppc64.Xor, A: 5, B: 4, C: 3}, 0x7c851a78},
		{ppc64.Instruction{Op: ppc64.Sradi, A: 4, B: 3, Shift: 16}, 0x7c648674},
		{ppc64.Instruction{Op: ppc64.Extsb, A: 4, B: 3}, 0x7c640774},
		{ppc64.Instruction{Op: ppc64.Extsh, A: 4, B: 3}, 0x7c640734},
		{ppc64.Instruction{Op: ppc64.Extsw, A: 4, B: 3}, 0x7c6407b4},
		{ppc64.Instruction{Op: ppc64.Rldicl, A: 6, B: 4, Mask: 15}, 0x788603c0},
		{ppc64.Instruction{Op: ppc64.Rldicr, A: 6, B: 4, Mask: 15}, 0x788603c4},
		{ppc64.Instruction{Op: ppc64.Cmp, B: 3, C: 4, Bits: 64}, 0x7c232000},
		{ppc64.Instruction{Op: ppc64.Cmpl, B: 3, C: 4, Bits: 64}, 0x7c232040},
		{ppc64.Instruction{Op: ppc64.Ldbrx, A: 5, B: 4, C: 3}, 0x7ca41c28},
		{ppc64.Instruction{Op: ppc64.Stdbrx, A: 5, B: 4, C: 3}, 0x7ca41d28},
		{ppc64.Instruction{Op: ppc64.Stdux, A: 5, B: 4, C: 3}, 0x7ca4196a},
		{ppc64.Instruction{Op: ppc64.Isel, A: 5, B: 3, C: 4, BI: 2}, 0x7ca3209e},
	} {
		word, err := ppc64.Encode(test.ins)
		if err != nil || word != test.word {
			t.Errorf("instruction %+v = %#08x, %v; want %#08x", test.ins, word, err, test.word)
		}
	}
}

func TestByteAndTextVisitorsConsumeSameExpansion(t *testing.T) {
	for _, architecture := range []string{"ppc64", "ppc64le"} {
		emitter := emitterFor(t, architecture, abi.StackPlatform)
		order := ir.ByteOrderBigEndian
		if architecture == "ppc64le" {
			order = ir.ByteOrderLittleEndian
		}
		bytes, err := ppc64.NewByteAssembler(order)
		if err != nil {
			t.Fatal(err)
		}
		text := ppc64.NewTextAssembler()
		pc := 0
		for _, ins := range []machine.Instruction{
			{Op: machine.OpFrameEnter, Imm: 64},
			{Op: machine.OpConst, A: 3, Imm: 0x8123456789abcdef},
			{Op: machine.OpShift, A: 4, B: 3, Bits: 16, Arithmetic: ir.OpRotateRight, Imm: 3},
			{Op: machine.OpSpill, A: 4, Slot: 0},
			{Op: machine.OpReload, A: 5, Slot: 0},
			{Op: machine.OpCompareImmediate, A: 5, Bits: 64, Comparison: machine.CompareUnsigned, Imm: ^uint64(0)},
			{Op: machine.OpJumpIf, Condition: ir.BranchBelow, Imm: 0, Long: true},
			{Op: machine.OpFrameLeave, Imm: 64}, {Op: machine.OpReturn},
		} {
			sequence, err := emitter.Expand(ins, pc)
			if err != nil {
				t.Fatal(err)
			}
			if len(sequence)*4 != emitter.Size(ins) {
				t.Fatal("emitter size disagrees with concrete expansion")
			}
			for _, native := range sequence {
				if err := bytes.Visit(native); err != nil {
					t.Fatal(err)
				}
				if err := text.Visit(native); err != nil {
					t.Fatal(err)
				}
			}
			pc += emitter.Size(ins)
		}
		if bytes.AssembledSize() != pc || text.AssembledSize() != pc || !strings.Contains(text.Text(), "cmpld") || !strings.Contains(text.Text(), "blr") {
			t.Fatalf("visitor layout differs: byte=%d text=%d PC=%d", bytes.AssembledSize(), text.AssembledSize(), pc)
		}
	}
}

func TestSymbolRelocationsValidateBothInstructionOrders(t *testing.T) {
	for _, order := range []ir.ByteOrder{ir.ByteOrderBigEndian, ir.ByteOrderLittleEndian} {
		architecture := "ppc64"
		var endian binary.ByteOrder = binary.BigEndian
		if order == ir.ByteOrderLittleEndian {
			architecture, endian = "ppc64le", binary.LittleEndian
		}
		emitter := emitterFor(t, architecture, abi.StackNoUse)
		sequence, err := emitter.Expand(machine.Instruction{Op: machine.OpSymbol, A: 7, Symbol: "answer"}, 0)
		if err != nil {
			t.Fatal(err)
		}
		assembler, err := ppc64.NewByteAssembler(order)
		if err != nil {
			t.Fatal(err)
		}
		for _, native := range sequence {
			if err := assembler.Visit(native); err != nil {
				t.Fatal(err)
			}
		}
		original := assembler.Bytes()
		for _, value := range []uint64{0, 0x123456789abcdef0, ^uint64(0)} {
			linked, err := machine.LinkRelocations(original, assembler.Relocations(), map[string]uint64{"answer": value})
			if err != nil {
				t.Fatal(err)
			}
			if uint16(endian.Uint32(linked)) != uint16(value>>48) || uint16(endian.Uint32(linked[4:])) != uint16(value>>32) || uint16(endian.Uint32(linked[12:])) != uint16(value>>16) || uint16(endian.Uint32(linked[16:])) != uint16(value) {
				t.Fatalf("%s symbol chunks = %x", architecture, linked)
			}
		}
		corrupt := append([]byte(nil), original...)
		corrupt[8] ^= 0x80
		if _, err := machine.LinkRelocations(corrupt, assembler.Relocations(), map[string]uint64{"answer": 42}); !errors.Is(err, regalloc.ErrInvalidFunction) {
			t.Fatalf("corrupt symbol sequence = %v", err)
		}
		if uint16(endian.Uint32(original)) != 0 {
			t.Fatal("linking mutated the original template")
		}
	}
}

func TestEncodingRejectsInvalidOperandsAndBranchRanges(t *testing.T) {
	for _, ins := range []ppc64.Instruction{
		{Op: ppc64.Addi, A: 32}, {Op: ppc64.Addi, Imm: 32768},
		{Op: ppc64.Ori, Imm: -1}, {Op: ppc64.Rldicl, Shift: 64},
		{Op: ppc64.Ld, Imm: 3}, {Op: ppc64.B, Imm: 1 << 25},
		{Op: ppc64.Bc, Imm: 32768}, {Op: ppc64.Bc, Imm: 2},
	} {
		if _, err := ppc64.Encode(ins); !errors.Is(err, machine.ErrUnsupportedOperation) {
			t.Fatalf("accepted invalid instruction %+v: %v", ins, err)
		}
	}
	emitter := emitterFor(t, "ppc64", abi.StackNoUse)
	for _, ins := range []machine.Instruction{
		{Op: machine.OpSymbol},
		{Op: machine.OpShift, Bits: 64, Arithmetic: ir.OpShiftLeft, Imm: 256},
		{Op: machine.OpNarrow, Imm: 264},
		{Op: machine.OpConst, ByteOrder: ir.ByteOrderBigEndian},
	} {
		if _, err := emitter.Expand(ins, 0); !errors.Is(err, machine.ErrUnsupportedOperation) {
			t.Fatalf("accepted malformed allocated operation %+v: %v", ins, err)
		}
	}
}
