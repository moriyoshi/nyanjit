package arm64

import (
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

func TestLiteralNearFarPlansAndRange(t *testing.T) {
	for _, w := range []uint8{32, 64, 128} {
		for _, far := range []bool{false, true} {
			for _, pc := range []int{0, 2 << 20} {
				disp := uint64(pc + 16)
				if far {
					disp = 0
					if pc == 0 {
						disp = 2 << 20
					}
				}
				i := machine.Instruction{Op: machine.OpLiteralLoad, A: backend.VectorRegister(3), Bits: w, Disp: disp, Long: far}
				bytes := NewByteAssembler(abi.StackNoUse, 0)
				bytes.SetPC(pc)
				text := NewTextAssembler(abi.StackNoUse, 0)
				text.SetPC(pc)
				if err := bytes.Literal(i); err != nil {
					t.Fatal(err)
				}
				if err := text.Literal(i); err != nil {
					t.Fatal(err)
				}
				if bytes.AssembledSize() != LiteralSize(i) || text.AssembledSize() != pc+LiteralSize(i) {
					t.Fatal("literal size differs from visitors")
				}
				if far && !strings.Contains(text.Text(), "sxtw") {
					t.Fatal("far offset is not sign extended")
				}
			}
		}
	}
	for _, i := range []machine.Instruction{
		{Op: machine.OpLiteralLoad, A: backend.VectorRegister(3), Bits: 64, Disp: 1 << 20},
		{Op: machine.OpLiteralLoad, A: backend.VectorRegister(3), Bits: 64, Disp: 1 << 31, Long: true},
		{Op: machine.OpLiteralLoad, A: backend.VectorRegister(3), Bits: 64, Disp: ^uint64(0), Long: true},
	} {
		for _, emit := range []func(machine.Instruction) error{NewByteAssembler(abi.StackNoUse, 0).Literal, NewTextAssembler(abi.StackNoUse, 0).Literal} {
			if err := emit(i); !errors.Is(err, backend.ErrBackendLimit) {
				t.Fatalf("range error=%v, want ErrBackendLimit", err)
			}
		}
	}
}

func TestFarLiteralEncoding(t *testing.T) {
	i := machine.Instruction{Op: machine.OpLiteralLoad, A: backend.VectorRegister(3), Bits: 64, Disp: 0x234560, Long: true}
	p, err := literalPlan(i, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	// Independent GNU as encodings for ADR ., MOVZ/MOVK, ADD SXTW and LDR D.
	want := []uint32{0x10000010, 0x5288ac11, 0x72a00471, 0x8b31c210, 0xfd400203}
	for n, i := range p.parts[:p.n] {
		if i.word != want[n] {
			t.Fatalf("instruction%d: %#x want%#x", n, i.word, want[n])
		}
	}
}
