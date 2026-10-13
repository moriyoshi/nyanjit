package compile

import (
	"sync"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

type registerListKey struct {
	registers        [64]physReg
	count            uint8
	workspace        physReg
	excludeWorkspace bool
	extraBP          bool
	architecture     string
}

var registerLists = struct {
	sync.RWMutex
	allocation, preserved map[registerListKey][]physReg
}{allocation: make(map[registerListKey][]physReg), preserved: make(map[registerListKey][]physReg)}

// These are read-only views. Three-index slices make append allocate before
// writing, while the table avoids rebuilding a target's register lists.
func cachedAllocationRegisters(t machineTarget, p *machine.HomePlan, stack ...abi.StackPractice) []physReg {
	key := registerListKey{workspace: t.WorkspaceRegister(), excludeWorkspace: len(stack) > 0 && stack[0] == abi.StackNoUse}
	count := t.Registers().Len()
	if p != nil {
		count += len(p.AdditionalRegisters)
	}
	if count > len(key.registers) {
		return buildAllocationRegisters(t, p, stack...)
	}
	key.count = uint8(count)
	n := len(t.Registers().AppendTo(key.registers[:0]))
	if p != nil {
		copy(key.registers[n:], p.AdditionalRegisters)
	}
	registerLists.RLock()
	out, ok := registerLists.allocation[key]
	registerLists.RUnlock()
	if ok {
		return out
	}
	out = buildAllocationRegisters(t, p, stack...)
	out = out[:len(out):len(out)]
	registerLists.Lock()
	if len(registerLists.allocation) < 512 {
		registerLists.allocation[key] = out
	}
	registerLists.Unlock()
	return out
}
func cachedPreservedRegisters(t machineTarget) []physReg {
	key := registerListKey{architecture: t.Architecture(), extraBP: t.ABIFlavor() == abi.C && t.ManagedFrameLimit() == 0}
	if t.Registers().Len() > len(key.registers) {
		return buildPreservedAllocatable(t)
	}
	key.count = uint8(len(t.Registers().AppendTo(key.registers[:0])))
	registerLists.RLock()
	out, ok := registerLists.preserved[key]
	registerLists.RUnlock()
	if ok {
		return out
	}
	out = buildPreservedAllocatable(t)
	out = out[:len(out):len(out)]
	registerLists.Lock()
	if len(registerLists.preserved) < 512 {
		registerLists.preserved[key] = out
	}
	registerLists.Unlock()
	return out
}

// Physical register names and membership are fixed by architecture, independent
// of the ABI, stack policy and CPU extensions. Keep the reverse lookup private
// and immutable; call-clobber handling must not rescan the set for each name.
var physicalRegistersByArchitecture = func() map[string]map[string]physReg {
	result := make(map[string]map[string]physReg, 6)
	for _, architecture := range []string{"386", "amd64", "arm64", "riscv64", "loong64", "ppc64le"} {
		target := newMachineTarget(compileOptions{Architecture: architecture})
		var buffer [64]physReg
		registers := target.PhysicalRegisters().AppendTo(buffer[:0])
		names := make(map[string]physReg, len(registers))
		for _, register := range registers {
			name := target.RegisterName(register)
			if _, exists := names[name]; !exists {
				names[name] = register
			}
		}
		result[architecture] = names
	}
	return result
}()
