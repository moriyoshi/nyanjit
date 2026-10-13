package ir

import (
	"reflect"
	"testing"
)

func TestInstructionRuleInputsMatchPublicInputs(t *testing.T) {
	var a, b [4]int
	for op := Opcode(0); op < opcodeEnd; op++ {
		i := Instruction{Op: op, A: 11, B: 12, C: 13, Args: []int{14, 15}}
		got := ruleInputsInto(instructionFieldRules[op], i.A, i.B, i.C, i.Args, a[:0])
		want := i.InputsInto(b[:0])
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: dense inputs=%v; public inputs=%v", op, got, want)
		}
	}
}

// Variable validation compares to a previously validated type. Check the
// shortcut against the public interpretation for every raw Bits value,
// including explicit invalid metadata and the legacy zero-width default.
func TestInstructionHasTypeMatchesValueType(t *testing.T) {
	expected := []Type{Scalar(8), Scalar(16), Scalar(32), Scalar(64), Scalar(128)}
	for _, lanes := range []uint16{2, 4, 8, 16} {
		for _, bits := range []uint8{8, 16, 32, 64} {
			if typ := Vec(lanes, bits); typ.Valid() {
				expected = append(expected, typ)
			}
		}
		expected = append(expected, Mask(lanes))
	}
	metadata := append([]Type{{}, {Kind: ScalarKind, Lanes: 0, LaneBits: 64}, {Lanes: 1, LaneBits: 64}, {Kind: Kind(255), Lanes: 1, LaneBits: 64}}, expected...)
	for _, typ := range expected {
		if !typ.Valid() {
			t.Fatal("expected type must be valid")
		}
		for _, explicit := range metadata {
			for bits := 0; bits < 256; bits++ {
				i := Instruction{Type: explicit, Bits: uint8(bits)}
				if got, want := instructionHasType(&i, typ), i.ValueType() == typ; got != want {
					t.Fatalf("metadata=%+v Bits=%d expected=%+v: got %t; want %t", explicit, bits, typ, got, want)
				}
			}
		}
	}
}
