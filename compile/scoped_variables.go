package compile

import (
	"sort"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

func planVariableHomes(p *ir.Program, t machineTarget, stack ...abi.StackPractice) *machine.HomePlan {
	return planVariableHomesWithFacts(p, t, nil, false, stack...)
}

func planVariableHomesWithFacts(p *ir.Program, t machineTarget, facts *programFacts, harden bool, stack ...abi.StackPractice) *machine.HomePlan {
	return planVariableHomesInto(p, t, facts, nil, harden, stack...)
}

// planVariableHomesInto assigns variable homes outside every fixed register
// that lowering of p can write. With harden, blinding later rewrites selected
// immediates, so those rewrites' registers are excluded too.
func planVariableHomesInto(p *ir.Program, t machineTarget, facts *programFacts, buffers *homeBuffers, harden bool, stack ...abi.StackPractice) *machine.HomePlan {
	if t == nil || len(p.Variables) == 0 {
		return nil
	}
	if facts == nil {
		facts = newProgramFacts(p)
	}
	if buffers == nil {
		buffers = new(homeBuffers)
	}
	plan := buffers.begin(len(p.Variables), len(facts.Scopes()))
	plan.RegionBounds, plan.FloatHomes = facts.ScopeBounds(), facts.FloatingVariables()

	for _, s := range p.ScopeExits {
		plan.Parents[s.ID] = s.Parent
	}
	plan.FloatHomes = facts.FloatingVariables()
	for _, floating := range plan.FloatHomes {
		plan.HasFloatingHomes = plan.HasFloatingHomes || floating
	}
	wordBits := int(t.RegisterBits())
	for id, v := range p.Variables {
		plan.VariableRegions[id], plan.VariableBits[id] = v.Region, uint8(v.Type.Bits())
		plan.Slots[id] = plan.ReservedSlots
		plan.Words[id] = (int(v.Type.Bits()) + wordBits - 1) / wordBits
		plan.ReservedSlots += plan.Words[id]
		plan.Captures[id] = v.Immutable
	}
	var entryBuffer [16]int
	for _, words := range entryWordsWithFacts(p.Instructions, t.RegisterBits(), entryBuffer[:0], facts) {
		plan.EntrySlots = append(plan.EntrySlots, plan.ReservedSlots)
		plan.ReservedSlots += words
	}
	var blocked [256]bool
	blocked[t.ScratchRegister()], blocked[t.WorkspaceRegister()] = true, true
	// Control-flow fragments use the ABI result register for intermediate
	// results and predicate reloads. In a straight-line body the final result
	// transfer happens after the last home use, and call lowering preserves
	// clobbered homes. Fixed arithmetic writes are excluded by the scan below.
	if p.Control != nil {
		blocked[t.ResultRegister()] = true
	}
	if writes, ok := t.(interface {
		ImplicitWrites(machine.Instruction) []physReg
	}); ok {
		var expansion [2]machine.Instruction
		for n := range p.Instructions {
			i := &p.Instructions[n]
			if i.Op == ir.OpCallC || i.Op == ir.OpCallGo || i.Op == ir.OpSyscall {
				blocked[t.CallTargetRegister()] = true
				continue
			}
			count := homeExpansion(t, p, i, &expansion)
			for _, physical := range expansion[:count] {
				for _, r := range writes.ImplicitWrites(physical) {
					blocked[r] = true
				}
			}
		}
		if harden {
			for _, physical := range hardenedHomeExpansion(t) {
				for _, r := range writes.ImplicitWrites(physical) {
					blocked[r] = true
				}
			}
		}
	}
	registers := allocationRegisters(t, nil, stack...)
	demand := scopeTemporaryDemandInto(p, t.RegisterBits(), facts, &buffers.demand, &buffers.ending)
	// Count instruction-local operands even when their producers are constants,
	// Get snapshots, or coalescible Set results. These still need capacity.
	for scope := range plan.Parents {
		demand[facts.ScopeIndex(scope)] = max(3, demand[facts.ScopeIndex(scope)])
	}
	fpDemand := scopeFloatTemporaryDemandInto(p, facts, &buffers.fpDemand, &buffers.ending)
	volatileCount := 0
	for _, r := range registers {
		if !blocked[r] {
			volatileCount++
		}
	}
	scopes := append(appendBufferTracked(&buffers.scopes, &buffers.history.scopes, len(facts.Scopes())), facts.Scopes()...)
	buffers.scopes = scopes
	bounds := facts.ScopeBounds()
	sort.Slice(scopes, func(i, j int) bool { return bounds[scopes[i]][0] < bounds[scopes[j]][0] })
	banks := resetBufferTracked(&buffers.banks, &buffers.history.banks, len(p.Variables))
	localBanks := resetBufferTracked(&buffers.localBanks, &buffers.history.localBanks, len(scopes))
	bankCounts := resetBufferTracked(&buffers.bankCounts, &buffers.history.bankCounts, len(scopes))
	for id, v := range p.Variables {
		bank := -1
		if plan.FloatHomes[id] {
			bank = 1
		} else if plan.Words[id] == 1 {
			bank = 0
		}
		banks[id] = bank
		if bank >= 0 {
			bankCounts[facts.ScopeIndex(v.Region)][bank]++
		}
	}
	bankData := resetBufferTracked(&buffers.bankData, &buffers.history.bankData, len(p.Variables))
	bankOffset := 0
	for scope, counts := range bankCounts {
		for bank, count := range counts {
			localBanks[scope][bank] = bankData[bankOffset : bankOffset : bankOffset+count]
			bankOffset += count
		}
	}
	for id, v := range p.Variables {
		if bank := banks[id]; bank >= 0 {
			scope := facts.ScopeIndex(v.Region)
			localBanks[scope][bank] = append(localBanks[scope][bank], id)
		}
	}
	needed := false
	visibleCounts := resetBufferTracked(&buffers.visibleCounts, &buffers.history.visibleCounts, len(scopes))
	for _, scope := range scopes {
		var count [2]int
		if parent := plan.Parents[scope]; parent >= 0 {
			count = visibleCounts[facts.ScopeIndex(parent)]
		}
		local := localBanks[facts.ScopeIndex(scope)]
		count[0] += len(local[0])
		count[1] += len(local[1])
		visibleCounts[facts.ScopeIndex(scope)] = count
		if count[0] > min(volatileCount, len(registers)-demand[facts.ScopeIndex(scope)]) {
			needed = true
		}
	}
	if needed {
		plan.AdditionalRegisters = preservedAllocatable(t)
		registers = append(append([]physReg(nil), registers...), plan.AdditionalRegisters...)
	}
	candidates := appendBufferTracked(&buffers.candidates, &buffers.history.candidates, len(registers))
	defer func() { buffers.candidates = candidates }()
	for _, r := range registers {
		if !blocked[r] {
			candidates = append(candidates, r)
		}
	}
	var entryRegisters [256]bool
	for n := 0; n < p.Arity && n < 6; n++ {
		if r, ok := t.ParamRegister(n); ok {
			entryRegisters[r] = true
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return !entryRegisters[candidates[i]] && entryRegisters[candidates[j]] })
	// The result register holds the final value transfer; offer it last.
	result := t.ResultRegister()
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i] != result && candidates[j] == result })
	// A straight-line program that returns a variable's value can keep that
	// variable in the result register and skip the final transfer.
	returned := -1
	if p.Control == nil && p.Result >= 0 && p.Instructions[p.Result].Op == ir.OpGet {
		returned = p.Instructions[p.Result].Variable
	}
	hasCalls := false
	for _, i := range p.Instructions {
		hasCalls = hasCalls || i.Op == ir.OpCallC || i.Op == ir.OpCallGo || i.Op == ir.OpSyscall
	}
	if preserved, ok := t.(interface{ PreservesRegister(physReg) bool }); ok && hasCalls {
		sort.SliceStable(candidates, func(i, j int) bool {
			return preserved.PreservesRegister(candidates[i]) && !preserved.PreservesRegister(candidates[j])
		})
	}
	queues := [2]*homeCandidates{&buffers.queues[0], &buffers.queues[1]}
	for _, q := range queues {
		q.reset(len(p.Variables))
	}
	active := resetBufferTracked(&buffers.active, &buffers.history.active, len(p.Variables))
	cursors := resetBufferTracked(&buffers.cursors, &buffers.history.cursors, len(p.Variables))
	nextUse := func(id int) int {
		if cursors[id] == len(facts.VarUses(id)) {
			return len(p.Instructions) + 1
		}
		return facts.VarUses(id)[cursors[id]]
	}
	position := 0
	scopeStack := appendBufferTracked(&buffers.scopeStack, &buffers.history.scopeStack, len(scopes))
	defer func() { buffers.scopeStack = scopeStack }()
	for _, scope := range scopes {
		parentScope := plan.Parents[scope]
		for len(scopeStack) != 0 && scopeStack[len(scopeStack)-1] != parentScope {
			leaving := scopeStack[len(scopeStack)-1]
			scopeStack = scopeStack[:len(scopeStack)-1]
			for _, id := range facts.Locals(leaving) {
				active[id] = false
				if bank := banks[id]; bank >= 0 {
					queues[bank].set(id, noHomeCandidate)
				}
			}
		}
		bounds := facts.ScopeBounds()[scope]
		for position < bounds[0] {
			ins := p.Instructions[position]
			if ins.Op == ir.OpGet || ins.Op == ir.OpSet {
				id := ins.Variable
				cursors[id]++
				if active[id] && banks[id] >= 0 {
					queues[banks[id]].set(id, nextUse(id))
				}
			}
			position++
		}
		row := &buffers.rows[facts.ScopeIndex(scope)]
		homes := row.homes
		for bank, floating := range []bool{false, true} {
			pool := candidates
			capacity := min(len(pool), max(0, len(registers)-demand[facts.ScopeIndex(scope)]))
			if floating {
				pool = floatRegisters(t.Architecture())
				capacity = max(0, len(pool)-fpDemand[facts.ScopeIndex(scope)])
			}
			free := append(appendBufferTracked(&buffers.free, &buffers.history.free, len(pool)), pool...)
			buffers.free = free
			remainingHomes := capacity
			locals := localBanks[facts.ScopeIndex(scope)][bank]
			assign := func(id, want int) {
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
				homes[id] = machine.Home{Slot: plan.Slots[id], Register: int(r), Bits: plan.VariableBits[id]}
				plan.Reserved[r] = true
			}
			assignLocals := func() {
				if !floating {
					for _, id := range locals {
						x := p.Variables[id].Initial[0]
						if p.Instructions[x].Op != ir.OpParam {
							continue
						}
						reg, ok := t.ParamRegister(facts.ParamIndex(x))
						if !ok {
							continue
						}
						want := int(reg)
						for _, r := range free {
							if int(r) == want {
								assign(id, want)
								break
							}
						}
					}
					if _, assigned := homes[returned]; returned >= 0 && !assigned && banks[returned] == 0 && p.Variables[returned].Region == scope {
						for _, r := range free {
							if r == result {
								assign(returned, int(result))
								break
							}
						}
					}
				}
				for _, id := range locals {
					if remainingHomes == 0 {
						break
					}
					if _, assigned := homes[id]; !assigned {
						assign(id, -1)
					}
				}
			}
			if visibleCounts[facts.ScopeIndex(scope)][bank] <= capacity {
				outer := appendBufferTracked(&buffers.outer, &buffers.history.outer, len(p.Variables))
				for id := queues[bank].first(0, len(p.Instructions)+2); id >= 0; id = queues[bank].first(id+1, len(p.Instructions)+2) {
					outer = append(outer, id)
				}
				buffers.outer = outer
				for _, id := range outer {
					if old, ok := plan.Lookup(parentScope, id); ok && old.Register >= 0 {
						assign(id, old.Register)
					}
				}
				assignLocals()
				for _, id := range outer {
					if _, assigned := homes[id]; !assigned {
						assign(id, -1)
					}
				}
			} else {
				assignLocals()
				for id := queues[bank].first(0, bounds[1]); remainingHomes > 0 && id >= 0; id = queues[bank].first(id+1, bounds[1]) {
					old, _ := plan.Lookup(parentScope, id)
					assign(id, old.Register)
				}
			}
		}
		var mask [4]uint64
		ids := appendBuffer(&row.ids, len(homes))
		modified := row.modified
		for id, h := range homes {
			mask[h.Register/64] |= uint64(1) << (uint(h.Register) % 64)
			ids = append(ids, id)
			if facts.VariableUsed(id, scope, true) {
				modified[id] = true
			}
		}
		sort.Ints(ids)
		row.ids = ids
		row.history.Observe(len(homes))
		plan.RegisterOverrides[scope], plan.Modified[scope] = homes, modified
		plan.ReservedRegions[scope], plan.RegisterVariables[scope] = mask, ids
		for _, id := range facts.Locals(scope) {
			active[id] = true
			if bank := banks[id]; bank >= 0 {
				queues[bank].set(id, nextUse(id))
			}
		}
		scopeStack = append(scopeStack, scope)
	}

	return plan
}

func bindVariableHomes(program *machineProgram, source *ir.Program, start int, plan *machine.HomePlan, context ...*programFacts) {
	if source == nil || plan == nil {
		return
	}
	facts := factsFor(source, context)
	program.HomePlan = plan
	program.HomeRegion = facts.ScopeAt(start)
	program.ReservedSlots = max(program.ReservedSlots, plan.ReservedSlots)
	extra := 0
	boundaries := false
	for _, ins := range program.Instructions {
		boundaries = boundaries || ins.Op == machine.SelectedHomeBoundary
		if ins.Op == machine.SelectedHomeBoundary && ins.Arithmetic == ir.OpScopeEnter {
			for _, id := range facts.Locals(int(ins.Imm)) {
				if v := source.Variables[id]; v.Immutable {
					extra += len(v.Initial)
				}
			}
		}
	}
	if !boundaries {
		// Nothing to rewrite: the copy below would equal the input.
		return
	}
	out := make([]machineInstruction, 0, len(program.Instructions)+extra)
	positions := make([]int, len(program.Instructions)+1)
	for index, i := range program.Instructions {
		positions[index] = len(out)
		if i.Op == machine.SelectedHomeBoundary {
			i.Args = nil
		}
		out = append(out, i)
		if i.Op == machine.SelectedHomeBoundary && i.Arithmetic == ir.OpScopeEnter {
			for _, id := range facts.Locals(int(i.Imm)) {
				v := source.Variables[id]
				if v.Immutable {
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

// hardenedHomeExpansion lists the physical operations that blinding can
// introduce after selection. It turns immediate shifts and rotates into
// register-count shifts, and scaled or indexed addresses into multiplies.
func hardenedHomeExpansion(t machineTarget) []machine.Instruction {
	var out []machine.Instruction
	for _, operation := range [...]ir.Opcode{ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight} {
		for _, bits := range [...]uint8{8, 16, 32, 64} {
			out = append(out, machine.Instruction{Op: machine.OpShiftBy, Arithmetic: operation, Bits: bits})
		}
	}
	if t.RegisterBits() == 32 {
		out = append(out, machine.Instruction{Op: machine.OpBinary, Arithmetic: ir.OpMul, Bits: 64})
	}
	return out
}

// homeExpansion stores in out the physical operations whose fixed writes
// machine selection can produce for i, and returns their count. Selection
// replaces operations the target lacks with Select or 64-bit multiply
// sequences, so variable homes must avoid the registers of that replacement as
// well as those of the native operation.
func homeExpansion(t machineTarget, p *ir.Program, i *ir.Instruction, out *[2]machine.Instruction) int {
	switch i.Op {
	case ir.OpSelect, ir.OpMul, ir.OpAddScaled, ir.OpMulHigh, ir.OpMulHighSigned, ir.OpCarryAdd, ir.OpBorrowSub, ir.OpAddWithCarry, ir.OpSubWithBorrow,
		ir.OpLeadingZeros, ir.OpTrailingZeros, ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy,
		ir.OpDiv, ir.OpDivSigned, ir.OpRem, ir.OpRemSigned, ir.OpFFromSigned, ir.OpFFromUnsigned, ir.OpFToSigned, ir.OpFToUnsigned, ir.OpFCompare:
	default:
		return 0
	}
	bits := instructionBits(*i)
	split := t.RegisterBits() == 32
	wideMul := machine.Instruction{Op: machine.OpBinary, Bits: 64, Arithmetic: ir.OpMul}
	out[0] = machine.Instruction{Bits: bits, Arithmetic: i.Op}
	switch i.Op {
	case ir.OpSelect:
		if !split || bits != 64 && instructionBits(p.Instructions[i.A]) != 64 {
			return 0
		}
		out[0] = machine.Instruction{Op: machine.OpSelect, Bits: 64, Arithmetic: ir.OpSelect}
		return 1
	case ir.OpMul:
		if !split || bits != 64 {
			return 0
		}
		out[0].Op = machine.OpBinary
		return 1
	case ir.OpAddScaled:
		// A scale with neither a shift nor an address form multiplies at the
		// value width.
		if !split || bits != 64 || i.Imm&(i.Imm-1) == 0 {
			return 0
		}
		out[0] = wideMul
		return 1
	case ir.OpMulHigh:
		out[0].Op = machine.OpMulHigh
		if can, ok := t.(interface{ CanMulHigh(uint8) bool }); ok && !can.CanMulHigh(bits) {
			// The fallback forms the product with 64-bit multiplies.
			out[1] = wideMul
			return 2
		}
		return 1
	case ir.OpMulHighSigned:
		if can, ok := t.(interface{ CanMulHighSigned(uint8) bool }); ok && can.CanMulHighSigned(bits) {
			out[0].Op = machine.OpMulHighSigned
			return 1
		}
		// The fallback multiplies sign-extended operands, or corrects the
		// unsigned 64-bit high half.
		out[0] = wideMul
		if bits != 64 {
			return 1
		}
		out[1] = machine.Instruction{Op: machine.OpMulHigh, Bits: 64, Arithmetic: ir.OpMulHigh}
		return 2
	case ir.OpCarryAdd, ir.OpBorrowSub:
		out[0].Op = machine.OpCarryBit
		return 1
	case ir.OpDiv, ir.OpDivSigned, ir.OpRem, ir.OpRemSigned:
		out[0] = machine.Instruction{Op: machine.OpDivide, Bits: bits, Arithmetic: i.Op, Imm: i.Imm}
		return 1
	case ir.OpFFromSigned, ir.OpFFromUnsigned:
		out[0] = machine.Instruction{Op: machine.OpIntToFloat, Bits: bits, Arithmetic: i.Op, Imm: uint64(instructionBits(p.Instructions[i.A]))}
		return 1
	case ir.OpFCompare:
		out[0] = machine.Instruction{Op: machine.OpFloatCompare, Bits: i.Bits, Imm: i.Imm}
		return 1
	case ir.OpFToSigned, ir.OpFToUnsigned:
		out[0] = machine.Instruction{Op: machine.OpFloatToInt, Bits: bits, Arithmetic: i.Op, Imm: i.Imm, Disp: uint64(instructionBits(p.Instructions[i.A]))}
		return 1
	case ir.OpAddWithCarry, ir.OpSubWithBorrow:
		out[0].Op = machine.OpWordCarry
		return 1
	case ir.OpLeadingZeros, ir.OpTrailingZeros:
		out[0].Op = machine.OpCountZeros
		if can, ok := t.(interface {
			CanCountZeros(ir.Opcode, uint8) bool
		}); !ok || !can.CanCountZeros(i.Op, bits) {
			out[1] = machine.Instruction{Op: machine.OpSelect, Bits: bits, Arithmetic: ir.OpSelect}
			return 2
		}
		return 1
	default: // register-count shifts and rotates
		out[0].Op = machine.OpShiftBy
		if can, ok := t.(interface{ CanShiftBy(uint8) bool }); ok && !can.CanShiftBy(bits) {
			out[1] = machine.Instruction{Op: machine.OpSelect, Bits: bits, Arithmetic: ir.OpSelect}
			return 2
		}
		return 1
	}
}
