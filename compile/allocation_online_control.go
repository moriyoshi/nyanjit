package compile

import (
	"slices"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/regalloc"
	"github.com/moriyoshi/nyanjit/ir"
)

// A control program on the regalloc.Allocator protocol is one Function: a
// block per segment or Emitter fragment, whose parameters are the parameters
// of the label it starts at, and an edge per jump or branch destination
// passing the edge's arguments. Online picks where each label's parameters
// live as it resolves the first edge into it, so the labels' fixed registers
// and slots go unused. A block's last position reads the values its
// predicate compares, or the result it returns; the predicate is emitted
// after the block from the registers Online leaves them in, before the
// edges' moves. What a block needs for that and Online cannot express, such
// as a float's bits in a general register, an immediate the comparison
// cannot encode or an argument in the other register bank, the block adds
// as instructions of its own at its end.

// onlineBlock is what a control program's block adds to its program's
// description, in the program's value numbering: the label's parameters it
// reads, the values its terminator reads and those that leave it.
type onlineBlock struct {
	params   []int
	terminal []int // the predicate's values, or the returned result
	// escaping are the values the edges pass and a returned result: Online
	// must hold them at the end, so none is read in place from a home.
	escaping []int
	exit     bool
}

// onlineStep is one block of a control program's Online function.
type onlineStep struct {
	id    int // the builder step
	setup onlineSetup
	block onlineBlock
	// indices are the label parameters block.params reads, by position.
	indices  []int
	callArea int // the 386 call area its calls need
	// pairs are the registers of the 386 pairs its predicate compares.
	pairs map[int][2]physReg
	// args are what the block's edges pass, to label and to other.
	args [2][]int
	// clamp is the copy of a hardened bounds check's index, or -1, and
	// clampRegister its register at the branch.
	clamp         int
	clampRegister physReg
	// predicate replaces the branch's, or the synthetic one of a fragment's
	// branch, when the branch compares values the block adds.
	predicate *ir.PredicateExpr
	// The emitted body and the registers the predicate reads.
	physical          physicalProgram
	homes             map[int]exportLocation
	values, positions int // the first of the block's in the function
	saved             onlineSaved
}

// onlineSaved holds a block's tables that its walk reads, or the builder
// points into, while later blocks are described.
type onlineSaved struct {
	inputs, inputStart []int
	homeAt             []machine.Home
	snapshot           []int16
	snapFrom           []int32
	calls              []onlineCall
	callHomes          []machine.Home
	callEffects        []abi.CallEffect
	callLocations      []abi.ValueLocation
	callWords          []abi.WordLocation
	callFixed          []physReg
	callNames          []string
	callWrites         []physReg
	kinds386           []uint8
	transitions        []onlineTransition
	transitionAt       []int32
}

// takeOnline returns the tables the last description filled, and leaves the
// rest of their storage for the next.
func (t *allocationTemps) takeOnline() onlineSaved {
	return onlineSaved{
		inputs: takeTail(&t.onlineInputs), inputStart: takeTail(&t.onlineInputStart),
		homeAt: takeTail(&t.onlineHomeAt), snapshot: takeTail(&t.onlineSnapshot), snapFrom: takeTail(&t.onlineSnapFrom),
		calls: takeTail(&t.onlineCalls), callHomes: takeTail(&t.onlineCallHomes), callEffects: takeTail(&t.onlineCallEffects),
		callLocations: takeTail(&t.onlineCallLocations), callWords: takeTail(&t.onlineCallWords), callFixed: takeTail(&t.onlineCallFixed),
		callNames: takeTail(&t.onlineCallNames), callWrites: takeTail(&t.onlineCallWrites), kinds386: takeTail(&t.online386),
		transitions: takeTail(&t.onlineTransitions), transitionAt: takeTail(&t.onlineTransitionAt),
	}
}

// restoreOnline puts a block's tables back for its walk.
func (t *allocationTemps) restoreOnline(x onlineSaved) {
	t.onlineInputs, t.onlineInputStart = x.inputs, x.inputStart
	t.onlineHomeAt, t.onlineSnapshot, t.onlineSnapFrom = x.homeAt, x.snapshot, x.snapFrom
	t.onlineCalls, t.onlineCallHomes, t.onlineCallEffects = x.calls, x.callHomes, x.callEffects
	t.onlineCallLocations, t.onlineCallWords, t.onlineCallFixed = x.callLocations, x.callWords, x.callFixed
	t.onlineCallNames, t.onlineCallWrites, t.online386 = x.callNames, x.callWrites, x.kinds386
	t.onlineTransitions, t.onlineTransitionAt = x.transitions, x.transitionAt
}

// regrowOnline returns the storage the function started with, grown to what
// all its blocks took, so that the next function of this size reuses it.
func (t *allocationTemps) regrowOnline(base onlineSaved, steps []onlineStep) {
	var total onlineSaved
	var n [16]int
	for i := range steps {
		x := &steps[i].saved
		for k, l := range [...]int{len(x.inputs), len(x.inputStart), len(x.homeAt), len(x.snapshot), len(x.snapFrom), len(x.calls), len(x.callHomes),
			len(x.callEffects), len(x.callLocations), len(x.callWords), len(x.callFixed), len(x.callNames), len(x.callWrites), len(x.kinds386), len(x.transitions), len(x.transitionAt)} {
			n[k] += l
		}
	}
	total.inputs, total.inputStart = regrow(base.inputs, n[0]), regrow(base.inputStart, n[1])
	total.homeAt, total.snapshot, total.snapFrom = regrow(base.homeAt, n[2]), regrow(base.snapshot, n[3]), regrow(base.snapFrom, n[4])
	total.calls, total.callHomes = regrow(base.calls, n[5]), regrow(base.callHomes, n[6])
	total.callEffects, total.callLocations, total.callWords = regrow(base.callEffects, n[7]), regrow(base.callLocations, n[8]), regrow(base.callWords, n[9])
	total.callFixed, total.callNames, total.callWrites = regrow(base.callFixed, n[10]), regrow(base.callNames, n[11]), regrow(base.callWrites, n[12])
	total.kinds386 = regrow(base.kinds386, n[13])
	total.transitions, total.transitionAt = regrow(base.transitions, n[14]), regrow(base.transitionAt, n[15])
	t.restoreOnline(total)
}

// onlineStorage returns the tables' storage, emptied.
func (t *allocationTemps) onlineStorage() onlineSaved {
	return onlineSaved{
		inputs: t.onlineInputs[:0], inputStart: t.onlineInputStart[:0],
		homeAt: t.onlineHomeAt[:0], snapshot: t.onlineSnapshot[:0], snapFrom: t.onlineSnapFrom[:0],
		calls: t.onlineCalls[:0], callHomes: t.onlineCallHomes[:0], callEffects: t.onlineCallEffects[:0],
		callLocations: t.onlineCallLocations[:0], callWords: t.onlineCallWords[:0], callFixed: t.onlineCallFixed[:0],
		callNames: t.onlineCallNames[:0], callWrites: t.onlineCallWrites[:0], kinds386: t.online386[:0],
		transitions: t.onlineTransitions[:0], transitionAt: t.onlineTransitionAt[:0],
	}
}

// takeTail returns *s, which cannot grow into what follows, and leaves *s
// empty with the rest of its capacity.
func takeTail[T any](s *[]T) []T {
	x := (*s)[:len(*s):len(*s)]
	*s = (*s)[len(*s):]
	return x
}

// regrow returns base emptied, or new storage when it holds fewer than n.
func regrow[T any](base []T, n int) []T {
	if cap(base) < n {
		return make([]T, 0, n)
	}
	return base[:0]
}

// onlineCandidate reports whether allocateOnlineControl can take e before
// anything is selected: no segment reads a label parameter twice, and every
// predicate is well formed.
func onlineCandidate(e *Emitter) bool {
	for _, step := range e.steps {
		switch step.kind {
		case stepBuilder:
			if step.segment != nil && step.segment.repeated {
				return false
			}
		case stepBranch:
			for _, err := range predicateComparisons(step.predicate) {
				if err != nil {
					return false
				}
			}
		}
	}
	return true
}

// onlineTerminalLimit is the most values a block's terminator reads when
// fewer registers are free.
const onlineTerminalLimit = 4

// onlineCompactPins asks for RCX, where amd64 compact loops keep a loop's
// counter for LOOP, for the arguments x's edges pass to a label's pinned
// parameter. Online starts a parameter where the first edge into it leaves
// its argument, so an argument without a definition here to prefer RCX,
// such as a constant, is first moved to a value that does.
func onlineCompactPins(e *Emitter, x *onlineStep, next emissionStep, t *allocationTemps) {
	p := &x.setup.program
	for side, label := range [2]ir.Label{next.label, next.other} {
		if side == 1 && next.kind == stepJump {
			break
		}
		pin := e.compactPins[label]
		if pin < 0 || pin >= len(x.args[side]) {
			continue
		}
		v := x.args[side][pin]
		defined := !p.Values[v].Constant && slices.ContainsFunc(p.Instructions, func(ins machineInstruction) bool { return ins.Dst == v })
		if !defined {
			w := len(p.Values)
			value := p.Values[v]
			value.Constant, value.Symbol, value.Param, value.EntryReg, value.EntrySlot = false, "", -1, -1, -1
			p.Values = append(p.Values, value)
			p.Instructions = append(p.Instructions, machineInstruction{Op: mMove, Dst: w, A: v})
			args := carveCopy(&t.onlineControlInts, x.args[side])
			args[pin] = w
			x.args[side] = args
			v = w
		}
		if p.PreferredRegisters == nil {
			p.PreferredRegisters = make(map[int]backend.Register, 1)
		}
		p.PreferredRegisters[v] = 1
	}
}

// onlineImmediateFits reports whether a comparison of a register with
// node's immediate encodes, as the predicate's register-home emission
// requires.
func onlineImmediateFits(node *ir.PredicateExpr, target machineTarget) bool {
	if node.WithValue || node.Immediate >= -4095 && node.Immediate <= 4095 {
		return true
	}
	capability, ok := target.(interface {
		CanCompareImmediate(uint8, machine.ComparisonMode, uint64) bool
	})
	return ok && capability.CanCompareImmediate(target.RegisterBits(), machine.ComparisonFor(node.Condition), uint64(node.Immediate))
}

// prepareOnline applies to a control block's program what
// allocateAndEmitMode applies before it allocates, and reports whether
// Online can take the result; otherwise the direct walk reports what it
// rejects.
func prepareOnline(program machineProgram, target machineTarget, emitter machineEmitter, t *allocationTemps) (machineProgram, bool) {
	if emitter.(*physicalEmitter).harden {
		var err error
		if program, err = hardenSelected(program, target); err != nil {
			return program, false
		}
	}
	if emitter.(*physicalEmitter).stack != abi.StackPlatform {
		for _, ins := range program.Instructions {
			if isCall(ins.Op) {
				return program, false
			}
		}
	}
	for id, v := range program.Values {
		if v.EntryReg >= 0 && backend.IsVectorRegister(physReg(v.EntryReg)) {
			program.Values[id].Class = backend.Float
		}
	}
	markFloatingHomeGets(&program)
	if len(floatRegisters(target.Architecture())) != 0 {
		program = assignFloatResidency(program, target.RegisterBits())
	}
	program = foldFloatLiterals(program, t)
	if validator, ok := target.(interface{ validateProgram(machineProgram) error }); ok {
		if err := validator.validateProgram(program); err != nil {
			return program, false
		}
	}
	if program.HomePlan != nil {
		program.ReservedSlots = max(program.ReservedSlots, program.HomePlan.ReservedSlots)
	}
	if !onlineEligible(program, target) {
	}
	return program, onlineEligible(program, target)
}

// onlineControl is what allocateOnlineControl leaves for the emission of
// the jumps and branches, by step: the moves of each edge, to label and to
// other, and the predicate to emit when it replaces the step's.
type onlineControl struct {
	edges      [][2][]physicalInsn
	predicates []*ir.PredicateExpr
	// clamps are the registers hardened bounds checks clamp their index
	// in, or -1; nil for none.
	clamps  []int
	slots   int // the frame slots the moves use
	written registerWrites
}

// onlineRecord receives a builder step's program, body and, for a branch,
// the registers its predicate's values are in.
type onlineRecord func(id int, program *machineProgram, physical physicalProgram, homes map[int]exportLocation, pairs map[int][2]physReg)

// allocateOnlineControl allocates and emits e's segments as one Online
// function, calling record with each block's body as soon as it is emitted.
// It returns, by jump or branch step, the moves of its edges to label and to
// other, the frame slots they use and the registers written. ok is false when
// Online cannot take the program, before anything is recorded; the caller
// then lowers each segment by itself.
func allocateOnlineControl(e *Emitter, options compileOptions, kind emissionKind, target machineTarget, lease func() machineEmitter,
	t *allocationTemps, record onlineRecord) (control onlineControl, ok bool, err error) {
	if !onlineCandidate(e) {
		return onlineControl{}, false, nil
	}
	// The blocks, in layout order, and the block each label starts.
	blockOf := make([]int, len(e.steps))
	labelBlock := make([]int, len(e.labels))
	for i := range labelBlock {
		labelBlock[i] = -1
	}
	blocks := 0
	var pending []ir.Label
	for id, step := range e.steps {
		blockOf[id] = -1
		switch {
		case step.kind == stepLabel:
			pending = append(pending, step.label)
		case step.kind == stepBuilder && (step.segment == nil || !step.segment.skip):
			blockOf[id] = blocks
			for _, label := range pending {
				labelBlock[label] = blocks
			}
			blocks++
			pending = pending[:0]
		case step.kind == stepBuilder:
			pending = pending[:0]
		}
	}
	// One block has no edges: the per-segment path allocates it the same
	// way, without copying its program out.
	if blocks <= 1 {
		return onlineControl{}, false, nil
	}
	if e.scratch != nil {
		e.scratch.machine.Rewind(1)
		e.scratch.selection.rewind()
	}
	// The edges' moves collect in one emitter, which also answers what
	// planning asks of an emitter.
	edgeEmitter := lease()
	// Every block's fields are set below.
	if cap(t.onlineSteps) < blocks {
		t.onlineSteps = make([]onlineStep, blocks)
	}
	steps := t.onlineSteps[:blocks]
	t.onlineControlInts = t.onlineControlInts[:0]
	t.onlineInstructions, t.onlineValues, t.onlineScopes = t.onlineInstructions[:0], t.onlineValues[:0], t.onlineScopes[:0]
	hasCall := false
	for id, step := range e.steps {
		if blockOf[id] < 0 {
			continue
		}
		if e.scratch != nil {
			e.scratch.selection.rewind() // the program is copied out below
		}
		// A segment's program, as lowerPass selects it, or an Emitter
		// fragment's.
		var program machineProgram
		var err error
		var ops []instruction
		var params []int
		if s := step.segment; s == nil {
			f := step.fragment
			program, err = selectProgramResultParts(f.Instructions, f.ScopeExits, f.Arity, f.Result, f.ResultParts, target, e.scratch)
		} else if len(s.resultParts) != 0 {
			program, err = selectProgramResultParts(s.ops, s.leaves, s.arity, s.result, s.resultParts, target, e.scratch)
			ops, params = s.ops, s.params
		} else {
			program, err = selectProgramExports(s.ops, s.leaves, s.arity, s.result, s.exports, target, e.scratch)
			ops, params = s.ops, s.params
		}
		if err != nil {
			return onlineControl{}, false, nil
		}
		if s := step.segment; s != nil {
			bindVariableHomes(&program, e.source, s.start, e.homePlan, e.facts)
		}
		if bridgeManagedCCalls(&program, options, kind) != nil {
			return onlineControl{}, false, nil
		}
		program.ReservedSlots = e.homeSlots
		calls := false
		for _, ins := range program.Instructions {
			if ins.Op == mCallGo && !options.ManagedCalls && options.ABI != abi.Go {
				return onlineControl{}, false, nil
			}
			calls = calls || isCall(ins.Op)
			hasCall = hasCall || ins.Op == mCallC || ins.Op == mCallGo
		}
		var next emissionStep
		if id+1 < len(e.steps) {
			next = e.steps[id+1]
		}
		if target.Architecture() == "amd64" && next.kind == stepBranch && next.predicate != nil {
			// As the direct walk sets it, but a value an edge passes goes
			// where Online puts the parameter.
			if value, ok := amd64ZeroBranchValue(next.predicate, ops); ok && !calls && !slices.Contains(next.args, value) && !slices.Contains(next.otherArgs, value) {
				program.PreferredRegisters = map[int]backend.Register{value: 1}
			}
		}
		program, ok := prepareOnline(program, target, edgeEmitter, t)
		// The walk reads the program after later segments are selected.
		program.Instructions = carveCopy(&t.onlineInstructions, program.Instructions)
		for i := range program.Instructions {
			if args := program.Instructions[i].Args; len(args) != 0 {
				program.Instructions[i].Args = carveCopy(&t.onlineControlInts, args)
			}
		}
		program.Values = carveCopy(&t.onlineValues, program.Values)
		program.Scopes = carveCopy(&t.onlineScopes, program.Scopes)
		if !ok {
			return onlineControl{}, false, nil
		}
		x := &steps[blockOf[id]]
		*x = onlineStep{id: id, setup: onlineSetup{program: program}, clamp: -1}
		if target.Architecture() == "386" {
			// The 386 call area, as allocateAndEmitMode sizes it.
			for _, ins := range program.Instructions {
				if ins.Op != mCallC && ins.Op != mCallGo {
					continue
				}
				bytes := 0
				for _, words := range selectedArgumentWordsInto(program, ins.Args[1:], nil) {
					bytes += 4 * words
				}
				if ins.Op == mCallGo {
					bytes += 8
				}
				x.callArea = max(x.callArea, bytes)
			}
		}
		// The block's lists share one arena, carved to their largest.
		x.block.params, x.indices = carve(&t.onlineControlInts, len(params)), carve(&t.onlineControlInts, len(params))
		x.block.escaping = carve(&t.onlineControlInts, len(next.args)+len(next.otherArgs)+1)
		// A fragment's branch compares its result with an immediate.
		predicate := next.predicate
		if next.kind == stepBranch && predicate == nil {
			predicate = &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: next.condition, Value: program.Result, Immediate: next.comparison}
			x.predicate = predicate
		}
		if next.kind == stepBranch {
			n := 0
			for range predicateComparisons(predicate) {
				n += 2
			}
			x.block.terminal = carve(&t.onlineControlInts, n+1)
		}
		for index, op := range params {
			if op >= 0 {
				x.block.params = append(x.block.params, op)
				x.indices = append(x.indices, index)
			}
		}
		x.args = [2][]int{next.args, next.otherArgs}
		// A hardened bounds check clamps the index it passes on its success
		// edge in place, after the branch: the edge passes a copy, which
		// stays in a register with no slot copy, so that the edge moves the
		// clamped register.
		if options.Harden && next.kind == stepBranch && next.predicate != nil && next.predicate.Kind == ir.PredicateCompare &&
			next.predicate.Condition == ir.BranchBelow && len(next.args) >= 2 && next.args[1] == next.predicate.Value {
			p := &x.setup.program
			index := next.args[1]
			if v := p.Values[index]; v.Class != backend.General || v.Words > 1 {
				return onlineControl{}, false, nil
			}
			w := len(p.Values)
			value := p.Values[index]
			value.Constant, value.Symbol, value.Param, value.EntryReg, value.EntrySlot = false, "", -1, -1, -1
			p.Values = append(p.Values, value)
			p.Instructions = append(p.Instructions, machineInstruction{Op: mMove, Dst: w, A: index})
			args := carveCopy(&t.onlineControlInts, x.args[0])
			args[1] = w
			x.args[0], x.clamp = args, w
			program = *p
		}
		if e.compactPins != nil && (next.kind == stepJump || next.kind == stepBranch) {
			onlineCompactPins(e, x, next, t)
			program = x.setup.program
		}
		switch next.kind {
		case stepExit:
			x.block.exit = true
			x.block.terminal = append(x.block.escaping, program.Result)
			if len(program.ResultParts) == 2 {
				// A wide result returns in the ABI's pair, which the exit
				// moves its words to.
				x.block.terminal = append(carve(&t.onlineControlInts, 2), program.Result, program.ResultParts[1])
				if pair, ok := target.Integer128Result(); ok {
					p := &x.setup.program
					if p.PreferredRegisters == nil {
						p.PreferredRegisters = make(map[int]backend.Register, 1)
					}
					p.PreferredRegisters[p.ResultParts[1]] = pair[1]
					program = *p
				}
			}
			x.block.escaping = x.block.terminal
		case stepBranch:
			// An immediate the comparison cannot encode becomes a constant
			// the branch reads in a register, in a copy of the predicate.
			// The branch compares general registers: an immediate the
			// comparison cannot encode becomes a constant, and a float's
			// bits are copied to a general value, in a copy of the
			// predicate.
			// On 386 a 64-bit value is a pair whose words are compared
			// with the immediate's halves.
			floating := func(id int) bool {
				return target.RegisterBits() == 64 && id >= 0 && id < len(program.Values) && program.Values[id].Class == backend.Float && program.Values[id].Bits == 64
			}
			paired := func(id int) bool {
				return target.RegisterBits() == 32 && id >= 0 && id < len(program.Values) && program.Values[id].Class == backend.General && program.Values[id].Words == 2
			}
			fits := func(node *ir.PredicateExpr) bool { return paired(node.Value) || onlineImmediateFits(node, target) }
			copied := false
			for node := range predicateComparisons(predicate) {
				if !fits(node) || floating(node.Value) || node.WithValue && floating(node.Other) {
					if x.predicate == nil {
						predicate = ir.RebasePredicate(predicate, 0)
					}
					copied = true
					break
				}
			}
			if copied {
				x.predicate = predicate
				p := &x.setup.program
				bits := make(map[int]int) // a float's general copy
				general := func(id int) int {
					if !floating(id) {
						return id
					}
					if c, ok := bits[id]; ok {
						return c
					}
					c := len(p.Values)
					p.Values = append(p.Values, machine.SelectedValue{Class: backend.General, Bits: 64, Words: 1, Param: -1, EntrySlot: -1, EntryReg: -1})
					p.Instructions = append(p.Instructions, machineInstruction{Op: machine.SelectedFloatMove, Bits: 64, Dst: c, A: id})
					bits[id] = c
					return c
				}
				for node := range predicateComparisons(predicate) {
					node.Value = general(node.Value)
					if node.WithValue {
						node.Other = general(node.Other)
					}
					if !fits(node) {
						// A register-wide constant, as the direct walk's
						// comparison materializes it.
						id, imm := len(p.Values), uint64(node.Immediate)
						if target.RegisterBits() == 32 {
							imm = uint64(uint32(imm))
						}
						p.Values = append(p.Values, machine.SelectedValue{Bits: target.RegisterBits(), Words: 1, Constant: true, Imm: imm, Param: -1, EntrySlot: -1, EntryReg: -1})
						p.Instructions = append(p.Instructions, machineInstruction{Op: mConst, Dst: id, Imm: imm})
						node.WithValue, node.Other, node.Immediate = true, id, 0
					}
				}
				program = *p
				x.block.terminal = carve(&t.onlineControlInts, 2*cap(x.block.terminal))
			}
			for node := range predicateComparisons(predicate) {
				x.block.terminal = append(x.block.terminal, node.Value)
				if node.WithValue {
					x.block.terminal = append(x.block.terminal, node.Other)
				}
			}
			if x.clamp >= 0 {
				x.block.terminal = append(x.block.terminal, x.clamp)
			}
			x.block.escaping = append(append(x.block.escaping, x.args[0]...), x.args[1]...)
		case stepJump:
			x.block.escaping = append(x.block.escaping, x.args[0]...)
		}
		for _, id := range x.block.terminal {
			if id < 0 || id >= len(program.Values) {
				return onlineControl{}, false, nil
			}
			pair := target.RegisterBits() == 32 && program.Values[id].Class == backend.General && program.Values[id].Words == 2
			if v := &program.Values[id]; !x.block.exit && (v.Class == backend.Float || v.Class == backend.Vector || v.Words > 1 && !pair) {
				return onlineControl{}, false, nil
			}
		}
		x.block.terminal = slices.Compact(x.block.terminal)
	}
	// An argument in the other register bank than its parameter is moved to
	// the parameter's bank at the end of the source, as lowerPass's edges
	// move it.
	for id, step := range e.steps {
		if step.kind != stepJump && step.kind != stepBranch || id == 0 || blockOf[id-1] < 0 {
			continue
		}
		from := &steps[blockOf[id-1]]
		changed := false
		for side, label := range [2]ir.Label{step.label, step.other} {
			if side == 1 && step.kind == stepJump || labelBlock[label] < 0 {
				continue
			}
			to := &steps[labelBlock[label]]
			for j, index := range to.indices {
				if index >= len(from.args[side]) {
					continue
				}
				param := &to.setup.program.Values[to.block.params[j]]
				p := &from.setup.program
				arg := from.args[side][index]
				vector := func(v *machine.SelectedValue) bool { return v.Class == backend.Float || v.Class == backend.Vector }
				if vector(param) == vector(&p.Values[arg]) || param.Class == backend.Vector || p.Values[arg].Class == backend.Vector {
					continue
				}
				bits := param.Bits
				if bits == 0 {
					bits = 64
				}
				c := len(p.Values)
				p.Values = append(p.Values, machine.SelectedValue{Class: param.Class, Bits: bits, Words: 1, Param: -1, EntrySlot: -1, EntryReg: -1})
				if v := p.Values[arg]; v.Constant && v.Symbol == "" && param.Class == backend.Float {
					p.Values[c].Constant, p.Values[c].Imm = true, v.Imm
					p.Instructions = append(p.Instructions, machineInstruction{Op: mConst, Bits: bits, Dst: c, Imm: v.Imm})
				} else {
					p.Instructions = append(p.Instructions, machineInstruction{Op: machine.SelectedFloatMove, Bits: bits, Dst: c, A: arg})
				}
				args := carveCopy(&t.onlineControlInts, from.args[side])
				args[index] = c
				from.args[side], changed = args, true
			}
		}
		if changed && !from.block.exit {
			from.block.escaping = append(carve(&t.onlineControlInts, len(from.args[0])+len(from.args[1])), from.args[0]...)
			from.block.escaping = append(from.block.escaping, from.args[1]...)
		}
	}
	// One configuration for every block, as allocateAndEmitMode derives it.
	planner := edgeEmitter.(*physicalEmitter)
	first := &steps[0].setup.program
	registers := append(t.registers[:0], allocationRegisters(target, first.HomePlan, planner.stack)...)
	defer func() { t.registers = registers }()
	preservedStart := len(registers)
	if !planner.withholdPreserved {
		for _, r := range preservedAllocatable(target) {
			if !slices.Contains(registers, r) {
				registers = append(registers, r)
			}
		}
	}
	preservedEnd := len(registers)
	needsFPBank := first.HomePlan.NeedsFloating()
	for i := range steps {
		for _, v := range steps[i].setup.program.Values {
			needsFPBank = needsFPBank || v.Class == backend.Vector || v.Class == backend.Float
		}
	}
	if needsFPBank {
		registers = append(registers, floatRegisters(target.Architecture())...)
	}
	// A branch reads its values in registers at once, two left over for
	// the comparison; the direct walk reloads a longer chain's one at a
	// time.
	terminals := 0
	for _, r := range registers {
		if !backend.IsVectorRegister(r) && (first.HomePlan == nil || !first.HomePlan.Reserved[r]) {
			terminals++
		}
	}
	for i := range steps {
		if n := len(steps[i].block.terminal); n > max(onlineTerminalLimit, terminals-2) {
			return onlineControl{}, false, nil
		}
	}
	if len(registers) > 64 {
		return onlineControl{}, false, nil
	}
	entryArity := 0
	for _, v := range first.Values {
		entryArity = max(entryArity, v.Param+1)
	}
	entryWords := resetBufferTracked(&t.entryWords, &t.history.entryWords, entryArity)
	for _, v := range first.Values {
		if v.Param >= 0 {
			entryWords[v.Param] = max(1, int(v.Words))
		}
	}
	entry, err := cachedEntryLayout(target, entryWords)
	if err != nil {
		return onlineControl{}, false, nil
	}
	reserved := first.ReservedSlots
	slotLimit := planner.spillSlotLimit()
	vectorParity := 0
	switch {
	case target.Architecture() == "arm64" && planner.stack == abi.StackPlatform:
		vectorParity = 1
		if planner.vectorHomes%2 != 0 {
			vectorParity = 0
		}
	case target.Architecture() == "amd64" && planner.stack == abi.StackPlatform:
		vectorParity = (planner.vectorHomes + 1) % 2
		if hasCall {
			vectorParity = planner.vectorHomes % 2
		}
	}
	mover := &t.mover
	mover.written = registerWrites{}
	config := regalloc.Config{
		Registers:     target.Registers().Derive(registers...),
		ReservedSlots: reserved, SlotLimit: slotLimit, VectorSlotParity: vectorParity,
		LimitError: mover.limitError(reserved, slotLimit),
		Preserved:  savedOnFirstUse(target, planner, registers[preservedStart:preservedEnd]),
	}
	// The Mover reads values in the function's numbering.
	var values []machine.SelectedValue
	b, a := &t.function, &t.online
	b.Reset()
	base := t.onlineStorage() // what the blocks share
	t.restoreOnline(base)
	restore := func() { t.regrowOnline(base, steps) }
	var arguments []abi.ValueLocation
	returns := false
	for k := range steps {
		x := &steps[k]
		x.setup = onlineSetup{program: x.setup.program, target: target, emitter: planner, bare: !x.block.exit, temps: t,
			config: config, entry: entry, written: &mover.written, block: &x.block}
		s := &x.setup
		t.online386 = t.online386[:0]
		if target.Architecture() == "386" && !plan386(s) {
			restore()
			return onlineControl{}, false, nil
		}
		if !planOnlineCalls(s) {
			restore()
			return onlineControl{}, false, nil
		}
		x.values, x.positions = b.Values(), b.Positions()
		// planOnlineHomes looks the escaping values up by value.
		escaping := resetBuffer(&t.onlineEscaping, len(x.setup.program.Values))
		for _, id := range x.block.escaping {
			escaping[id] = true
		}
		ret := describeOnline(s, onlineBuilder{b: b, values: x.values, positions: x.positions})
		// An exit that cannot return through the layout moves a general
		// result to the result register at its end, as body does, or
		// leaves a pair to the exit's moves.
		if x.block.exit && !ret && x.setup.program.Values[x.setup.program.Result].Class != backend.General {
			restore()
			return onlineControl{}, false, nil
		}
		returns = returns || ret
		if k == 0 {
			arguments = slices.Clone(t.onlineArguments)
		}
		x.saved = t.takeOnline()
		values = append(values, x.setup.program.Values...)
		for len(values) < b.Values() {
			values = append(values, machine.SelectedValue{Class: backend.General, Words: 1, Param: -1, EntrySlot: -1, EntryReg: -1})
		}
	}
	layout := abi.EntryLayout{Arguments: arguments}
	if returns {
		layout.Result = entry.result
	}
	b.SetLayout(layout)
	// Each jump or branch passes its arguments to the parameters the
	// destination reads.
	edgeAt := make([][2]int, len(e.steps))
	var argBuffer []int
	for id, step := range e.steps {
		edgeAt[id] = [2]int{-1, -1}
		if step.kind != stepJump && step.kind != stepBranch || id == 0 || blockOf[id-1] < 0 {
			continue
		}
		from := &steps[blockOf[id-1]]
		for side, edge := range [2]struct {
			label ir.Label
			args  []int
		}{{step.label, from.args[0]}, {step.other, from.args[1]}} {
			if side == 1 && step.kind == stepJump {
				break
			}
			to := labelBlock[edge.label]
			if to < 0 {
				if len(edge.args) != 0 {
					restore()
					return onlineControl{}, false, nil
				}
				continue
			}
			argBuffer = argBuffer[:0]
			for _, index := range steps[to].indices {
				if index >= len(edge.args) {
					restore()
					return onlineControl{}, false, nil
				}
				argBuffer = append(argBuffer, from.values+edge.args[index])
			}
			edgeAt[id][side] = b.AddEdge(blockOf[id-1], to, argBuffer...)
		}
	}
	inputs := 0
	for i := range steps {
		inputs += len(steps[i].saved.inputs)
	}
	t.observeOnline(b, inputs)
	mover.values, mover.registerBits = values, target.RegisterBits()
	defer mover.release()
	if err := a.Allocate(config, b, mover); err != nil {
		restore()
		return onlineControl{}, false, nil
	}
	defer a.Finish()
	defer restore()
	// A walk that fails leaves the program to the direct walk, which
	// reports what it rejects: nothing is recorded until every block is
	// emitted.
	if a.Entry() != nil {
		return onlineControl{}, false, nil
	}
	t.onlineBodies = t.onlineBodies[:0]
	edges := make([][2][]physicalInsn, len(e.steps))
	slots := 0
	var predicates []*ir.PredicateExpr
	var clamps []int
	var edgeArena []physicalInsn
	for k := range steps {
		x := &steps[k]
		s, p := &x.setup, &x.setup.program
		if e.scratch != nil {
			e.scratch.machine.Rewind(2) // the root and the edges' streams stay
		}
		fragment := lease()
		fragment.(*physicalEmitter).machineStream().Reserve(len(p.Instructions) + 8)
		fragment.(*physicalEmitter).callAreaBytes = max(fragment.(*physicalEmitter).callAreaBytes, x.callArea)
		t.online386Patches = t.online386Patches[:0]
		s.emitter, mover.emitter = fragment, fragment
		t.restoreOnline(x.saved)
		w := onlineWalk{a: a, positions: x.positions}
		var homes onlineHomePlan
		if p.HomePlan != nil {
			t.beginHomes(2 * len(p.HomePlan.Slots))
			homes.snapshot, homes.from = t.onlineSnapshot, t.onlineSnapFrom
		}
		fail := func(err error) (onlineControl, bool, error) {
			return onlineControl{}, false, nil
		}
		if err := walkOnline(s, w, &homes); err != nil {
			return fail(err)
		}
		last := len(p.Instructions)
		if err := w.Before(last); err != nil {
			return fail(err)
		}
		var predicate map[int]exportLocation
		if !x.block.exit && len(x.block.terminal) != 0 {
			predicate = make(map[int]exportLocation, len(x.block.terminal))
		}
		result := target.ResultRegister()
		var words [2]physReg // a wide result's
		at := 0
		for i, id := range x.block.terminal {
			var g regalloc.Group
			if home := homes.inPlace(id, last); home >= 0 {
				g = regalloc.SingleRegister(physReg(home))
			} else {
				g = w.Operand(last, at)
				at++
			}
			r := g.Registers[0]
			if x.block.exit {
				words[i] = r
				if g.Words == 2 {
					words = g.Registers // a 386 pair
				}
				result = words[0]
				continue
			}
			if g.Words == 2 {
				// A 386 pair, compared word by word.
				if x.pairs == nil {
					x.pairs = make(map[int][2]physReg, 1)
				}
				x.pairs[id] = g.Registers
			}
			if id == x.clamp {
				x.clampRegister = r
			}
			predicate[id] = exportLocation{Bits: p.Values[id].Bits, Register: int(r), Slot: -1}
		}
		if err := w.After(last); err != nil {
			return fail(err)
		}
		if len(t.online386Patches) != 0 {
			// No value of another block is in a slot during this one's
			// expansions: their temporary takes the next slot.
			patch386(s, a.Slots())
		}
		emitter := fragment.(*physicalEmitter)
		used := referencedSpillSlots(emitter.machineStream().Instructions(), target.RegisterBits())
		var physical physicalProgram
		wide := x.block.exit && (len(p.ResultParts) == 2 || target.RegisterBits() == 32 && p.Values[p.Result].Words == 2)
		if x.block.exit && !wide {
			physical, err = emitter.body(result, used)
		} else {
			physical, err = emitter.bodyBare(target.ResultRegister(), used)
		}
		if err != nil {
			return fail(err)
		}
		physical.flags = registerImageFlags(physical.flags, *p, target.RegisterBits())
		physical.resultPhysical = target.ResultRegister()
		physical.resultWords[0], physical.resultWordCount = physical.resultPhysical, 1
		if wide {
			physical.resultWords, physical.resultWordCount = words, 2
		}
		// The body shares the fragments' buffer: keep a copy.
		physical.insns = carveCopy(&t.onlineBodies, physical.insns)
		x.physical, x.homes = physical, predicate
		if x.predicate != nil {
			if predicates == nil {
				predicates = make([]*ir.PredicateExpr, len(e.steps))
			}
			predicates[x.id+1] = x.predicate
		}
		if x.clamp >= 0 {
			if clamps == nil {
				clamps = make([]int, len(e.steps))
				for i := range clamps {
					clamps[i] = -1
				}
			}
			clamps[x.id+1] = int(x.clampRegister)
		}
		// The block's edges, from where it leaves its values.
		mover.emitter = edgeEmitter
		stream := edgeEmitter.(*physicalEmitter).machineStream()
		if term := x.id + 1; term < len(e.steps) {
			for side, edge := range edgeAt[term] {
				if edge < 0 {
					continue
				}
				n := len(stream.Instructions())
				if err := a.Edge(edge); err != nil {
					return fail(err)
				}
				edges[term][side] = carveCopy(&edgeArena, stream.Instructions()[n:])
				slots = max(slots, referencedSpillSlots(edges[term][side], target.RegisterBits()))
			}
		}
	}
	for k := range steps {
		x := &steps[k]
		record(x.id, &x.setup.program, x.physical, x.homes, x.pairs)
	}
	return onlineControl{edges: edges, predicates: predicates, clamps: clamps, slots: slots, written: mover.written}, true, nil
}
