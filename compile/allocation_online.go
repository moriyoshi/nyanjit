package compile

import (
	"fmt"
	"os"
	"slices"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/regalloc"
	"github.com/moriyoshi/nyanjit/ir"
)

// onlineAllocation routes eligible programs through regalloc.Online instead
// of the direct Residency walk. It is the internal switch of the port onto
// the regalloc.Allocator protocol and goes away when the port completes.
var onlineAllocation = os.Getenv("NYANJIT_INTERNAL_ONLINE_ALLOCATION") == "1"

// onlineSetup is what allocateAndEmitMode has computed when it hands an
// eligible program to allocateOnline.
type onlineSetup struct {
	program     machineProgram
	target      machineTarget
	emitter     machineEmitter
	final, bare bool
	temps       *allocationTemps
	config      regalloc.Config
	entry       abiEntryLayout
	written     *registerWrites
	// block is set when the program is one block of a control program's
	// Function; see allocateOnlineControl.
	block *onlineBlock
}

// onlineBuilder adds one program to a FunctionBuilder that may already
// describe other programs: the program numbers its values and positions
// from 0, and the function from values and positions.
type onlineBuilder struct {
	b                 *regalloc.FunctionBuilder
	values, positions int
}

// shift renumbers ops' values in place for the function.
func (o onlineBuilder) shift(ops []regalloc.Operand) []regalloc.Operand {
	if o.values != 0 {
		for i := range ops {
			ops[i].Value += o.values
		}
	}
	return ops
}

func (o onlineBuilder) AddValue(v regalloc.Value) int { return o.b.AddValue(v) - o.values }

// AddBlock renumbers params in place.
func (o onlineBuilder) AddBlock(params ...int) int {
	if o.values != 0 {
		for i := range params {
			params[i] += o.values
		}
	}
	return o.b.AddBlock(params...)
}

func (o onlineBuilder) AddPosition(ops ...regalloc.Operand) int {
	return o.b.AddPosition(o.shift(ops)...) - o.positions
}

func (o onlineBuilder) AddCall(effect *abi.CallEffect, ops ...regalloc.Operand) int {
	return o.b.AddCall(effect, o.shift(ops)...) - o.positions
}

func (o onlineBuilder) AddReturn(ops ...regalloc.Operand) int {
	return o.b.AddReturn(o.shift(ops)...) - o.positions
}

// AddScope returns the function's scope index, which a child names as its
// Parent.
func (o onlineBuilder) AddScope(scope regalloc.Scope) int {
	scope.Start += o.positions
	scope.End += o.positions
	return o.b.AddScope(scope)
}

func (o onlineBuilder) SetLayout(layout abi.EntryLayout) { o.b.SetLayout(layout) }

func (o onlineBuilder) SetClobbers(position int, registers backend.RegisterSet) {
	o.b.SetClobbers(o.positions+position, registers)
}

// onlineWalk walks one program of an Online function, whose positions it
// numbers from 0 and the function from positions.
type onlineWalk struct {
	a         *regalloc.Online
	positions int
}

func (w onlineWalk) Before(position int) error { return w.a.Before(w.positions + position) }
func (w onlineWalk) After(position int) error  { return w.a.After(w.positions + position) }
func (w onlineWalk) Operand(position, index int) regalloc.Group {
	return w.a.Operand(w.positions+position, index)
}
func (w onlineWalk) Memory(position, index int) regalloc.ValueLocation {
	return w.a.Memory(w.positions+position, index)
}

// onlineEligible reports whether allocateOnline handles program: one
// segment, with two-word values only on 386, whose pairs plan386 checks.
// Without a home plan, scope boundaries emit nothing.
func onlineEligible(program machineProgram, target machineTarget) bool {
	if program.Result < 0 || program.DirectExports && len(program.ExportRegisters) != len(program.Exports) {
		return false
	}
	if target.Architecture() != "386" {
		for _, v := range program.Values {
			if v.Class != backend.Float && v.Words > 1 {
				return false
			}
		}
	}
	for _, ins := range program.Instructions {
		switch ins.Op {
		case machine.SelectedHomeGet, machine.SelectedHomeSet:
			if program.HomePlan == nil || ins.Variable < 0 || ins.Variable >= len(program.HomePlan.Slots) {
				return false // the direct walk reports it
			}
		}
	}
	return true
}

// onlineFinalUses appends the values the final position reads: the result,
// unless discarded, its high part and the exports.
func onlineFinalUses(p *machineProgram, uses []int) []int {
	if !p.DiscardResult {
		uses = append(uses, p.Result)
	}
	if len(p.ResultParts) != 0 {
		uses = append(uses, p.ResultParts[1])
	}
	return append(uses, p.Exports...)
}

// describeOnline describes s.program to b: one block whose parameters are
// the entry arguments, a position per instruction, and a final position that
// reads the result, returning it through the ABI result register when the
// function ends there. A result takes a dying input's register when the
// target allows reuse (a tie); otherwise it avoids every input's register
// (an early clobber) when the target cannot reuse one of them. An
// instruction's fixed scratch registers are reserved for its position alone.
func describeOnline(s *onlineSetup, b onlineBuilder) (returns bool) {
	p := &s.program
	t := s.temps
	// Entry arguments in parameter order, and each value's class for the
	// reuse checks below.
	byParam := resetBufferTracked(&t.onlineByParam, &t.history.onlineByParam, len(s.entry.arguments))
	for i := range byParam {
		byParam[i] = -1
	}
	classes := resetBufferTracked(&t.onlineClasses, &t.history.onlineClasses, len(p.Values))
	bits := s.target.RegisterBits()
	entered := false // a value enters in a register or slot of its own
	for id := range p.Values {
		v := &p.Values[id]
		storage := uint8(max(v.Words, 1))
		if v.Class == backend.Float || v.Class == backend.Vector {
			storage = uint8(valueStorageWords(*v, bits))
		}
		b.AddValue(regalloc.Value{Class: v.Class, Words: v.Words, StorageWords: storage, Constant: v.Constant})
		classes[id] = v.Class
		if v.Param >= 0 && v.Param < len(byParam) && !v.Constant && byParam[v.Param] < 0 {
			byParam[v.Param] = id
		}
		entered = entered || v.Param < 0 && !v.Constant && (v.EntryReg >= 0 || v.EntrySlot >= 0)
	}
	// The 386 expansions' scratch registers are values of their own.
	is386 := s.target.Architecture() == "386"
	scratch := len(p.Values)
	if is386 {
		for range t.online386Scratch {
			b.AddValue(regalloc.Value{Class: backend.General, Words: 1, StorageWords: 1})
		}
	}
	params := t.onlineParams[:0]
	arguments := t.onlineArguments[:0]
	for param, id := range byParam {
		if id >= 0 {
			params = append(params, id)
			arguments = append(arguments, s.entry.arguments[param])
		}
	}
	// Values a previous segment left in registers or reserved slots enter as
	// parameters too; a control block's parameters are its label's.
	if s.block != nil {
		params = append(params, s.block.params...)
	}
	for id := range p.Values {
		if !entered || s.block != nil {
			break
		}
		v := &p.Values[id]
		if v.Param >= 0 || v.Constant {
			continue
		}
		switch {
		case v.EntryReg >= 0:
			params = append(params, id)
			arguments = append(arguments, abi.ValueLocation{Words: []abi.WordLocation{{Storage: abi.Register, Register: s.target.RegisterName(physReg(v.EntryReg))}}})
		case v.EntrySlot >= 0:
			n := max(int(v.Words), 1)
			if v.Class == backend.Float || v.Class == backend.Vector {
				n = valueStorageWords(*v, bits)
			}
			words := make([]abi.WordLocation, n)
			for k := range words {
				words[k] = abi.WordLocation{Storage: abi.Frame, Slot: v.EntrySlot + k}
			}
			params = append(params, id)
			arguments = append(arguments, abi.ValueLocation{Words: words})
		}
	}
	t.onlineParams, t.onlineArguments = params, arguments
	last := len(p.Instructions)
	// Each instruction's inputs, read once, for this and the walk, and 1 +
	// each value's last use, negated once it has two. Ties also save Online
	// the search for a register.
	x86 := s.target.Architecture() == "amd64" || s.target.Architecture() == "386"
	inputStart := resetBufferTracked(&t.onlineInputStart, &t.history.onlineInputStart, last+1)
	lastUse := resetBufferTracked(&t.onlineLastUse, &t.history.onlineLastUse, len(p.Values))
	// Stale entries are told apart by the instruction they name.
	t.history.onlineDefinedAt.Observe(len(p.Values))
	if cap(t.onlineDefinedAt) < len(p.Values) {
		t.onlineDefinedAt = make([]int, len(p.Values))
	}
	definedAt := t.onlineDefinedAt[:len(p.Values)]
	flat := t.onlineInputs[:0]
	var buffer [4]int
	for pos := range p.Instructions {
		ins := &p.Instructions[pos]
		inputStart[pos] = len(flat)
		if ins.Op == machine.SelectedHomeBoundary {
			continue
		}
		for _, id := range ins.InputsInto(buffer[:0]) {
			if lastUse[id] != 0 {
				lastUse[id] = -pos - 1
			} else {
				lastUse[id] = pos + 1
			}
			flat = append(flat, id)
		}
		if ins.Dst >= 0 {
			definedAt[ins.Dst] = pos
		}
	}
	inputStart[last] = len(flat)
	t.onlineInputs = flat
	// A control block's last position reads its terminator's values, and
	// Online reads its edges' arguments there.
	var final []int
	if s.block != nil {
		final = append(append(t.onlineFinal[:0], s.block.terminal...), s.block.escaping...)
	} else {
		final = onlineFinalUses(p, t.onlineFinal[:0])
	}
	t.onlineFinal = final
	for _, id := range final {
		lastUse[id] = -last - 1
	}
	if s.block != nil {
		final = final[:len(s.block.terminal)]
	}
	prefers := onlinePreferences(s, lastUse, definedAt)
	var homes onlineHomePlan
	if p.HomePlan != nil {
		homes = planOnlineHomes(s, lastUse)
	}
	// substituted reports whether value's use at pos reads a home in place.
	substituted := func(id, pos int) bool { return homes.inPlace(id, pos) >= 0 }
	layout := abi.EntryLayout{Arguments: arguments}
	exits := s.final && !s.bare && len(p.Exports) == 0
	if s.block != nil {
		exits = s.block.exit
	}
	returns = exits && !p.DiscardResult && len(p.ResultParts) == 0 &&
		p.Values[p.Result].Class != backend.Float && p.Values[p.Result].Class != backend.Vector &&
		len(s.entry.result.Words) == 1 && s.entry.result.Words[0].Storage == abi.Register && !substituted(p.Result, last)
	if returns {
		layout.Result = s.entry.result
	}
	if s.block == nil {
		b.SetLayout(layout) // a control program sets its function's
	}
	b.AddBlock(params...)
	fixedClobbers, _ := s.target.(interface {
		FixedClobbers(machineInstruction) []physReg
	})
	ops := t.onlineOperands[:0]
	next := 0 // the first of prefers at or after pos
	// Every segment's scope, the final position's included.
	segScopes := t.onlineSegScopes[:0]
	for _, x := range homes.segments {
		scope := -1
		if !x.registers.empty() {
			scope = b.AddScope(regalloc.Scope{Parent: -1, Start: x.start, End: x.end, Registers: x.registers.set()})
		}
		segScopes = append(segScopes, scope)
	}
	t.onlineSegScopes = segScopes
	seg, segScope, homeDef := 0, -1, 0 // pos's segment, its scope, the next of homes.defs
	call := 0                          // the next of t.onlineCalls
	var segSet homeMask
	for pos := range p.Instructions {
		ins := &p.Instructions[pos]
		ops = ops[:0]
		if homes.segments != nil && homes.segments[seg].end <= pos {
			seg++
		}
		if homes.segments != nil && homes.segments[seg].start == pos {
			segSet, segScope = homes.segments[seg].registers, segScopes[seg]
		}
		switch ins.Op {
		case machine.SelectedHomeBoundary:
			b.AddPosition() // the walk moves the homes
			continue
		case machine.SelectedHomeGet, machine.SelectedHomeSet:
			op, ok := onlineHomeOperand(s, t.onlineHomeAt[pos], ins)
			if ins.Op == machine.SelectedHomeGet && (homes.loaded(ins.Dst, pos) || lastUse[ins.Dst] == 0) || ins.Op == machine.SelectedHomeSet && substituted(ins.A, pos) {
				ok = false // read in place, or never
			}
			if ok {
				b.AddPosition(op)
			} else {
				b.AddPosition()
			}
			continue
		case mCallC, mCallGo, mSyscall:
			region := p.HomeRegion
			if homes.segments != nil {
				region = homes.segments[seg].region
			}
			ops = describeOnlineCall(s, b, call, region, segSet, &homes, ops)
			call++
			continue
		}
		inputs := flat[inputStart[pos]:inputStart[pos+1]]
		if is386 && t.online386[pos] != o386Generic {
			ops = describe386(s, b, pos, t.online386[pos], inputs, lastUse, &scratch, ops)
			continue
		}
		for _, id := range inputs {
			if !substituted(id, pos) {
				ops = append(ops, regalloc.Operand{Value: id})
			}
		}
		if ins.Dst >= 0 {
			tied, early := -1, false
			if !p.Values[ins.Dst].Constant {
				// Two-address forms write the result before reading the
				// inputs they cannot reuse, so it must avoid those even
				// when they live on and an eviction frees their registers.
				// A float result may also reuse the first input of an FP
				// instruction, or any input off x86.
				class := classes[ins.Dst]
				floatReuse := class == backend.Float && (fpInstruction(*ins) || ins.Op == machine.SelectedFloatMove)
				// An input read in place from a home cannot be reused, and
				// a two-address form must not write over another input
				// before reading it.
				reusableAt := func(i, id int) bool {
					return classes[id] == class && !(x86 && substituted(id, pos)) && (floatReuse && (!x86 || i == 0) || s.target.CanReuse(*ins, i)) &&
						(!is386 || online386Words(p, id) == 1)
				}
				// Each input's answer, asked once: CanReuse takes the
				// instruction by value.
				var reuse uint64
				for i, id := range inputs {
					if i < 64 && reusableAt(i, id) {
						reuse |= 1 << uint(i)
					}
				}
				reusable := func(i, id int) bool {
					if i < 64 {
						return reuse&(1<<uint(i)) != 0
					}
					return reusableAt(i, id)
				}
				operand := 0 // index among the operands
				for i, id := range inputs {
					if substituted(id, pos) {
						continue
					}
					if u := lastUse[id]; (u == pos+1 || u == -pos-1) && !onlineRepeated(inputs, i) && reusable(i, id) {
						tied = operand
						break
					}
					operand++
				}
				if tied < 0 && len(inputs) <= 64 && reuse != 1<<uint(len(inputs))-1 {
					early = true
				}
				for i, id := range inputs {
					if tied < 0 && i >= 64 && !reusable(i, id) {
						early = true
						break
					}
				}
				// Where the registers left beside the reservations cannot
				// hold the inputs and a result apart, tie the result to a
				// live input it may reuse, as the direct walk retries:
				// Online moves or stores that input first.
				if early && len(ops)+1 > onlineFree(s, class, segSet) {
					operand := 0
					for i, id := range inputs {
						if substituted(id, pos) {
							continue
						}
						if !onlineRepeated(inputs, i) && reusable(i, id) {
							tied, early = operand, false
							break
						}
						operand++
					}
				}
			}
			ops = append(ops, regalloc.Operand{Value: ins.Dst, Def: true, Tied: tied >= 0, TiedUse: max(tied, 0), EarlyClobber: early})
			def := &ops[len(ops)-1]
			for ; next < len(prefers) && prefers[next].position <= pos; next++ {
				if prefers[next].position == pos {
					def.Prefer = regalloc.SingleRegister(prefers[next].register)
				}
			}
			// A result stored to a register home next goes there directly.
			for ; homeDef < len(homes.defs) && homes.defs[homeDef].position < pos; homeDef++ {
			}
			if homeDef < len(homes.defs) && homes.defs[homeDef].position == pos {
				if h := homes.defs[homeDef]; h.fixed {
					def.Fixed, def.Prefer = regalloc.SingleRegister(h.register), regalloc.Group{}
				} else {
					def.Prefer = regalloc.SingleRegister(h.register)
				}
			}
		}
		b.AddPosition(ops...)
		if fixedClobbers != nil {
			if fixed := fixedClobbers.FixedClobbers(*ins); len(fixed) != 0 {
				// Within a run's scope, the instruction's reserved set is
				// the run's and its own.
				var list [72]backend.Register
				n := 0
				for _, r := range fixed {
					if s.config.Registers.Contains(r) && !segSet.contains(r) && n < 8 {
						list[n] = r
						n++
					}
				}
				if n != 0 {
					for r := range segSet.all() {
						list[n] = r
						n++
					}
					b.AddScope(regalloc.Scope{Parent: segScope, Start: pos, End: pos + 1, Registers: backend.NewRegisterSet(list[:n]...)})
				}
			}
		}
	}
	ops = ops[:0]
	for _, id := range final {
		if !substituted(id, last) {
			ops = append(ops, regalloc.Operand{Value: id})
		}
	}
	t.onlineOperands = ops
	if returns {
		b.AddReturn(ops...)
	} else {
		b.AddPosition(ops...)
	}
	return returns
}

// onlinePrefer asks for register for the definition at position.
type onlinePrefer struct {
	position int
	register physReg
}

// onlinePreferences returns, by position, the registers the definitions
// prefer, as the direct walk sets them: the registers of direct exports,
// the result's register and the program's hints, each carried back through
// a single-use input its definition can reuse. Of two at one position, the
// first of those counts: the last export's, the result's, then the lowest
// hinted value's.
func onlinePreferences(s *onlineSetup, lastUse, definedAt []int) []onlinePrefer {
	p, t := &s.program, s.temps
	definition := func(id int) (int, bool) {
		pos := definedAt[id]
		return pos, pos >= 0 && pos < len(p.Instructions) && p.Instructions[pos].Dst == id
	}
	prefers := t.onlinePrefers[:0]
	prefer := func(id int, reg physReg) {
		if !s.config.Registers.Contains(reg) {
			return
		}
		vector := backend.IsVectorRegister(reg)
		for id >= 0 && id < len(p.Values) && !p.Values[id].Constant &&
			vector == (p.Values[id].Class == backend.Float || p.Values[id].Class == backend.Vector) {
			pos, ok := definition(id)
			if !ok {
				return
			}
			prefers = append(prefers, onlinePrefer{pos, reg})
			ins := &p.Instructions[pos]
			input, at := -1, 0
			switch ins.Op {
			case mMove, mInc, mDec, mNarrow, mSignExtend, mShift, mShiftBy, mCarryBit, mWordCarry, mMulHigh, mMulHighSigned, mBinary, mFloatBinary, mFloatFMA, mFloatUnary, mBinaryImmediate, mScaled, mMulAdd:
				input = ins.A
			case mSelect:
				input, at = ins.C, 2
			}
			// The input's only use is here.
			if input < 0 || input >= len(p.Values) || lastUse[input] != pos+1 || !s.target.CanReuse(*ins, at) {
				return
			}
			id = input
		}
	}
	// The direct walk's preferences went through general register names, so
	// a value whose last export register is a vector register ends with none.
	vectorExports := false
	if p.DirectExports {
		for _, r := range p.ExportRegisters {
			vectorExports = vectorExports || r >= 0 && backend.IsVectorRegister(physReg(r))
		}
	}
	vectorExport := func(id int) bool {
		if !vectorExports {
			return false
		}
		last := -1
		for i, export := range p.Exports {
			if export == id && i < len(p.ExportRegisters) && p.ExportRegisters[i] >= 0 {
				last = p.ExportRegisters[i]
			}
		}
		return last >= 0 && backend.IsVectorRegister(physReg(last))
	}
	// A call's input prefers its register when the call is its last use,
	// it is defined in the call's innermost scope and no call comes between,
	// as the direct walk sets it last.
	if len(t.onlineCalls) != 0 {
		previous := -1 // the call before
		for k := range t.onlineCalls {
			c := &t.onlineCalls[k]
			ins := &p.Instructions[c.position]
			scope := onlineInnermostScope(p, c.position)
			for i, id := range ins.Args {
				pos, ok := definition(id)
				if u := lastUse[id]; !ok || u != c.position+1 && u != -c.position-1 || pos < scope.Start || previous > pos ||
					onlineRepeatedAfter(ins.Args, i) {
					continue
				}
				prefer(id, c.fixed[i])
			}
			previous = c.position
		}
	}
	if p.DirectExports {
		for i := len(p.Exports) - 1; i >= 0; i-- {
			id := p.Exports[i]
			if r := physReg(p.ExportRegisters[i]); p.ExportRegisters[i] >= 0 && !backend.IsVectorRegister(r) && !vectorExport(id) {
				prefer(id, r)
			}
		}
	}
	if !s.bare && !p.DiscardResult && !vectorExport(p.Result) {
		prefer(p.Result, s.target.ResultRegister())
	}
	if len(p.PreferredRegisters) != 0 {
		// In value order, so that collisions resolve the same way each time.
		ids := t.onlineHinted[:0]
		for id := range p.PreferredRegisters {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			if !vectorExport(id) {
				prefer(id, p.PreferredRegisters[id])
			}
		}
		t.onlineHinted = ids
	}
	// Each chain runs backward: reversed, one chain is in order.
	slices.Reverse(prefers)
	if !slices.IsSortedFunc(prefers, compareOnlinePrefers) {
		slices.SortStableFunc(prefers, compareOnlinePrefers)
	}
	t.onlinePrefers = prefers
	return prefers
}

func compareOnlinePrefers(a, b onlinePrefer) int { return a.position - b.position }

// onlineInnermostScope returns the innermost of p's scopes at pos, as the
// direct walk finds it.
func onlineInnermostScope(p *machineProgram, pos int) machine.SelectedScope {
	if len(p.Scopes) == 0 {
		return machine.SelectedScope{End: len(p.Instructions)}
	}
	scope := p.Scopes[0]
	for _, candidate := range p.Scopes[1:] {
		if candidate.Start <= pos && pos < candidate.End &&
			candidate.Start >= scope.Start && candidate.End <= scope.End &&
			(candidate.Start > scope.Start || candidate.End < scope.End || candidate.ID > scope.ID) {
			scope = candidate
		}
	}
	return scope
}

// onlineRepeatedAfter reports whether inputs[i] appears again after i.
func onlineRepeatedAfter(inputs []int, i int) bool {
	for _, id := range inputs[i+1:] {
		if id == inputs[i] {
			return true
		}
	}
	return false
}

// onlineFree returns how many registers of class the allocator has outside
// reserved.
func onlineFree(s *onlineSetup, class backend.Class, reserved homeMask) int {
	vector := class == backend.Float || class == backend.Vector
	n := 0
	for r := range s.config.Registers.All() {
		if backend.IsVectorRegister(r) == vector && !reserved.contains(r) {
			n++
		}
	}
	return n
}

// onlineFinalFits reports whether the registers hold every value the final
// position reads at once. The direct walk stores exports one at a time.
func onlineFinalFits(s *onlineSetup) bool {
	p := &s.program
	n := len(p.Exports) + 2
	if n <= 4 {
		return true
	}
	var general, vector int
	for r := range s.config.Registers.All() {
		if s.config.Blocked.Contains(r) {
			continue
		}
		if backend.IsVectorRegister(r) {
			vector++
		} else {
			general++
		}
	}
	final := onlineFinalUses(p, s.temps.onlineFinal[:0])
	s.temps.onlineFinal = final
	for _, id := range final {
		if c := p.Values[id].Class; c == backend.Float || c == backend.Vector {
			vector--
		} else {
			general -= max(int(p.Values[id].Words), 1)
		}
	}
	return general >= 0 && vector >= 0
}

// onlineRepeated reports whether inputs[i] already appears before i.
func onlineRepeated(inputs []int, i int) bool {
	for _, id := range inputs[:i] {
		if id == inputs[i] {
			return true
		}
	}
	return false
}

// allocateOnline allocates and emits an eligible program through
// regalloc.Online. taken is false when Online rejects the description before
// anything is emitted, and the caller then walks the program itself.
func allocateOnline(s *onlineSetup) (output physicalProgram, workspace int, writes registerWrites, taken bool, err error) {
	p, t := &s.program, s.temps
	b, a := &t.function, &t.online
	// Only a 386 program has expansions; a scratch shared across targets may
	// hold another program's.
	t.online386 = t.online386[:0]
	if !onlineFinalFits(s) || s.target.Architecture() == "386" && !plan386(s) || !planOnlineCalls(s) {
		return physicalProgram{}, 0, registerWrites{}, false, nil
	}
	b.Reset()
	describeOnline(s, onlineBuilder{b: b})
	t.observeOnline(b, len(t.onlineInputs))
	// Scopes reserve the home registers; the direct walk blocks them.
	config := s.config
	config.Blocked = backend.RegisterSet{}
	if err := a.Allocate(config, b, &t.mover); err != nil {
		return physicalProgram{}, 0, registerWrites{}, false, nil
	}
	defer a.Finish()
	fail := func(err error) (physicalProgram, int, registerWrites, bool, error) {
		return physicalProgram{}, 0, registerWrites{}, true, err
	}
	if err := a.Entry(); err != nil {
		return fail(err)
	}
	w := onlineWalk{a: a}
	// What describeOnline planned to read in place.
	var homes onlineHomePlan
	if p.HomePlan != nil {
		t.beginHomes(2 * len(p.HomePlan.Slots))
		homes.snapshot, homes.from = t.onlineSnapshot, t.onlineSnapFrom
	}
	if err := walkOnline(s, w, &homes); err != nil {
		return fail(err)
	}
	return finishOnline(s, w, &homes)
}

// walkOnline walks s.program's instructions, the final position excepted,
// emitting each with the moves Online makes around it.
func walkOnline(s *onlineSetup, a onlineWalk, homes *onlineHomePlan) error {
	p, target, emitter, written := &s.program, s.target, s.emitter, s.written
	t := s.temps
	fixedClobbers, _ := target.(interface {
		FixedClobbers(machineInstruction) []physReg
	})
	// describeOnline left each instruction's inputs in t.onlineInputs.
	flat, inputStart := t.onlineInputs, t.onlineInputStart
	var inputRegisterBuffer [4]physReg
	boundary := 0 // the next scope boundary
	call := 0     // the next of t.onlineCalls
	for position := range p.Instructions {
		ins := &p.Instructions[position]
		if isCall(ins.Op) {
			// The homes the call overwrites are stored before Online places
			// its arguments, which may take their registers.
			c := &t.onlineCalls[call]
			call++
			saveOnlineCallHomes(s, c)
			if err := a.Before(position); err != nil {
				return fmt.Errorf("position %d: %w", position, err)
			}
			emitOnlineCall(s, a, c, homes)
			if err := a.After(position); err != nil {
				return err
			}
			continue
		}
		if err := a.Before(position); err != nil {
			return fmt.Errorf("position %d: %w", position, err)
		}
		if len(t.online386) != 0 && t.online386[position] != o386Generic {
			emit386(s, a, position, t.online386[position])
			if err := a.After(position); err != nil {
				return err
			}
			continue
		}
		switch {
		case ins.Op == machine.SelectedHomeBoundary && p.HomePlan != nil:
			emitOnlineTransition(s, boundary)
			boundary++
		case ins.Op == machine.SelectedHomeGet || ins.Op == machine.SelectedHomeSet:
			emitOnlineHome(s, t.onlineHomeAt[position], position, ins, a.Operand(position, 0), homes)
		}
		if ins.Op == mConst || ins.Op == machine.SelectedHomeBoundary || ins.Op == machine.SelectedHomeGet || ins.Op == machine.SelectedHomeSet {
			if err := a.After(position); err != nil {
				return err
			}
			continue
		}
		if fixedClobbers != nil {
			for _, reg := range fixedClobbers.FixedClobbers(*ins) {
				written[reg] = true
			}
		}
		inputs := flat[inputStart[position]:inputStart[position+1]]
		var inputRegs []physReg
		if len(inputs) <= len(inputRegisterBuffer) {
			inputRegs = inputRegisterBuffer[:len(inputs)]
		} else {
			inputRegs = make([]physReg, len(inputs))
		}
		operand := 0
		for i, id := range inputs {
			if r := homes.inPlace(id, position); r >= 0 {
				inputRegs[i] = physReg(r) // read in place from its home
				continue
			}
			inputRegs[i] = a.Operand(position, operand).Registers[0]
			operand++
		}
		switch {
		case ins.Op == mVector && ins.Arithmetic == ir.OpVStore:
			physical := machine.Instruction{Op: machine.OpVector, Arithmetic: ins.Arithmetic, Bits: ins.Bits, VectorType: ins.Type, A: inputRegs[0], B: inputRegs[1], Disp: ins.Disp, Indexed: ins.Indexed}
			if ins.Indexed {
				physical.C = inputRegs[2]
			}
			emitter.vectorInstruction(physical)
		case ins.Op == mStoreAddress:
			emitter.storeAddress(inputRegs[0], inputRegs[1], inputRegs[2], ins.Imm, ins.Disp, ins.ByteOrder)
		case ins.Op == mStoreOffset:
			emitter.storeOffset(inputRegs[0], inputRegs[1], ins.Disp, ins.ByteOrder)
		case ins.Op == mStore16 || ins.Op == mStore32:
			emitter.narrowMemory(false, ins.Bits, inputRegs[0], inputRegs[1], ins.ByteOrder)
		case ins.Op == mStore8:
			emitter.store8(inputRegs[0], inputRegs[1])
		case ins.Op == mStore64:
			emitter.store64(inputRegs[0], inputRegs[1], ins.ByteOrder)
		default:
			dst := a.Operand(position, operand).Registers[0]
			selectFromFlags := false
			if ins.Op == mSelect && target.Architecture() == "arm64" {
				flags := emitter.currentFlags()
				selectFromFlags = flags.Zero.Valid && flags.Zero.Value == ins.A
			}
			var floatOperand func(id, operand int) physReg
			if ins.Op == mFloatBinary || ins.Op == mFloatUnary || ins.Op == mFloatFMA {
				floatOperand = func(id, operand int) physReg {
					if ins.LiteralMask&(1<<operand) == 0 {
						// Literal operands are not inputs: count the ones before.
						k := 0
						for n := 0; n < operand; n++ {
							if ins.LiteralMask&(1<<n) == 0 {
								k++
							}
						}
						return inputRegs[k]
					}
					n := uint8(operand)
					if target.Architecture() == "amd64" && ins.Op == mFloatFMA {
						n = []uint8{1, 2, 0}[operand]
					}
					if target.Architecture() == "arm64" {
						n += 16
					}
					reg := backend.VectorRegister(n)
					width := ins.Bits
					if ins.Op == mFloatUnary && ins.Arithmetic == ir.OpFConvert {
						width = 96 - width
					}
					emitter.(*physicalEmitter).literalValue(reg, width, p.Values[id].Imm)
					return reg
				}
			}
			if err := emitSelectedOperation(emitter, target, p.Values, ins, dst, inputRegs, selectFromFlags, written, floatOperand); err != nil {
				return err
			}
			if !backend.IsVectorRegister(dst) && (ins.Op != mMove || dst != inputRegs[0]) {
				written[dst] = true
			}
		}
		if err := a.After(position); err != nil {
			return err
		}
	}
	return nil
}

// finishOnline emits s.program's final position and finishes its body.
func finishOnline(s *onlineSetup, a onlineWalk, homes *onlineHomePlan) (output physicalProgram, workspace int, writes registerWrites, taken bool, err error) {
	p, target, emitter, written := &s.program, s.target, s.emitter, s.written
	t := s.temps
	fail := func(err error) (physicalProgram, int, registerWrites, bool, error) {
		return physicalProgram{}, 0, registerWrites{}, true, err
	}
	last := len(p.Instructions)
	if err := a.Before(last); err != nil {
		return fail(err)
	}
	// The final position reads the result, its high part, then the exports,
	// but not the values read in place from their homes.
	at := 0
	finalGroup := func(id int) regalloc.Group {
		if r := homes.inPlace(id, last); r >= 0 {
			return regalloc.SingleRegister(physReg(r))
		}
		at++
		return a.Operand(last, at-1)
	}
	var resultReg, highReg physReg
	var resultGroup regalloc.Group
	if !p.DiscardResult {
		resultGroup = finalGroup(p.Result)
		resultReg = resultGroup.Registers[0]
	}
	if len(p.ResultParts) != 0 {
		highReg = finalGroup(p.ResultParts[1]).Registers[0]
	}
	exportGroups := t.onlineExportGroups[:0]
	for _, id := range p.Exports {
		exportGroups = append(exportGroups, finalGroup(id))
	}
	t.onlineExportGroups = exportGroups
	if err := a.After(last); err != nil {
		return fail(err)
	}
	if len(t.online386Patches) != 0 {
		patch386(s, a.a.Slots())
	}
	// Exports go to fresh slots, unless the caller takes them where they are.
	var exportSlots []int
	if !p.DirectExports {
		exportSlots = make([]int, len(p.Exports))
		next := a.a.Slots()
		for i := range p.Exports {
			g := exportGroups[i]
			exportSlots[i] = next
			for word := range int(g.Words) {
				emitter.spill(g.Registers[word], next+word)
			}
			next += int(g.Words)
		}
	}
	if s.final && !p.DiscardResult {
		planAllocatedPreservation(target, emitter, written)
	}
	slots := referencedSpillSlots(emitter.(*physicalEmitter).machineStream().Instructions(), target.RegisterBits())
	switch {
	case p.DiscardResult:
		output, err = emitter.bodyBare(target.ResultRegister(), slots)
		if err != nil {
			return fail(err)
		}
		return output, workspaceBytesForSlots(target, emitter, slots), *written, true, nil
	case len(p.ResultParts) != 0 && !s.final:
		output, err = emitter.bodyBare(resultReg, slots)
		output.resultWords = [2]physReg{resultReg, highReg}
		output.resultWordCount = 2
	case len(p.ResultParts) != 0:
		pair, supported := target.Integer128Result()
		pairEmitter, ok := emitter.(interface {
			finishPair(physReg, physReg, physReg, physReg, int) (physicalProgram, int, error)
		})
		if !supported || !ok || target.RegisterBits() != 64 {
			return fail(ErrBackendLimit)
		}
		if resultReg != pair[0] {
			written[pair[0]] = true
		}
		if highReg != pair[1] {
			written[pair[1]] = true
		}
		output, slots, err = pairEmitter.finishPair(resultReg, highReg, pair[0], pair[1], slots)
	case resultGroup.Words == 2 && s.final:
		// A 386 pair returns in the ABI's two result registers.
		pairEmitter, ok := emitter.(interface {
			finishPair(physReg, physReg, physReg, physReg, int) (physicalProgram, int, error)
		})
		names := physicalRegistersByArchitecture[target.Architecture()]
		if !ok || len(s.entry.result.Words) != 2 {
			return fail(ErrBackendLimit)
		}
		low, lowOK := names[s.entry.result.Words[0].Register]
		high, highOK := names[s.entry.result.Words[1].Register]
		if !lowOK || !highOK {
			return fail(ErrBackendLimit)
		}
		if resultGroup.Registers[0] != low {
			written[low] = true
		}
		if resultGroup.Registers[1] != high {
			written[high] = true
		}
		output, slots, err = pairEmitter.finishPair(resultGroup.Registers[0], resultGroup.Registers[1], low, high, slots)
	case resultGroup.Words == 2 && s.bare:
		output, err = emitter.bodyBare(resultReg, slots)
		output.resultWords = [2]physReg{resultGroup.Registers[0], resultGroup.Registers[1]}
		output.resultWordCount = 2
	case resultGroup.Words == 2:
		return fail(ErrBackendLimit)
	case s.final && len(s.entry.result.Words) == 2:
		narrowEmitter, ok := emitter.(interface {
			finishNarrowPair(physReg, physReg, physReg, int) (physicalProgram, error)
		})
		if !ok || s.entry.result.Words[0].Storage != abi.Register || s.entry.result.Words[1].Storage != abi.Register {
			return fail(ErrBackendLimit)
		}
		names := physicalRegistersByArchitecture[target.Architecture()]
		low, lowOK := names[s.entry.result.Words[0].Register]
		high, highOK := names[s.entry.result.Words[1].Register]
		if !lowOK || !highOK {
			return fail(ErrBackendLimit)
		}
		written[high] = true
		if resultReg != low {
			written[low] = true
		}
		output, err = narrowEmitter.finishNarrowPair(resultReg, low, high, slots)
	case s.final:
		if resultReg != target.ResultRegister() {
			written[target.ResultRegister()] = true
		}
		output, err = emitter.finish(resultReg, slots)
	case s.bare:
		output, err = emitter.bodyBare(resultReg, slots)
	default:
		output, err = emitter.body(resultReg, slots)
	}
	if err != nil {
		return fail(err)
	}
	output.exportSlots = exportSlots
	if p.DirectExports {
		output.exportHomes = make([]exportLocation, len(p.Exports))
		for i, id := range p.Exports {
			r, _ := exportGroups[i].First()
			output.exportHomes[i] = exportLocation{Float: p.Values[id].Class == backend.Float, Bits: p.Values[id].Bits, Register: int(r), Slot: -1}
		}
	}
	if s.bare || len(p.ResultParts) != 0 && !s.final {
		output.resultPhysical = resultReg
	} else if len(s.entry.result.Words) == 1 {
		output.resultPhysical = target.ResultRegister()
	} else {
		low, _ := physicalRegistersByArchitecture[target.Architecture()][s.entry.result.Words[0].Register]
		output.resultPhysical = low
	}
	if output.resultWordCount == 0 {
		output.resultWords[0] = output.resultPhysical
		output.resultWordCount = 1
	}
	if output.stackBytes == 0 {
		// finishRegisters can append ABI preservation and entry-resource slots.
		finalSlots := slots
		for _, located := range output.insns {
			finalSlots = max(finalSlots, referencedSpillSlots([]machine.Instruction{located.ins}, target.RegisterBits()))
		}
		workspace = finalSlots * int(target.RegisterBits()/8)
	}
	return output, workspace, *written, true, nil
}
