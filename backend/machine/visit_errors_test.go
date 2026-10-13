package machine

import (
	"errors"
	"github.com/moriyoshi/nyanjit/backend"
	"testing"
)

type visitErrorAssembler struct {
	Assembler
	sticky, result error
	calls          int
}

func (a *visitErrorAssembler) Err() error { return a.sticky }
func (a *visitErrorAssembler) fail() {
	a.calls++
	a.sticky = ErrInvalidInstruction
}
func (a *visitErrorAssembler) Constant(backend.Register, uint64) { a.fail() }
func (a *visitErrorAssembler) NarrowMemory(Instruction) error {
	a.fail()
	return a.result
}
func (a *visitErrorAssembler) WordCarry(Instruction) { a.fail() }
func (a *visitErrorAssembler) MulHigh(uint8, backend.Register, backend.Register, backend.Register) {
	a.fail()
}
func (a *visitErrorAssembler) MulWide(backend.Register) { a.fail() }

func TestVisitReturnsStickyErrorsWithoutDiscardingVisitorErrors(t *testing.T) {
	visitorError := errors.New("visitor failed")
	for _, tc := range []struct {
		name   string
		op     Opcode
		result error
	}{
		{"ordinary", OpConst, nil},
		{"optional", OpLoad16, nil},
		{"optional-error", OpLoad16, visitorError},
		{"canonical-carry", OpBinaryCarry, nil},
		{"canonical-multiply", OpMulWide, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &visitErrorAssembler{result: tc.result}
			ins := Instruction{Op: tc.op}
			want := tc.result
			if want == nil {
				want = ErrInvalidInstruction
			}
			if got := Visit(ins, a); got != want {
				t.Fatalf("Visit = %v, want %v", got, want)
			}
			if a.calls != 1 || ins.Op != tc.op {
				t.Fatalf("calls=%d, caller opcode=%d", a.calls, ins.Op)
			}
		})
	}

	// A pre-existing assembler error wins before validation or dispatch.
	a := &visitErrorAssembler{sticky: visitorError}
	if got := Visit(Instruction{Op: Opcode(255)}, a); got != visitorError || a.calls != 0 {
		t.Fatalf("pre-existing error: got=%v calls=%d", got, a.calls)
	}
}
