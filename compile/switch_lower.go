package compile

import (
	"fmt"
	"slices"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/ir"
)

// The optimized tier lowers a TermSwitch to a compare tree before Var
// promotion and cross-block threading, so that every later pass sees only
// jumps and two-way branches:
//
//  1. Runs of equal consecutive targets coalesce into ranges. Cases the
//     selector's width cannot reach are dropped, and so is the default
//     when every value of the width has a case.
//  2. One unsigned comparison sends selector >= len(Cases) to the default.
//  3. A binary search on the range bounds (selector < k) picks the range;
//     its depth is ⌈log₂ ranges⌉, and a range needs no test of its own.
//
// The switch's segment ends with the first comparison, and each further
// comparison is an empty segment of its own with a synthetic label, laid out
// right after it. Every edge to a destination passes the switch's Args;
// edges between comparisons pass nothing, since the selector and the Args
// dominate them. No instruction is added or moved, so value IDs are the
// source's. Emission, relaxation and hardening (a barrier at every label)
// then treat the comparisons as ordinary branches.
//
// A switch with at least switchTableRanges ranges, at least
// switchTableDensity of its reachable cases starting one, becomes a jump
// table instead, unless the function is hardened: an indirect jump is what
// branch-target injection steers, so hardened code keeps compare trees. On
// 386 a 64-bit selector keeps the tree too. The switch stays a TermSwitch
// whose destinations are landing blocks, one per distinct destination: an
// empty segment that jumps on with the Args. A landing block's only
// predecessor is the switch, so cross-block threading can give every
// landing block the same parameters, which every edge of the switch passes.
// The landing labels follow the cases, the default last (machine.OpJumpTable
// clamps the selector to it). Var promotion does not handle such a switch,
// so a function with one keeps its Vars in homes.

// Jump-table thresholds: ranges, and ranges per reachable case.
const (
	switchTableRanges  = 8
	switchTableDensity = 0.4
)

// switchRange is a run of consecutive cases with one target.
type switchRange struct {
	start uint64 // first selector value of the run
	label int
}

// desugarSwitches returns p with every TermSwitch lowered to a compare tree,
// or with tables to its jump-table form, and reports whether a jump table
// remains. It returns p itself when p has no switch. p is a validated
// program and is not modified. registerBits is the target's register width.
func desugarSwitches(p *ir.Program, tables bool, registerBits uint8) (*ir.Program, bool) {
	if p.Control == nil {
		return p, false
	}
	found := false
	for i := range p.Control.Steps {
		if s := &p.Control.Steps[i]; !s.IsLabel && s.Term == ir.TermSwitch {
			found = true
			break
		}
	}
	if !found {
		return p, false
	}
	source := p.Control
	control := &ir.Control{
		Labels:      append([]bool(nil), source.Labels...),
		LabelScopes: append([]int(nil), source.LabelScopes...),
		Params:      append([]int(nil), source.Params...),
		ParamBits:   append([][]uint8(nil), source.ParamBits...),
		Steps:       make([]ir.ControlStep, 0, len(source.Steps)),
	}
	if len(source.ParamTypes) != 0 {
		control.ParamTypes = append([][]ir.Type(nil), source.ParamTypes...)
	}
	var ranges []switchRange
	table := false
	for _, step := range source.Steps {
		if step.IsLabel || step.Term != ir.TermSwitch {
			control.Steps = append(control.Steps, step)
			continue
		}
		bits := instructionBits(p.Instructions[step.Selector])
		ranges = switchRanges(ranges[:0], step.Cases, bits)
		entries := min(uint64(len(step.Cases)), switchEntries(bits))
		if tables && len(ranges) >= switchTableRanges && float64(len(ranges)) >= switchTableDensity*float64(entries) &&
			(bits <= registerBits) {
			control.Steps = appendSwitchTable(control, control.Steps, step, int(entries))
			table = true
			continue
		}
		control.Steps = appendSwitchTree(control, control.Steps, step, ranges, switchHasDefault(len(step.Cases), bits))
	}
	out := *p
	out.Control = control
	return &out, table
}

// switchRegisterBits is the register width of architecture, which bounds a
// jump table's selector.
func switchRegisterBits(architecture string) uint8 {
	if architecture == "386" {
		return 32
	}
	return 64
}

// switchEntries is the number of values a bits-wide selector can hold, or
// the most a table can have.
func switchEntries(bits uint8) uint64 {
	if bits >= 32 {
		return 1 << 32
	}
	return uint64(1) << bits
}

// appendSwitchTable appends switch step in its jump-table form, with its
// first entries cases, to steps, adding a landing label to control per
// distinct destination.
func appendSwitchTable(control *ir.Control, steps []ir.ControlStep, step ir.ControlStep, entries int) []ir.ControlStep {
	scope := control.LabelScopes[step.To]
	landing := make(map[int]int)
	var order []int // destinations in order of first use
	land := func(to int) int {
		if label, ok := landing[to]; ok {
			return label
		}
		label := len(control.Labels)
		control.Labels = append(control.Labels, true)
		control.LabelScopes = append(control.LabelScopes, scope)
		control.Params = append(control.Params, 0)
		control.ParamBits = append(control.ParamBits, nil)
		if control.ParamTypes != nil {
			control.ParamTypes = append(control.ParamTypes, nil)
		}
		landing[to] = label
		order = append(order, to)
		return label
	}
	head := step
	head.Cases, head.Args = make([]int, entries), nil
	for i := range head.Cases {
		head.Cases[i] = land(step.Cases[i])
	}
	head.To = land(step.To)
	steps = append(steps, head)
	for _, to := range order {
		steps = append(steps, ir.ControlStep{IsLabel: true, Label: landing[to]}, ir.ControlStep{
			Term: ir.TermJump, To: to, Args: step.Args, Other: -1, Result: -1,
			Start: step.End, End: step.End, LeaveStart: step.LeaveEnd, LeaveEnd: step.LeaveEnd,
		})
	}
	return steps
}

// switchRanges coalesces the cases a bits-wide selector can reach into
// ranges, in selector order.
func switchRanges[T int | int32](dst []switchRange, cases []T, bits uint8) []switchRange {
	n := uint64(len(cases))
	if bits < 64 {
		n = min(n, uint64(1)<<bits)
	}
	for i := uint64(0); i < n; i++ {
		if label := int(cases[i]); len(dst) == 0 || dst[len(dst)-1].label != label {
			dst = append(dst, switchRange{start: i, label: label})
		}
	}
	return dst
}

// switchHasDefault reports whether some value of a bits-wide selector is at
// or above cases.
func switchHasDefault(cases int, bits uint8) bool {
	return bits == 64 || uint64(cases) < uint64(1)<<bits
}

// appendSwitchTree appends the compare tree of switch step over ranges to
// steps, adding a label to control per synthetic block.
func appendSwitchTree(control *ir.Control, steps []ir.ControlStep, step ir.ControlStep, ranges []switchRange, hasDefault bool) []ir.ControlStep {
	selector := step.Selector
	scope := control.LabelScopes[step.To]
	type node struct{ lo, hi, label int }
	var work []node // synthetic blocks to lay out, the next on top
	// edge returns the destination of ranges[lo..hi] and its arguments: the
	// range's own label, or a new block that searches them.
	edge := func(lo, hi int) (int, []int) {
		if lo == hi {
			return ranges[lo].label, step.Args
		}
		label := len(control.Labels)
		control.Labels = append(control.Labels, true)
		control.LabelScopes = append(control.LabelScopes, scope)
		control.Params = append(control.Params, 0)
		control.ParamBits = append(control.ParamBits, nil)
		if control.ParamTypes != nil {
			control.ParamTypes = append(control.ParamTypes, nil)
		}
		work = append(work, node{lo, hi, label})
		return label, nil
	}
	// compare ends s with "selector condition k", true to yes and false to no.
	compare := func(s *ir.ControlStep, condition ir.BranchCondition, k uint64, yes int, yesArgs []int, no int, noArgs []int) {
		s.Term = ir.TermBranch
		s.Predicate = &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: condition, Value: selector, Immediate: int64(k)}
		s.Values, s.Result = []int{selector}, selector
		s.To, s.Args, s.Other, s.OtherArgs = yes, yesArgs, no, noArgs
	}
	// search splits ranges[lo..hi] at the middle bound; the upper half,
	// pushed last, is laid out next and so is the fallthrough.
	search := func(s *ir.ControlStep, lo, hi int) {
		mid := (lo + hi + 1) / 2
		yes, yesArgs := edge(lo, mid-1)
		no, noArgs := edge(mid, hi)
		compare(s, ir.BranchBelow, ranges[mid].start, yes, yesArgs, no, noArgs)
	}
	head := step
	head.Selector, head.Cases, head.Result = 0, nil, 0
	switch {
	case hasDefault:
		to, args := edge(0, len(ranges)-1)
		compare(&head, ir.BranchAboveEqual, uint64(len(step.Cases)), step.To, step.Args, to, args)
	case len(ranges) == 1:
		head.Term, head.To = ir.TermJump, ranges[0].label
	default:
		search(&head, 0, len(ranges)-1)
	}
	steps = append(steps, head)
	for len(work) != 0 {
		n := work[len(work)-1]
		work = work[:len(work)-1]
		s := ir.ControlStep{Start: step.End, End: step.End, LeaveStart: step.LeaveEnd, LeaveEnd: step.LeaveEnd}
		search(&s, n.lo, n.hi)
		steps = append(steps, ir.ControlStep{IsLabel: true, Label: n.label}, s)
	}
	return steps
}

// switchSource seals a program from desugarSwitches and returns it with its
// analysis. A program that fails validation is an internal error.
func switchSource(program *ir.Program) (*ir.Program, *programFacts, error) {
	sealed, err := ir.SealOwned(program)
	if err != nil {
		return nil, nil, fmt.Errorf("lowering switches: %w: %w", ErrInvalidFunction, err)
	}
	view := new(ir.Program)
	sealedView(sealed, view)
	return view, sealed.Analysis(), nil
}

// jumpTableReserved lists, per architecture, the registers OpJumpTable cannot
// take as A or B: its own temporaries, and registers that are no index.
var jumpTableReserved = map[string][]physReg{
	"amd64":   {4, 5, 13},
	"386":     {4, 5},
	"arm64":   {16, 17, 31},
	"riscv64": {0, 30, 31},
	"loong64": {0, 19, 20},
	"ppc64le": {0, 1, 2, 11, 12, 13},
}

// emitJumpTable emits a stepTable after its segment: the selector into a
// register, machine.OpJumpTable, and for each destination with edge
// arguments a stub that moves them and jumps on. The segment exported the
// arguments to slots, so every register outside the variable homes is free
// here; the selector is the segment's result, or one of those exports.
func emitJumpTable(stream *physicalEmitter, target machineTarget, step emissionStep, slots map[int]int, result int,
	resultRegister physReg, registers []physReg, scratch physReg, written *registerWrites,
	newLabel func() ir.Label, bind func(ir.Label), edge func(ir.Label, []int) error) error {
	reserved := jumpTableReserved[target.Architecture()]
	usable := func(r physReg) bool { return !backend.IsVectorRegister(r) && !slices.Contains(reserved, r) }
	pick := func(not physReg, avoid bool) (physReg, bool) {
		for _, r := range registers {
			if (!avoid || r != not) && usable(r) {
				return r, true
			}
		}
		return 0, false
	}
	slot, exported := slots[step.selector]
	if !exported && step.selector != result {
		return ErrInvalidFunction
	}
	index := resultRegister
	if exported {
		index = scratch
	}
	if !usable(index) {
		moved, ok := pick(0, false)
		if !ok {
			return fmt.Errorf("jump table needs an index register: %w", ErrBackendLimit)
		}
		if !exported {
			emitHomeMove(stream, 64, moved, index)
		}
		index = moved
	}
	if exported {
		emitHomeReload(stream, index, slot)
	}
	temporary, ok := pick(index, true)
	if !ok {
		return fmt.Errorf("jump table needs a temporary register: %w", ErrBackendLimit)
	}
	written[index], written[temporary] = true, true
	// Destinations with arguments are reached through a stub that moves them.
	stubs := make(map[ir.Label]ir.Label)
	var order []ir.Label
	entry := func(label ir.Label) int {
		if len(step.args) == 0 {
			return int(label)
		}
		stub, ok := stubs[label]
		if !ok {
			stub = newLabel()
			stubs[label] = stub
			order = append(order, label)
		}
		return int(stub)
	}
	table := make([]int, 0, len(step.table)+1)
	for _, label := range step.table {
		table = append(table, entry(label))
	}
	table = append(table, entry(step.label))
	stream.append(physicalInsn{Op: pJumpTable, A: index, B: temporary, Table: table})
	for _, label := range order {
		bind(stubs[label])
		if err := edge(label, step.args); err != nil {
			return err
		}
		stream.append(physicalInsn{Op: pJump, Imm: uint64(label)})
	}
	return nil
}
