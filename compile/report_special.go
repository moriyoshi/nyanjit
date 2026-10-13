package compile

import "github.com/moriyoshi/nyanjit/backend/machine"

func specialWritesFromPhysical(program physicalProgram, target machineTarget) []string {
	provider, ok := target.(interface {
		SpecialWrites(machine.Instruction) []machine.Resource
		ResourceName(machine.Resource) string
	})
	if !ok {
		return nil
	}
	seen := make(map[string]bool)
	// Frame linkage and ABI preservation are planned in the physical stream.
	// Report destruction at the function boundary after those restores.
	restored := make(map[string]bool)
	var result []string
	for _, located := range program.insns {
		if target.Architecture() == "ppc64le" {
			if located.ins.Op == machine.OpFrameLeave || located.ins.Op == machine.OpRestoreLink {
				restored["LR"] = true
			}
			if located.ins.Op == machine.OpRestoreCondition {
				restored["CR2"], restored["CR3"], restored["CR4"] = true, true, true
			}
		}
		for _, resource := range provider.SpecialWrites(located.ins) {
			name := provider.ResourceName(resource)
			if name != "" && !seen[name] {
				seen[name] = true
				result = append(result, name)
			}
		}
	}
	filtered := result[:0]
	for _, name := range result {
		if !restored[name] {
			filtered = append(filtered, name)
		}
	}
	return filtered
}
