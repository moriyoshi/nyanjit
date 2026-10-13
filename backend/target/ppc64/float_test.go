package ppc64_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
)

// Independent words from Go's PPC64 assembler testdata. Single precision
// differs in primary opcode; FMUL/FMADD put the multiplier in FRC.
func TestFloatingEncodingMatchesGoAssemblerVectors(t *testing.T) {
	for _, test := range []struct {
		ins  ppc64.Instruction
		word uint32
	}{
		{ppc64.Instruction{Op: ppc64.Fadd, A: 3, B: 2, C: 1, Bits: 64}, 0xfc62082a},
		{ppc64.Instruction{Op: ppc64.Fadd, A: 3, B: 2, C: 1, Bits: 32}, 0xec62082a},
		{ppc64.Instruction{Op: ppc64.Fsub, A: 3, B: 2, C: 1, Bits: 64}, 0xfc620828},
		{ppc64.Instruction{Op: ppc64.Fsub, A: 3, B: 2, C: 1, Bits: 32}, 0xec620828},
		{ppc64.Instruction{Op: ppc64.Fmul, A: 3, B: 2, C: 1, Bits: 64}, 0xfc620072},
		{ppc64.Instruction{Op: ppc64.Fmul, A: 3, B: 2, C: 1, Bits: 32}, 0xec620072},
		{ppc64.Instruction{Op: ppc64.Fdiv, A: 3, B: 2, C: 1, Bits: 64}, 0xfc620824},
		{ppc64.Instruction{Op: ppc64.Fdiv, A: 3, B: 2, C: 1, Bits: 32}, 0xec620824},
		{ppc64.Instruction{Op: ppc64.Fmadd, A: 4, B: 1, C: 3, D: 2, Bits: 64}, 0xfc8110fa},
		{ppc64.Instruction{Op: ppc64.Fmadd, A: 4, B: 1, C: 3, D: 2, Bits: 32}, 0xec8110fa},
		{ppc64.Instruction{Op: ppc64.Mtvsrd, A: 1, B: 3}, 0x7c230166},
		{ppc64.Instruction{Op: ppc64.Mfvsrd, A: 3, B: 1}, 0x7c230066},
		{ppc64.Instruction{Op: ppc64.Xxsldwi, A: 3, B: 1, C: 2, Shift: 1}, 0xf0611110},
	} {
		got, err := ppc64.Encode(test.ins)
		if err != nil || got != test.word {
			t.Errorf("%+v = %#x,%v; want %#x", test.ins, got, err, test.word)
		}
	}
}

func TestFloatingVisitorsAgreeOnExpansionAndResources(t *testing.T) {
	for _, architecture := range []string{"ppc64le"} {
		for _, width := range []uint8{32, 64} {
			for _, fused := range []bool{false, true} {
				emitter := emitterFor(t, architecture, abi.StackNoUse)
				ins := machine.Instruction{Op: machine.OpFloatBinary, Bits: width, Arithmetic: op.OpFSub, A: 3, B: 3, C: 4}
				if fused {
					ins.Op = machine.OpFloatFMA
					ins.D = 5
				}
				sequence, err := emitter.Expand(ins, 0x100)
				if err != nil {
					t.Fatal(err)
				}
				want := 4
				if fused {
					want = 5
				}
				if width == 32 {
					want = 10 // POWER8 word-0 rotation before MFVSRWZ.
					if fused {
						want = 13
					}
				}
				if len(sequence) != want || emitter.Size(ins) != want*4 {
					t.Fatalf("width %d fused %v size=%d instructions=%d", width, fused, emitter.Size(ins), len(sequence))
				}
				bytes := ppc64.NewByteAssembler()
				text := ppc64.NewTextAssembler()
				for i, native := range sequence {
					if native.PC != 0x100+i*4 {
						t.Fatal("incorrect concrete PC")
					}
					if err := bytes.Visit(native); err != nil {
						t.Fatal(err)
					}
					if err := text.Visit(native); err != nil {
						t.Fatal(err)
					}
				}
				if bytes.AssembledSize() != want*4 || text.AssembledSize() != want*4 {
					t.Fatal("visitor size mismatch")
				}
				if strings.Contains(text.Text(), "stfd") || strings.Contains(text.Text(), "ld ") {
					t.Fatalf("FP expansion uses memory:\n%s", text.Text())
				}
				if emitter.FlagEffect(ins) {
					t.Fatal("non-record FP arithmetic must preserve CR/XER facts")
				}
				if got := emitter.SpecialWrites(ins); !slices.Equal(got, []machine.Resource{{Class: machine.ResourceFloatingStatus}}) {
					t.Fatalf("FP status = %v", got)
				}
				writes := emitter.ImplicitWrites(ins)
				expected := []backend.Register{3}

				if !slices.Equal(writes, expected) {
					t.Fatalf("GPR writes=%v; want %v", writes, expected)
				}
			}
		}
	}
}

func TestFloatingOperandsRejectedBeforeAssembly(t *testing.T) {
	emitter := emitterFor(t, "ppc64le", abi.StackNoUse)
	for _, ins := range []machine.Instruction{
		{Op: machine.OpFloatBinary, Bits: 8, Arithmetic: op.OpFAdd, A: 3, B: 4, C: 5},
		{Op: machine.OpFloatFMA, Bits: 128, A: 3, B: 4, C: 5, D: 6},
		{Op: machine.OpFloatBinary, Bits: 64, Arithmetic: op.OpAdd, A: 3, B: 4, C: 5},
	} {
		if _, err := emitter.Expand(ins, 0); !errors.Is(err, machine.ErrUnsupportedOperation) {
			t.Fatalf("invalid FP operation=%v", err)
		}
		if emitter.Size(ins) != 0 {
			t.Fatal("invalid FP operation has nonzero size")
		}
	}
	for _, ins := range []ppc64.Instruction{
		{Op: ppc64.Fmadd, A: 1, B: 2, C: 3, D: 32, Bits: 64},
		{Op: ppc64.Fadd, A: 1, B: 2, C: 3, Bits: 16},
		{Op: ppc64.Mtvsrd, A: 32, B: 2},
		{Op: ppc64.Xxsldwi, A: 1, B: 1, C: 1, Shift: 4},
	} {
		if _, err := ppc64.Encode(ins); !errors.Is(err, machine.ErrUnsupportedOperation) {
			t.Fatalf("invalid concrete FP instruction=%v", err)
		}
	}
}
