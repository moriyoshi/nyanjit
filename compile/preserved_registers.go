package compile

import (
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/regalloc"
)

// Runtime, frame-pointer, workspace and instruction-expansion registers remain
// reserved. Preserved registers are added only when volatile capacity runs out.
func preservedAllocatable(t machineTarget) []physReg {
	var registers []physReg
	switch t.Architecture() {
	case "amd64":
		registers = []physReg{3, 12, 13, 15}
		if t.ABIFlavor() == ABIC && t.ManagedFrameLimit() == 0 {
			registers = append(registers, 5)
		}
	case "arm64":
		for r := physReg(19); r <= 27; r++ {
			registers = append(registers, r)
		}
	case "riscv64":
		for r := physReg(18); r <= 26; r++ {
			registers = append(registers, r)
		}
	case "ppc64", "ppc64le":
		for r := physReg(14); r <= 28; r++ {
			registers = append(registers, r)
		}
		registers = append(registers, 31)
	}
	present := map[physReg]bool{}
	for _, r := range t.Registers() {
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
func planAllocatedPreservation(t machineTarget, e machineEmitter, written map[physReg]bool) {
	if t.ABIFlavor() != ABIC || t.Architecture() == "386" {
		return
	}
	effect, err := PreflightCall(CallSpec{Architecture: t.Architecture(), Kind: FunctionCall, ABI: ABIC})
	if err != nil {
		return
	}
	clobbered := map[string]bool{}
	for _, name := range effect.ClobberedRegisters {
		clobbered[name] = true
	}
	physical := e.(*physicalEmitter)
	for _, r := range append(append([]physReg{}, t.Registers()...), preservedAllocatable(t)...) {
		if !written[r] || clobbered[t.RegisterName(r)] {
			continue
		}
		exists := false
		for _, old := range physical.allocatedSaves {
			exists = exists || old == r
		}
		if !exists {
			physical.allocatedSaves = append(physical.allocatedSaves, r)
		}
	}
}
func allocationRegisters(t machineTarget, p *machine.HomePlan, stack ...StackPractice) []physReg {
	out := append([]physReg(nil), t.Registers()...)
	if p != nil {
		out = append(out, p.AdditionalRegisters...)
	}
	if len(stack) > 0 && stack[0] == StackNoUse {
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

// Selected liveness includes register-bank copies and architecture expansions
// that were not present in the source IR. In loops, one-time ABI saves are
// preferable to repeating pressure spills on each iteration.
func selectedGeneralPeak(p machineProgram) int {
	last := make([]int, len(p.Values))
	for id := range last {
		last[id] = -1
	}
	for pos, i := range p.Instructions {
		for _, x := range i.Inputs() {
			last[x] = pos
		}
	}
	if !p.DiscardResult {
		last[p.Result] = len(p.Instructions)
	}
	for _, x := range p.Exports {
		last[x] = len(p.Instructions)
	}
	words := func(id int) int { return max(1, int(p.Values[id].Words)) }
	active := map[int]bool{}
	live := 0
	for id, v := range p.Values {
		if v.Class == regalloc.General && !v.Constant && last[id] >= 0 && (v.Param >= 0 || v.EntryReg >= 0) {
			active[id] = true
			live += words(id)
		}
	}
	peak := live
	for pos, i := range p.Instructions {
		temporary := 0
		seen := map[int]bool{}
		for _, x := range i.Inputs() {
			if !seen[x] && p.Values[x].Class == regalloc.General && p.Values[x].Constant {
				temporary += words(x)
				seen[x] = true
			}
		}
		if i.Dst >= 0 && p.Values[i.Dst].Class == regalloc.General && !p.Values[i.Dst].Constant {
			active[i.Dst] = true
			live += words(i.Dst)
		}
		peak = max(peak, live+temporary)
		for id := range active {
			if last[id] <= pos {
				live -= words(id)
				delete(active, id)
			}
		}
	}
	return peak
}
func hasLoop(p *ir.Program) bool {
	if p == nil || p.Control == nil {
		return false
	}
	positions := map[int]int{}
	for pos, s := range p.Control.Steps {
		if s.IsLabel {
			positions[s.Label] = pos
		}
	}
	for pos, s := range p.Control.Steps {
		if !s.IsLabel && (s.Term == ir.TermJump || s.Term == ir.TermBranch) && positions[s.To] <= pos {
			return true
		}
		if !s.IsLabel && s.Term == ir.TermBranch && positions[s.Other] <= pos {
			return true
		}
	}
	return false
}
