package compile

import (
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"sort"
)

func planVariableHomes(p *ir.Program, t machineTarget, stack ...StackPractice) *machine.HomePlan {
	if t == nil || len(p.Variables) == 0 {
		return nil
	}
	plan := &machine.HomePlan{Slots: make([]int, len(p.Variables)), Words: make([]int, len(p.Variables)), Regions: map[int]map[int]machine.Home{}, Parents: map[int]int{0: -1}, Captures: map[int]bool{}, Reserved: map[physReg]bool{}, Modified: map[int]map[int]bool{}}
	for _, s := range p.ScopeExits {
		plan.Parents[s.ID] = s.Parent
	}
	plan.FloatHomes = floatingHomes(p)
	if t.RegisterBits() == 32 {
		for i := range plan.FloatHomes {
			plan.FloatHomes[i] = false
		}
	}
	wordBits := int(t.RegisterBits())
	for id, v := range p.Variables {
		plan.Slots[id] = plan.ReservedSlots
		plan.Words[id] = (int(v.Type.Bits()) + wordBits - 1) / wordBits
		plan.ReservedSlots += plan.Words[id]
		plan.Captures[id] = v.Immutable
	}
	for _, words := range entryWordsFromIR(p.Instructions, t.RegisterBits()) {
		plan.EntrySlots = append(plan.EntrySlots, plan.ReservedSlots)
		plan.ReservedSlots += words
	}
	blocked := map[physReg]bool{t.ResultRegister(): true, t.ScratchRegister(): true, t.WorkspaceRegister(): true}
	if writes, ok := t.(interface {
		ImplicitWrites(machine.Instruction) []physReg
	}); ok {
		for _, i := range p.Instructions {
			physical := machine.Instruction{Bits: instructionBits(i), Arithmetic: i.Op}
			switch i.Op {
			case ir.OpSelect:
				if t.RegisterBits() != 32 || instructionBits(i) != 64 && instructionBits(p.Instructions[i.A]) != 64 {
					continue
				}
				physical.Op, physical.Bits = machine.OpSelect, 64
			case ir.OpMul:
				if t.RegisterBits() != 32 || instructionBits(i) != 64 {
					continue
				}
				physical.Op = machine.OpBinary
			case ir.OpMulHigh:
				physical.Op = machine.OpMulHigh
			case ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy:
				physical.Op = machine.OpShiftBy
			case ir.OpCarryAdd, ir.OpBorrowSub:
				physical.Op = machine.OpCarryBit
			case ir.OpAddWithCarry, ir.OpSubWithBorrow:
				physical.Op = machine.OpWordCarry
			case ir.OpLeadingZeros, ir.OpTrailingZeros:
				physical.Op = machine.OpCountZeros
			case ir.OpCallC, ir.OpCallGo, ir.OpSyscall:
				blocked[t.CallTargetRegister()] = true
			default:
				continue
			}
			for _, r := range writes.ImplicitWrites(physical) {
				blocked[r] = true
			}
		}
	}
	registers := allocationRegisters(t, nil, stack...)
	demand := scopeTemporaryDemand(p, t.RegisterBits())
	// Count instruction-local operands even when their producers are constants,
	// Get snapshots, or coalescible Set results. These still need capacity.
	for scope := range plan.Parents {
		demand[scope] = max(3, demand[scope])
	}
	fpDemand := scopeFloatTemporaryDemand(p)
	volatileCount := 0
	for _, r := range registers {
		if !blocked[r] {
			volatileCount++
		}
	}
	needed := false
	for scope := range plan.Parents {
		count := 0
		for id, v := range p.Variables {
			current := scope
			for current >= 0 {
				if current == v.Region {
					if plan.Words[id] == 1 && !plan.FloatHomes[id] {
						count++
					}
					break
				}
				current = plan.Parents[current]
			}
		}
		if count > min(volatileCount, len(registers)-demand[scope]) {
			needed = true
		}
	}
	if needed {
		plan.AdditionalRegisters = preservedAllocatable(t)
		registers = append(append([]physReg(nil), registers...), plan.AdditionalRegisters...)
	}
	candidates := []physReg{}
	for _, r := range registers {
		if !blocked[r] {
			candidates = append(candidates, r)
		}
	}
	entryRegisters := map[physReg]bool{}
	for n := 0; n < p.Arity && n < 6; n++ {
		entryRegisters[t.ParamRegister(n)] = true
	}
	sort.SliceStable(candidates, func(i, j int) bool { return !entryRegisters[candidates[i]] && entryRegisters[candidates[j]] })
	hasCalls := false
	for _, i := range p.Instructions {
		hasCalls = hasCalls || i.Op == ir.OpCallC || i.Op == ir.OpCallGo || i.Op == ir.OpSyscall
	}
	if preserved, ok := t.(interface{ PreservesRegister(physReg) bool }); ok && hasCalls {
		sort.SliceStable(candidates, func(i, j int) bool {
			return preserved.PreservesRegister(candidates[i]) && !preserved.PreservesRegister(candidates[j])
		})
	}
	scopes := []int{}
	for id := range plan.Parents {
		scopes = append(scopes, id)
	}
	sort.Ints(scopes)
	ancestor := func(a, b int) bool {
		for b >= 0 {
			if a == b {
				return true
			}
			b = plan.Parents[b]
		}
		return false
	}
	for _, scope := range scopes {
		homes := map[int]machine.Home{}
		used := map[int]bool{}
		plan.Modified[scope] = map[int]bool{}
		for pos, i := range p.Instructions {
			if (i.Op == ir.OpGet || i.Op == ir.OpSet) && ancestor(scope, regionAt(p, pos)) {
				used[i.Variable] = true
				if i.Op == ir.OpSet && pos >= p.Variables[i.Variable].Declaration+len(p.Variables[i.Variable].Initial) {
					plan.Modified[scope][i.Variable] = true
				}
			}
		}
		for id, v := range p.Variables {
			if ancestor(v.Region, scope) {
				homes[id] = machine.Home{Slot: plan.Slots[id], Register: -1}
			}
		}
		parent := plan.Regions[plan.Parents[scope]]
		for _, floating := range []bool{false, true} {
			pool := candidates
			// Blocked home candidates (e.g. the result register) remain usable
			// as temporaries. Subtract demand from the entire allocation bank.
			capacity := min(len(pool), max(0, len(registers)-demand[scope]))
			if floating {
				pool = floatRegisters(t.Architecture())
				capacity = max(0, len(pool)-fpDemand[scope])
			}
			free := append([]physReg(nil), pool...)
			remainingHomes := capacity
			locals, outer := []int{}, []int{}
			for id, v := range p.Variables {
				if ancestor(v.Region, scope) && plan.Words[id] == 1 && plan.FloatHomes[id] == floating {
					if v.Region == scope {
						locals = append(locals, id)
					} else {
						outer = append(outer, id)
					}
				}
			}
			assign := func(id int, want int) {
				if len(free) == 0 || remainingHomes == 0 {
					return
				}
				n := 0
				for j, r := range free {
					if int(r) == want {
						n = j
						break
					}
				}
				r := free[n]
				remainingHomes--
				free = append(free[:n], free[n+1:]...)
				homes[id] = machine.Home{Slot: plan.Slots[id], Register: int(r)}
				plan.Reserved[r] = true
			}
			assignLocals := func() {
				if !floating {
					for _, id := range locals {
						x := p.Variables[id].Initial[0]
						if p.Instructions[x].Op != ir.OpParam {
							continue
						}
						param := 0
						for n := 0; n < x; n++ {
							if p.Instructions[n].Op == ir.OpParam {
								param++
							}
						}
						want := int(t.ParamRegister(param))
						for _, r := range free {
							if int(r) == want {
								assign(id, want)
								break
							}
						}
					}
				}
				for _, id := range locals {
					if homes[id].Register < 0 {
						assign(id, -1)
					}
				}
			}
			if len(locals)+len(outer) <= capacity {
				for _, id := range outer {
					if old, ok := parent[id]; ok && old.Register >= 0 {
						assign(id, old.Register)
					}
				}
				assignLocals()
				for _, id := range outer {
					if homes[id].Register < 0 {
						assign(id, -1)
					}
				}
			} else {
				assignLocals()
				for _, id := range outer {
					if used[id] {
						assign(id, parent[id].Register)
					}
				}
			}
		}
		plan.Regions[scope] = homes
	}
	return plan
}

func regionAt(p *ir.Program, pos int) int {
	region := 0
	start := -1
	for _, s := range p.ScopeExits {
		if pos > s.Enter && pos < s.Position && s.Enter >= start {
			region, start = s.ID, s.Enter
		}
	}
	return region
}

func bindVariableHomes(program *machineProgram, source *ir.Program, start int, plan *machine.HomePlan) {
	program.LoopPressure = hasLoop(source)
	if plan == nil {
		return
	}
	program.HomePlan = plan
	program.HomeRegion = regionAt(source, start)
	program.ReservedSlots = max(program.ReservedSlots, plan.ReservedSlots)
	out := []machineInstruction{}
	positions := make([]int, len(program.Instructions)+1)
	for index, i := range program.Instructions {
		positions[index] = len(out)
		if i.Op == machine.SelectedHomeBoundary {
			i.Args = nil
		}
		out = append(out, i)
		if i.Op == machine.SelectedHomeBoundary && i.Arithmetic == ir.OpScopeEnter {
			for id, v := range source.Variables {
				if v.Immutable && v.Region == int(i.Imm) {
					for part, x := range v.Initial {
						out = append(out, machineInstruction{Op: machine.SelectedHomeSet, Dst: -1, A: x - start, Variable: id, Imm: uint64(part), Bits: source.Instructions[x].Bits})
					}
				}
			}
		}
	}
	positions[len(program.Instructions)] = len(out)
	for i := range program.Scopes {
		scope := &program.Scopes[i]
		scope.Start, scope.End = positions[scope.Start], positions[scope.End]
	}
	program.Instructions = out
}
