package ir

import (
	"errors"
	"fmt"
)

// Block identifies a block of a ProgramBuilder.
type Block int

// ProgramBuilder appends IR for front ends that emit it directly, in SSA
// style. Blocks take parameters, which incoming edges supply, and a value is
// usable in every block its definition dominates. Instructions can be
// appended to any unterminated block in any order.
//
// Every appended instruction gets an ID, which its operands, edge arguments
// and predicates use. IDs are the builder's own: Seal lays the reachable
// blocks out in reverse postorder and renumbers the instructions, so the
// SealedProgram's indices differ. A *ValidationError from Seal carries the
// builder's ID in Index.
//
// Methods check their arguments as they go. The first error is kept and
// returned by Seal; methods called after it do nothing and return -1 where
// they return an ID. A ProgramBuilder is not safe for concurrent use.
type ProgramBuilder struct {
	blocks []builderBlock
	owner  []instructionRef // the block and position of each ID
	err    error
	sealed bool
}

type instructionRef struct{ block, index int }

type builderBlock struct {
	params []int // IDs of the block's parameters
	code   []Instruction
	ids    []int // ID of each instruction in code
	term   ControlTerm
	done   bool
	to     [2]Block
	args   [2][]int
	pred   *PredicateExpr
	result []int
}

// NewProgramBuilder returns a builder holding an empty entry block.
func NewProgramBuilder() *ProgramBuilder {
	return &ProgramBuilder{blocks: make([]builderBlock, 1)}
}

// Entry returns the entry block, where execution starts. It takes no block
// parameters; Param declares the function's parameters there.
func (pb *ProgramBuilder) Entry() Block { return 0 }

func (pb *ProgramBuilder) fail(format string, args ...any) {
	if pb.err == nil {
		pb.err = fmt.Errorf("ProgramBuilder: "+format+": %w", append(args, ErrInvalidProgram)...)
	}
}

func (pb *ProgramBuilder) open(b Block) *builderBlock {
	switch {
	case pb.err != nil:
		return nil
	case pb.sealed:
		pb.fail("already sealed")
	case b < 0 || int(b) >= len(pb.blocks):
		pb.fail("unknown block %d", b)
	case pb.blocks[b].done:
		pb.fail("block %d is already terminated", b)
	default:
		return &pb.blocks[b]
	}
	return nil
}

func (pb *ProgramBuilder) add(b Block, block *builderBlock, ins Instruction) int {
	id := len(pb.owner)
	pb.owner = append(pb.owner, instructionRef{int(b), len(block.code)})
	block.code = append(block.code, ins)
	block.ids = append(block.ids, id)
	return id
}

// value reports whether id names an instruction that produces a value.
func (pb *ProgramBuilder) value(id int) bool {
	if id < 0 || id >= len(pb.owner) {
		return false
	}
	ref := pb.owner[id]
	return pb.blocks[ref.block].code[ref.index].Op.ProducesValue()
}

func (pb *ProgramBuilder) checkValues(what string, ids []int) bool {
	for _, id := range ids {
		if !pb.value(id) {
			pb.fail("%s: %d is not a value", what, id)
			return false
		}
	}
	return true
}

// Param appends a function parameter of scalar type t to the entry block and
// returns its ID. Parameters are numbered in the order Param declares them.
func (pb *ProgramBuilder) Param(entry Block, t Type) int {
	block := pb.open(entry)
	if block == nil {
		return -1
	}
	if entry != pb.Entry() || t.Kind != ScalarKind || !t.Valid() || t.LaneBits > 64 {
		pb.fail("Param needs the entry block and a scalar type of at most 64 bits")
		return -1
	}
	return pb.add(entry, block, Instruction{Op: OpParam, Bits: t.LaneBits})
}

// NewBlock returns a new block whose parameters have the given types.
func (pb *ProgramBuilder) NewBlock(params ...Type) Block {
	if pb.err != nil || pb.sealed {
		pb.fail("already sealed")
		return -1
	}
	b := Block(len(pb.blocks))
	pb.blocks = append(pb.blocks, builderBlock{})
	block := &pb.blocks[b]
	for i, t := range params {
		ins := Instruction{Op: OpLabelParam, Imm: uint64(i), Bits: t.LaneBits}
		switch {
		case !t.Valid() || t.Kind == ScalarKind && t.LaneBits > 64:
			pb.fail("block parameter %d has invalid type %+v", i, t)
			return -1
		case t.Kind == MaskKind:
			ins.Type, ins.Bits = t, 64
		case t.Kind == VectorKind:
			ins.Type = t
		}
		block.params = append(block.params, pb.add(b, block, ins))
	}
	return b
}

// BlockParam returns the ID of block b's parameter i.
func (pb *ProgramBuilder) BlockParam(b Block, i int) int {
	if pb.err != nil {
		return -1
	}
	if b < 0 || int(b) >= len(pb.blocks) || i < 0 || i >= len(pb.blocks[b].params) {
		pb.fail("block %d has no parameter %d", b, i)
		return -1
	}
	return pb.blocks[b].params[i]
}

// Append appends ins to block b and returns its ID. Its operands (A, B, C
// and Args, as the opcode uses them) are IDs of earlier instructions that
// produce values. Use Param and NewBlock for parameters; Vars and scopes
// (OpGet, OpSet, OpScopeEnter, OpScopeLeave) are not available here. Append
// does not copy ins.Args, ins.Data or ins.Effect; Seal does, so the caller may
// reuse them once the program is sealed.
func (pb *ProgramBuilder) Append(b Block, ins Instruction) int {
	block := pb.open(b)
	if block == nil {
		return -1
	}
	switch ins.Op {
	case OpParam, OpLabelParam, OpGet, OpSet, OpScopeEnter, OpScopeLeave:
		pb.fail("Append cannot add %s", ins.Op)
		return -1
	}
	if ins.Op >= opcodeEnd {
		pb.fail("unknown opcode %d", ins.Op)
		return -1
	}
	var buffer [4]int
	if !pb.checkValues(ins.Op.String()+" operand", ins.InputsInto(buffer[:0])) {
		return -1
	}
	return pb.add(b, block, ins)
}

func (pb *ProgramBuilder) terminate(from Block, term ControlTerm, edges [2]Block, args [2][]int, pred *PredicateExpr, result []int) {
	block := pb.open(from)
	if block == nil {
		return
	}
	targets := 0
	switch term {
	case TermJump:
		targets = 1
	case TermBranch:
		targets = 2
	}
	for i := range targets {
		to := edges[i]
		if to < 0 || int(to) >= len(pb.blocks) {
			pb.fail("unknown block %d", to)
			return
		}
		if to == pb.Entry() && len(args[i]) != 0 || len(args[i]) != len(pb.blocks[to].params) {
			pb.fail("edge to block %d passes %d arguments for %d parameters", to, len(args[i]), len(pb.blocks[to].params))
			return
		}
		if !pb.checkValues("edge argument", args[i]) {
			return
		}
	}
	if !pb.checkValues("result", result) {
		return
	}
	block.term, block.done, block.to, block.args, block.pred, block.result = term, true, edges, args, pred, result
}

// Jump ends block from with a jump to block to, passing args to its
// parameters.
func (pb *ProgramBuilder) Jump(from, to Block, args ...int) {
	pb.terminate(from, TermJump, [2]Block{to}, [2][]int{args}, nil, nil)
}

// Branch ends block from with a conditional branch: to ifTrue with trueArgs
// when predicate holds, otherwise to ifFalse with falseArgs. The predicate's
// operands are IDs; Branch does not copy it, and Seal does not modify it.
func (pb *ProgramBuilder) Branch(from Block, predicate *PredicateExpr, ifTrue Block, trueArgs []int, ifFalse Block, falseArgs []int) {
	if pb.err == nil {
		values, err := predicateValues(predicate)
		if err != nil {
			pb.fail("Branch: %v", err)
			return
		}
		if !pb.checkValues("predicate operand", values) {
			return
		}
	}
	pb.terminate(from, TermBranch, [2]Block{ifTrue, ifFalse}, [2][]int{trueArgs, falseArgs}, predicate, nil)
}

// Return ends block from by returning value. Two values return a 128-bit
// result as its low and high 64-bit halves.
func (pb *ProgramBuilder) Return(from Block, values ...int) {
	if pb.err == nil && len(values) != 1 && len(values) != 2 {
		pb.fail("Return takes one value or two halves, not %d", len(values))
		return
	}
	pb.terminate(from, TermReturn, [2]Block{}, [2][]int{}, nil, values)
}

// predicateValues returns the operands of predicate in first-use order,
// without duplicates.
func predicateValues(root *PredicateExpr) ([]int, error) {
	var values []int
	seenValue := map[int]bool{}
	seen := map[*PredicateExpr]bool{}
	work := []*PredicateExpr{root}
	for len(work) != 0 {
		node := work[len(work)-1]
		work = work[:len(work)-1]
		if node == nil {
			return nil, errors.New("missing predicate node")
		}
		if seen[node] {
			continue
		}
		seen[node] = true
		switch node.Kind {
		case PredicateCompare:
			for i, id := range [3]int{node.Value, node.Other, node.Anchor} {
				if i == 1 && !node.WithValue || i == 2 && !node.HasAnchor || seenValue[id] {
					continue
				}
				seenValue[id] = true
				values = append(values, id)
			}
		case PredicateNot:
			work = append(work, node.Left)
		case PredicateAnd, PredicateOr:
			work = append(work, node.Right, node.Left)
		default:
			return nil, fmt.Errorf("unknown predicate kind %d", node.Kind)
		}
	}
	return values, nil
}

// Seal lays out the blocks reachable from the entry in reverse postorder,
// so that every dominating block precedes the blocks it dominates, and seals
// the result (see Seal). Every reachable block must be terminated; blocks
// unreachable from the entry are dropped. A *ValidationError's Index is the
// builder's instruction ID. The builder cannot be used afterwards.
func (pb *ProgramBuilder) Seal() (*SealedProgram, error) {
	if pb.err != nil {
		return nil, pb.err
	}
	if pb.sealed {
		return nil, fmt.Errorf("ProgramBuilder: already sealed: %w", ErrInvalidProgram)
	}
	pb.sealed = true
	// Reverse postorder of the reachable blocks.
	n := len(pb.blocks)
	visited := make([]bool, n)
	post := make([]int, 0, n)
	type frame struct{ block, next int }
	visited[0] = true
	stack := []frame{{0, 0}}
	for len(stack) != 0 {
		top := &stack[len(stack)-1]
		block := &pb.blocks[top.block]
		if !block.done {
			return nil, fmt.Errorf("ProgramBuilder: block %d is reachable but not terminated: %w", top.block, ErrInvalidProgram)
		}
		targets := 0
		switch block.term {
		case TermJump:
			targets = 1
		case TermBranch:
			targets = 2
		}
		if top.next == targets {
			post = append(post, top.block)
			stack = stack[:len(stack)-1]
			continue
		}
		next := int(block.to[top.next])
		top.next++
		if !visited[next] {
			visited[next] = true
			stack = append(stack, frame{next, 0})
		}
	}
	order := make([]int, len(post))
	label := make([]int, n) // layout position, or -1 when dropped
	for i := range label {
		label[i] = -1
	}
	for i, b := range post {
		order[len(post)-1-i] = b
	}
	for position, b := range order {
		label[b] = position
	}
	// Number the laid-out instructions.
	index := make([]int, len(pb.owner))
	for i := range index {
		index[i] = -1
	}
	handle := make([]int, 0, len(pb.owner))
	for _, b := range order {
		for _, id := range pb.blocks[b].ids {
			index[id] = len(handle)
			handle = append(handle, id)
		}
	}
	remapErr := error(nil)
	remap := func(id int) int {
		if index[id] < 0 && remapErr == nil {
			remapErr = &ValidationError{Index: id, Op: pb.blocks[pb.owner[id].block].code[pb.owner[id].index].Op,
				Rule: "value is defined in a block unreachable from the entry"}
		}
		return index[id]
	}
	remapAll := func(ids []int) []int {
		if len(ids) == 0 {
			return nil
		}
		out := make([]int, len(ids))
		for i, id := range ids {
			out[i] = remap(id)
		}
		return out
	}
	p := &Program{Instructions: make([]Instruction, 0, len(handle)), Result: -1}
	control := &Control{
		Labels: make([]bool, len(order)), LabelScopes: make([]int, len(order)),
		Params: make([]int, len(order)), ParamBits: make([][]uint8, len(order)),
	}
	typed := false
	for position, b := range order {
		control.Labels[position] = true
		for _, id := range pb.blocks[b].params {
			ins := &pb.blocks[b].code[pb.owner[id].index]
			control.Params[position]++
			control.ParamBits[position] = append(control.ParamBits[position], ins.Bits)
			typed = typed || ins.Type.Kind == VectorKind || ins.Type.Kind == MaskKind
		}
	}
	if typed {
		control.ParamTypes = make([][]Type, len(order))
		for position, b := range order {
			for _, id := range pb.blocks[b].params {
				control.ParamTypes[position] = append(control.ParamTypes[position], pb.blocks[b].code[pb.owner[id].index].ValueType())
			}
		}
	}
	var buffer [4]int
	for position, b := range order {
		block := &pb.blocks[b]
		start := len(p.Instructions)
		arity := 0
		for _, ins := range block.code {
			// The sealed program owns its storage: Append kept the caller's
			// Args, Data and Effect, which the caller may reuse.
			ins = ins.Clone()
			if ins.Op == OpParam {
				arity++
			}
			switch ins.Op {
			case OpSyscall, OpCallC, OpCallGo:
				ins.Args = remapAll(ins.Args)
			default:
				switch len(ins.InputsInto(buffer[:0])) {
				case 3:
					ins.C = remap(ins.C)
					fallthrough
				case 2:
					ins.B = remap(ins.B)
					fallthrough
				case 1:
					ins.A = remap(ins.A)
				}
			}
			p.Instructions = append(p.Instructions, ins)
		}
		p.Arity += arity
		step := ControlStep{Start: start, End: len(p.Instructions), Term: block.term, Arity: arity, To: -1, Other: -1}
		switch block.term {
		case TermJump:
			step.To, step.Args = label[block.to[0]], remapAll(block.args[0])
		case TermBranch:
			step.To, step.Args = label[block.to[0]], remapAll(block.args[0])
			step.Other, step.OtherArgs = label[block.to[1]], remapAll(block.args[1])
			values, _ := predicateValues(block.pred)
			step.Values = remapAll(values)
			step.Result = step.Values[0]
			step.Predicate = copyPredicateWith(block.pred, remap)
		case TermReturn:
			step.Result = remap(block.result[0])
			if len(block.result) == 2 {
				step.ResultParts = remapAll(block.result)
			}
		}
		control.Steps = append(control.Steps, ControlStep{IsLabel: true, Label: position}, step)
	}
	if remapErr != nil {
		return nil, remapErr
	}
	p.Control = control
	sealed, err := SealOwned(p)
	if err != nil {
		var invalid *ValidationError
		if errors.As(err, &invalid) && invalid.Index >= 0 && invalid.Index < len(handle) {
			located := *invalid
			located.Index = handle[invalid.Index]
			return nil, &located
		}
		return nil, err
	}
	return sealed, nil
}

// copyPredicateWith copies a predicate tree, preserving shared nodes, with
// each operand replaced by remap(operand).
func copyPredicateWith(root *PredicateExpr, remap func(int) int) *PredicateExpr {
	memo := make(map[*PredicateExpr]*PredicateExpr)
	var work []*PredicateExpr
	copyRef := func(n *PredicateExpr) *PredicateExpr {
		if n == nil {
			return nil
		}
		if c := memo[n]; c != nil {
			return c
		}
		c := *n
		memo[n] = &c
		work = append(work, n)
		return &c
	}
	out := copyRef(root)
	for len(work) != 0 {
		n := work[len(work)-1]
		work = work[:len(work)-1]
		c := memo[n]
		c.Left, c.Right = copyRef(n.Left), copyRef(n.Right)
		if c.Kind == PredicateCompare {
			c.Value = remap(c.Value)
			if c.WithValue {
				c.Other = remap(c.Other)
			}
			if c.HasAnchor {
				c.Anchor = remap(c.Anchor)
			}
		}
	}
	return out
}
