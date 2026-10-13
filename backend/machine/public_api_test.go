package machine_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/machine"
)

// Exported fields may be reordered, since callers construct these structs
// with keyed fields, but none may disappear or be renamed.
func TestPublicStructFieldCompatibility(t *testing.T) {
	cases := []struct {
		value  any
		fields string
	}{
		{machine.Instruction{}, "Data VectorType Op Bits FlagRequest PreserveFlags Symbol Arithmetic Condition Comparison ByteOrder A B C D E HighA HighB HighC CarryMode Imm Disp CallAreaBytes Slot Indexed Long Far"},
		{machine.SelectedInstruction{}, "Variable Data LiteralMask Type Op Bits ByteOrder ZeroExtended Indexed Symbol Arithmetic Dst A B C Imm Disp Args Effect"},
		{machine.HomePlan{}, "Slots FloatHomes AdditionalRegisters Regions Modified Parents Captures Reserved Words EntrySlots ReservedSlots HasFloatingHomes VariableRegions VariableBits RegionBounds ReservedRegions RegisterVariables RegisterOverrides"},
		{machine.StreamOptions{}, "Size FlagEffect FlagFacts BinarySetsFlags DecSetsCarry ExactFlagFacts"},
		{machine.Relocation{}, "TargetOffset Symbol Offset Encoding"},
	}
	for _, c := range cases {
		typ := reflect.TypeOf(c.value)
		t.Run(typ.Name(), func(t *testing.T) {
			for _, name := range strings.Fields(c.fields) {
				if _, ok := typ.FieldByName(name); !ok {
					t.Errorf("lost field %s", name)
				}
			}
		})
	}
}

func TestHomePlanDenseAndCompactForms(t *testing.T) {
	register := machine.Home{Slot: 7, Register: 3, Bits: 32}
	spill := machine.Home{Slot: 8, Register: -1, Bits: 64}
	dense := machine.HomePlan{
		Regions:    map[int]map[int]machine.Home{1: {0: register, 1: spill}},
		FloatHomes: []bool{false, true},
		// Dense Regions remain authoritative even when visibility metadata exists.
		Slots: []int{7, 8, 9}, VariableRegions: []int{0, 1, 0},
		RegionBounds: map[int][2]int{0: {0, 100}, 1: {10, 50}},
	}
	for id, want := range []machine.Home{register, spill} {
		if got, ok := dense.Lookup(1, id); !ok || got != want {
			t.Fatalf("dense Var %d: %v, %v; want %v", id, got, ok, want)
		}
		if got := dense.Regions[1][id]; got != want {
			t.Fatalf("Regions changed: %v", got)
		}
	}
	if _, ok := dense.Lookup(1, 2); ok {
		t.Fatal("dense plan invented a home absent from Regions")
	}
	if !dense.RegisterReserved(1, 3) || dense.RegisterReserved(1, 4) || !dense.NeedsFloating() {
		t.Fatal("dense plan queries changed")
	}

	compact := machine.HomePlan{
		Slots: []int{7, 8, 9}, VariableBits: []uint8{32, 64, 64},
		VariableRegions:   []int{0, 1, 2},
		RegionBounds:      map[int][2]int{0: {0, 100}, 1: {10, 50}, 2: {60, 90}},
		RegisterOverrides: map[int]map[int]machine.Home{1: {0: register}},
	}
	if got, ok := compact.Lookup(1, 0); !ok || got != register {
		t.Fatalf("compact register = %v, %v", got, ok)
	}
	if got, ok := compact.Lookup(1, 1); !ok || got != spill {
		t.Fatalf("compact spill = %v, %v", got, ok)
	}
	if _, ok := compact.Lookup(1, 2); ok {
		t.Fatal("sibling Var visible in compact plan")
	}
	if !compact.RegisterReserved(1, 3) || compact.RegisterReserved(1, 4) {
		t.Fatal("compact plan reservation fallback failed")
	}
	if compact.Regions != nil {
		t.Fatal("compact lookup materialized dense Regions")
	}
}
