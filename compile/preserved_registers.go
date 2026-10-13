package compile

import (
	"slices"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// Runtime, frame-pointer, workspace and instruction-expansion registers remain
// reserved. Preserved registers are added only when volatile capacity runs out.
func preservedAllocatable(t machineTarget) []physReg { return cachedPreservedRegisters(t) }
func buildPreservedAllocatable(t machineTarget) []physReg {
	var registers []physReg
	switch t.Architecture() {
	case "amd64":
		registers = []physReg{3, 12, 13, 15}
		if t.ABIFlavor() == abi.C && t.ManagedFrameLimit() == 0 {
			registers = append(registers, 5)
		}
	case "386":
		// EBX, ESI and EDI. A 386 frame saves them with EBP on entry, so its
		// policy allocates them already; without a frame they are added here
		// and saved individually. EBP stays the workspace base.
		registers = []physReg{3, 6, 7}
	case "arm64":
		for r := physReg(19); r <= 27; r++ {
			registers = append(registers, r)
		}
	case "loong64":
		registers = []physReg{24, 25, 26, 27, 28, 31}
	case "riscv64":
		for r := physReg(18); r <= 26; r++ {
			registers = append(registers, r)
		}
	case "ppc64le":
		for r := physReg(14); r <= 28; r++ {
			registers = append(registers, r)
		}
		registers = append(registers, 31)
	}
	var present [256]bool
	var availableScanBuffer [64]physReg
	for _, r := range t.Registers().AppendTo(availableScanBuffer[:0]) {
		present[r] = true
	}
	out := []physReg{}
	for _, r := range registers {
		if !present[r] {
			out = append(out, r)
		}
	}
	return out
}
func planAllocatedPreservation(t machineTarget, e machineEmitter, written *registerWrites) {
	physical := e.(*physicalEmitter)
	for _, r := range preservedWrites(t, written) {
		if !slices.Contains(physical.allocatedSaves, r) {
			physical.allocatedSaves = append(physical.allocatedSaves, r)
		}
	}
}

// unsavedPreservedWrites returns the registers in written that the C ABI
// requires the callee to preserve but e does not save.
func unsavedPreservedWrites(t machineTarget, e machineEmitter, written *registerWrites) []physReg {
	physical := e.(*physicalEmitter)
	var missing []physReg
	for _, r := range preservedWrites(t, written) {
		if !slices.Contains(physical.allocatedSaves, r) {
			missing = append(missing, r)
		}
	}
	return missing
}

// preservedWrites returns the allocatable registers in written that a C
// callee must preserve.
func preservedWrites(t machineTarget, written *registerWrites) []physReg {
	if t.ABIFlavor() != abi.C {
		return nil
	}
	effect, err := cachedCallEffect(abi.CallSpec{Architecture: t.Architecture(), Kind: abi.FunctionCall, ABI: abi.C})
	if err != nil {
		return nil
	}
	var out []physReg
	var registerBuffer [64]physReg
	banks := [2][]physReg{t.Registers().AppendTo(registerBuffer[:0]), preservedAllocatable(t)}
	if t.Architecture() == "386" {
		// The 386 frame prologue saves every preserved register its policy
		// allocates; only the added ones need their own saves.
		banks[0] = nil
	}
	for _, bank := range banks {
		for _, r := range bank {
			if written[r] && !slices.Contains(effect.ClobberedRegisters, t.RegisterName(r)) {
				out = append(out, r)
			}
		}
	}
	return out
}
func allocationRegisters(t machineTarget, p *machine.HomePlan, stack ...abi.StackPractice) []physReg {
	return cachedAllocationRegisters(t, p, stack...)
}
func buildAllocationRegisters(t machineTarget, p *machine.HomePlan, stack ...abi.StackPractice) []physReg {
	out := t.Registers().AppendTo(nil)
	if p != nil {
		out = append(out, p.AdditionalRegisters...)
	}
	if len(stack) > 0 && stack[0] == abi.StackNoUse {
		available := out[:0]
		for _, r := range out {
			if r != t.WorkspaceRegister() {
				available = append(available, r)
			}
		}
		out = available
	}
	return out
}

// savedOnFirstUse returns regalloc.Config.Preserved for the added preserved
// registers: those a C entry must save before it writes them. A managed C
// entry saves them all regardless.
func savedOnFirstUse(t machineTarget, e machineEmitter, added []physReg) backend.RegisterSet {
	if t.ABIFlavor() != abi.C {
		return backend.RegisterSet{}
	}
	var list []physReg
	for _, r := range added {
		if !slices.Contains(e.(*physicalEmitter).entrySaves, r) {
			list = append(list, r)
		}
	}
	return backend.NewRegisterSet(list...)
}
