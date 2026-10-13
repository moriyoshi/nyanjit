package compile

import (
	"github.com/moriyoshi/nyanjit/machine"
	"testing"
)

func TestEntryResourcePlanAlignsVectorsAndAccountsForBothWords(t *testing.T) {
	emitter := &physicalEmitter{entryResources: []entryResourceSave{
		{Save: machine.OpSaveCondition, Restore: machine.OpRestoreCondition, Words: 1, Align: 1},
		{Save: machine.OpSaveCVector, Restore: machine.OpRestoreCVector, Register: 20, Words: 2, Align: 2},
		{Save: machine.OpSaveCVector, Restore: machine.OpRestoreCVector, Register: 21, Words: 2, Align: 2},
	}}
	for _, base := range []int{0, 1, 2, 3} {
		plan, end := emitter.entryResourcePlan(base)
		if plan[0].Slot != base || plan[1].Slot%2 != 0 || plan[1].Slot < base+1 || plan[2].Slot != plan[1].Slot+2 || end != plan[2].Slot+2 {
			t.Fatalf("base %d: plan=%+v end=%d", base, plan, end)
		}
	}
}
