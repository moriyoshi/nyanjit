package builder

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"sync"

	"github.com/moriyoshi/nyanjit/ir"
)

// A segment Builder keeps no program. It keeps the open block's instructions
// in one reused buffer, a valueRecord per instruction ID for type, scope and
// capture checks, and, for the dominance check at End, the block graph and
// the uses of values from earlier blocks.

// segmentState is the state of a Builder from NewSegmentBuilder.
type segmentState struct {
	emit   func(*ir.Segment) error
	err    error // emit's first error
	failed bool
	// testFinish, set through testSegmentHook, is what Finish returns.
	testFinish func(endErr error) (*ir.SealedProgram, error)

	out ir.Segment
	// The open block: code[i] has ID codeBase+i. Its Variables start at
	// varBase, and leaves[i] is ScopeExit leaveBase+i.
	code      []instruction
	codeBase  int
	labels    []ir.Label
	varBase   int
	leaves    []ir.ScopeExit
	leaveBase int
	// ints holds the open block's ScopeExit and step lists; preds its
	// frozen predicate. Both are reused once the block is emitted.
	ints         []int
	preds        predicateSlab
	freezeSerial uint64
	freezeWork   []*predicateNode
	// enterVars[s] is the Variable count at scope s's Enter.
	enterVars []int

	// emitted counts the segments handed to emit; block is the index of the
	// open block's first segment. entryEnd ends the entry segment, whose
	// values dominate every block.
	emitted  int
	block    int
	entryEnd int

	// A one-target Branch's block is staged until the next operation shows
	// whether a Jump retargets its false edge. step is its terminator, the
	// staged* fields its extent, and stagedLabel the fallthrough label that
	// starts the next block, or -1.
	staged       bool
	step         ir.ControlStep
	stagedEnd    int
	stagedVars   int
	stagedLeaves int
	stagedLabel  ir.Label

	// The block graph and the checks that span segments.
	blockStarts    []int32
	blockSucc      [][2]int32
	labelBlock     []int32
	uses           []crossUse
	predicateNodes int
	wideReturn     bool
	narrowReturn   bool

	// spareValues is the Builder's value records, kept with the other
	// buffers between Builders. payloads means some instruction in code
	// referred to memory, which recycle then clears.
	spareValues []valueRecord
	spareLabels []bool
	spareScopes []int
	spareFrame  scopeFrame
	payloads    bool
	codeHigh    int
}

// segmentPool recycles the buffers of ended segment Builders, so that a
// function built in segment mode allocates little beyond what it keeps.
var segmentPool sync.Pool

func newSegmentState(emit func(*ir.Segment) error) *segmentState {
	s, _ := segmentPool.Get().(*segmentState)
	if s == nil {
		s = new(segmentState)
	}
	s.emit, s.entryEnd, s.stagedLabel = emit, math.MaxInt, -1
	s.enterVars = append(s.enterVars[:0], 0)
	return s
}

// recycle returns s's buffers to segmentPool, with the Builder's value
// records and label tables.
func (s *segmentState) recycle(values []valueRecord, labels []bool, labelScopes []int, frame scopeFrame) {
	if s.payloads {
		clear(s.code[:s.codeHigh])
	}
	clear(s.leaves[:cap(s.leaves)])
	s.preds.reset()
	clear(s.freezeWork[:cap(s.freezeWork)])
	clear(frame.variables[:cap(frame.variables)])
	*s = segmentState{
		code: s.code[:0], labels: s.labels[:0], leaves: s.leaves[:0], ints: s.ints[:0], preds: s.preds,
		freezeWork: s.freezeWork[:0], enterVars: s.enterVars[:0], blockStarts: s.blockStarts[:0],
		blockSucc: s.blockSucc[:0], labelBlock: s.labelBlock[:0], uses: s.uses[:0], spareValues: values[:0],
		spareLabels: labels[:0], spareScopes: labelScopes[:0], spareFrame: frame.empty(),
	}
	segmentPool.Put(s)
}

// scopeFrame holds a Builder's per-scope and Var tables between Builders.
type scopeFrame struct {
	scopes, scopeStarts, captureStarts, captureEnds []int
	scopeActive, scopeTerminated                    []bool
	variables                                       []ir.Variable
	initials                                        []int
}

// empty returns f's tables with their storage and no elements.
func (f scopeFrame) empty() scopeFrame {
	return scopeFrame{
		scopes: f.scopes[:0], scopeStarts: f.scopeStarts[:0], captureStarts: f.captureStarts[:0], captureEnds: f.captureEnds[:0],
		scopeActive: f.scopeActive[:0], scopeTerminated: f.scopeTerminated[:0],
		variables: f.variables[:0], initials: f.initials[:0],
	}
}

// crossUse is a use at pos (or -1 for a terminator, export or Var operand)
// in block of a value def from an earlier block.
type crossUse struct{ def, pos, block int32 }

// NewSegmentBuilder creates a Builder that hands each block to emit as an
// ir.Segment when the block ends, instead of recording a program for Finish.
// The rules for Values, Vars and scopes are those of every Builder. If emit
// returns an error, the method that ended the block returns it, later methods
// return ErrInvalidFunction, and End returns emit's error.
func NewSegmentBuilder(emit func(*ir.Segment) error) *Builder {
	// A one-target Branch's block is handed over by the next operation,
	// once it is known whether a Jump gives the Branch its false edge; an
	// error from that hand-over is returned by End and makes later methods
	// fail.
	b := &Builder{expressionArena: new(expressionArena), generation: newGeneration()}
	b.storageOwner = b
	b.seg = newSegmentState(emit)
	b.values = b.seg.spareValues
	b.seg.spareValues = nil
	// The root scope's tables reuse an ended Builder's.
	f := &b.seg.spareFrame
	b.scopes, b.scopeStarts = append(f.scopes, 0), append(f.scopeStarts, 0)
	b.scopeActive, b.scopeTerminated = append(f.scopeActive, true), append(f.scopeTerminated, false)
	b.captureStarts, b.captureEnds = append(f.captureStarts, 0), append(f.captureEnds, 0)
	b.variables, b.variableInitials = f.variables, f.initials
	b.seg.spareFrame = scopeFrame{}
	return b
}

// End finishes a Builder from NewSegmentBuilder. It reports open scopes,
// unbound labels and a missing return as Finish does, and otherwise returns
// emit's first error, if any. On a Builder from NewBuilder it returns
// ErrInvalidFunction, as Finish does on a segment Builder.
func (b *Builder) End() error {
	if b == nil || b.seg == nil || b.finished {
		return fmt.Errorf("End: builder is nil, finished, or not from NewSegmentBuilder: %w", ErrInvalidFunction)
	}
	if b.storageOwner != b {
		b.detachCopy()
		return fmt.Errorf("End: builder is a copy: %w", ErrInvalidFunction)
	}
	s := b.seg
	if s.staged {
		b.flushStaged()
	}
	if s.failed {
		err := s.err
		b.releaseSegment()
		return err
	}
	if len(b.scopes) != 1 {
		return fmt.Errorf("End: %d nested scopes remain open: %w", len(b.scopes)-1, ErrInvalidFunction)
	}
	if b.codeLen() == 0 && b.control == nil {
		return fmt.Errorf("End: function has no instructions or return: %w", ErrInvalidFunction)
	}
	if b.control == nil {
		if !b.returned {
			return fmt.Errorf("End: function has no return: %w", ErrInvalidFunction)
		}
	} else {
		if b.control.active || !b.control.hasTerm {
			return fmt.Errorf("End: control-flow block has no terminating branch or return: %w", ErrInvalidFunction)
		}
		for label, bound := range b.control.labels {
			if !bound {
				return fmt.Errorf("End: label %d is unbound: %w", label, ErrInvalidFunction)
			}
		}
	}
	if b.splitErr != nil {
		return fmt.Errorf("End: scope entry validation failed: %w: %w", ErrInvalidFunction, b.splitErr)
	}
	if err := b.checkSegments(); err != nil {
		return fmt.Errorf("End: program validation failed: %w: %w", ErrInvalidFunction, err)
	}
	b.releaseSegment()
	return nil
}

func (b *Builder) releaseSegment() {
	var labels []bool
	var labelScopes []int
	if b.control != nil {
		labels, labelScopes = b.control.labels, b.control.labelScopes
	}
	b.seg.recycle(b.values, labels, labelScopes, scopeFrame{
		scopes: b.scopes, scopeStarts: b.scopeStarts, captureStarts: b.captureStarts, captureEnds: b.captureEnds,
		scopeActive: b.scopeActive, scopeTerminated: b.scopeTerminated,
		variables: b.variables, initials: b.variableInitials,
	})
	b.seg, b.codeSize = nil, 0
	b.values, b.scopes, b.scopeStarts, b.control, b.variables, b.captures = nil, nil, nil, nil, nil, nil
	b.scopeActive, b.scopeTerminated, b.captureStarts, b.captureEnds = nil, nil, nil, nil
	b.ownedStack, b.ownedMarks, b.variableInitials, b.checkedAddresses = nil, nil, nil, nil
	if arena := b.expressionArena; arena != nil {
		arena.mu.Lock()
		arena.slab, arena.predicates = nil, nil
		arena.mu.Unlock()
	}
	b.expressionWork, b.expressionSeen, b.predicateWork, b.predicateIDs, b.leaveWork = nil, nil, nil, nil, nil
	b.arity, b.result, b.returned, b.finished = 0, -1, false, true
}

// use records a use, at pos in the open block, of a value from an earlier
// block. Values of the entry segment dominate every block.
func (s *segmentState) use(id, pos int) {
	if id < s.codeBase && id >= s.entryEnd {
		s.uses = append(s.uses, crossUse{int32(id), int32(pos), int32(s.block)})
	}
}

func (s *segmentState) appendInstruction(i *instruction, id int) {
	if lo, hi := s.entryEnd, s.codeBase; hi > lo {
		// Most operands come from the open block or the entry segment.
		// Unused operand fields are zero, which a precise walk ignores.
		if i.Args != nil || uint(i.A-lo) < uint(hi-lo) || uint(i.B-lo) < uint(hi-lo) || uint(i.C-lo) < uint(hi-lo) {
			var inputBuffer [4]int
			for _, input := range i.InputsInto(inputBuffer[:0]) {
				s.use(input, id)
			}
		}
	}
	if i.Args != nil || i.Data != nil || i.Effect != nil || i.Symbol != "" {
		s.payloads = true
	}
	n := len(s.code)
	if n == cap(s.code) {
		s.code = append(s.code, instruction{})
	} else {
		s.code = s.code[:n+1]
	}
	if s.payloads {
		copyInstruction(&s.code[n], i)
	} else {
		// Until a payload is stored, every slot's pointer fields are nil.
		copyScalars(&s.code[n], i)
	}
	s.codeHigh = max(s.codeHigh, n+1)
}

// copyScalars copies the fields of an instruction without payloads.
func copyScalars(d, i *instruction) {
	d.Variable, d.Type, d.Op, d.Bits = i.Variable, i.Type, i.Op, i.Bits
	d.EntryReg, d.A, d.B, d.C, d.Imm = i.EntryReg, i.A, i.B, i.C, i.Imm
	d.ByteOrder, d.Aligned, d.Contract = i.ByteOrder, i.Aligned, i.Contract
}

// copyInstruction copies field by field: an instruction copy as one value
// moves its pointer fields with a bulk write barrier, and most instructions
// have none.
func copyInstruction(d, i *instruction) {
	d.Variable, d.Type, d.Op, d.Bits = i.Variable, i.Type, i.Op, i.Bits
	d.EntryReg, d.A, d.B, d.C, d.Imm = i.EntryReg, i.A, i.B, i.C, i.Imm
	d.ByteOrder, d.Aligned, d.Contract = i.ByteOrder, i.Aligned, i.Contract
	d.Data, d.Symbol, d.Args, d.Effect = i.Data, i.Symbol, i.Args, i.Effect
}

// allocInts returns n integers of the open block's slab.
func (s *segmentState) allocInts(n int) []int {
	if cap(s.ints)-len(s.ints) < n {
		// Earlier lists keep the old array; it is dropped with the block.
		s.ints = make([]int, 0, max(n, 2*cap(s.ints), 64))
	}
	start := len(s.ints)
	s.ints = s.ints[:start+n]
	return s.ints[start : start+n : start+n]
}

func (s *segmentState) copyInts(values []int) []int {
	if values == nil {
		return nil
	}
	out := s.allocInts(len(values))
	copy(out, values)
	return out
}

// stage keeps the open block, which a one-target Branch ended with step.
func (s *segmentState) stage(b *Builder, step ir.ControlStep) {
	s.staged, s.step, s.stagedLabel = true, step, -1
	s.stagedEnd, s.stagedVars, s.stagedLeaves = b.codeLen(), len(b.variables), len(s.leaves)
}

// flushStaged hands the staged block to emit. The fallthrough label then
// starts the open block.
func (b *Builder) flushStaged() error {
	s := b.seg
	if !s.staged {
		return nil
	}
	s.staged = false
	err := b.emitSegment(s.stagedEnd, s.stagedVars, s.stagedLeaves, s.step, true)
	if s.stagedLabel >= 0 {
		s.labels = append(s.labels, s.stagedLabel)
		s.stagedLabel = -1
	}
	return err
}

// emitSegment hands the open block's code up to end, its Variables up to
// varEnd and its first leaveEnd ScopeExits to emit, ending with step. final
// means the whole block has been handed over. It returns emit's error.
func (b *Builder) emitSegment(end, varEnd, leaveEnd int, step ir.ControlStep, final bool) error {
	s := b.seg
	n := end - s.codeBase
	step.Start, step.End = s.codeBase, end
	step.LeaveStart, step.LeaveEnd = s.leaveBase, s.leaveBase+leaveEnd
	// Assign field by field: a whole-struct assignment moves the pointer
	// fields with a bulk write barrier.
	out := &s.out
	out.Labels, out.Base, out.Code = s.labels, s.codeBase, s.code[:n]
	out.Variables, out.ScopeExits = b.variables[s.varBase:varEnd], s.leaves[:leaveEnd]
	out.Step, out.ParameterTypes = step, nil
	if s.emitted == 0 && len(b.parameterTypes) != 0 {
		types := make([]reflect.Type, b.arity)
		for index, typ := range b.parameterTypes {
			types[index] = typ
		}
		s.out.ParameterTypes = types
	}

	index := s.emitted
	s.emitted++
	if index == 0 {
		s.entryEnd = end
	}
	s.blockStarts = append(s.blockStarts, int32(s.codeBase))
	successors := [2]int32{-1, -1}
	switch step.Term {
	case ir.TermJump:
		successors[0] = int32(step.To)
	case ir.TermBranch:
		successors = [2]int32{int32(step.To), int32(step.Other)}
	}
	s.blockSucc = append(s.blockSucc, successors)
	for _, label := range s.labels {
		for int(label) >= len(s.labelBlock) {
			s.labelBlock = append(s.labelBlock, -1)
		}
		s.labelBlock[label] = int32(index)
	}

	var err error
	if !s.failed {
		if s.emit == nil {
			err = fmt.Errorf("NewSegmentBuilder: emit is nil: %w", ErrInvalidFunction)
		} else {
			err = s.emit(&s.out)
		}
		if err != nil {
			s.err, s.failed = err, true
		}
	}
	out.Labels, out.Code, out.Variables, out.ScopeExits, out.ParameterTypes = nil, nil, nil, nil, nil
	out.Step.ResultParts, out.Step.Values, out.Step.Args, out.Step.OtherArgs, out.Step.Predicate = nil, nil, nil, nil, nil

	s.code = s.code[:copy(s.code, s.code[n:])]
	s.leaves = s.leaves[:copy(s.leaves, s.leaves[leaveEnd:])]
	s.labels = s.labels[:0]
	s.codeBase, s.varBase, s.leaveBase = end, varEnd, s.leaveBase+leaveEnd
	if final {
		s.ints = s.ints[:0]
		s.preds.reset()
	}
	return err
}

// segmentReturn ends the function's block with a return of result, or of
// the 128-bit pair parts.
func (b *Builder) segmentReturn(result int, parts []int) error {
	s := b.seg
	if parts != nil {
		s.wideReturn = true
		s.use(parts[0], -1)
		s.use(parts[1], -1)
	} else {
		s.narrowReturn = true
		s.use(result, -1)
	}
	if b.control != nil {
		_, err := b.endControl(controlEnd{term: builderReturn, result: result, resultParts: parts, to: -1, other: -1})
		return err
	}
	b.returned, b.result, b.resultParts = true, result, parts
	return b.emitSegment(b.codeLen(), len(b.variables), len(s.leaves),
		ir.ControlStep{Term: ir.TermReturn, Result: result, ResultParts: s.copyInts(parts), To: -1, Other: -1, Arity: b.arity}, true)
}

// freezePredicate copies a predicate DAG into the open block's slab,
// sharing the copies of shared nodes.
func (s *segmentState) freezePredicate(root *predicateNode) *ir.PredicateExpr {
	if root == nil {
		return nil
	}
	s.freezeSerial++
	serial := s.freezeSerial
	work := s.freezeWork[:0]
	copyRef := func(node *predicateNode) *ir.PredicateExpr {
		if node == nil {
			return nil
		}
		if node.frozenAt == serial {
			return node.frozen
		}
		frozen := s.preds.alloc()
		*frozen = ir.PredicateExpr{
			Kind: ir.PredicateKind(node.kind), Condition: node.condition,
			Value: node.value, Other: node.other, Anchor: node.anchor,
			Immediate: node.immediate, WithValue: node.withValue, HasAnchor: node.hasAnchor,
		}
		node.frozen, node.frozenAt = frozen, serial
		work = append(work, node)
		return frozen
	}
	result := copyRef(root)
	for len(work) != 0 {
		node := work[len(work)-1]
		work = work[:len(work)-1]
		node.frozen.Left, node.frozen.Right = copyRef(node.left), copyRef(node.right)
	}
	clear(work[:cap(work)])
	s.freezeWork = work[:0]
	return result
}

// predicateSlab allocates frozen predicate nodes in chunks that never move.
type predicateSlab struct {
	chunks [][]ir.PredicateExpr
	chunk  int
	used   int
}

const predicateChunk = 64

func (p *predicateSlab) alloc() *ir.PredicateExpr {
	if p.chunk == len(p.chunks) {
		p.chunks = append(p.chunks, make([]ir.PredicateExpr, predicateChunk))
	}
	node := &p.chunks[p.chunk][p.used]
	if p.used++; p.used == predicateChunk {
		p.chunk, p.used = p.chunk+1, 0
	}
	return node
}

func (p *predicateSlab) reset() {
	for i := 0; i < p.chunk; i++ {
		clear(p.chunks[i])
	}
	if p.chunk < len(p.chunks) {
		clear(p.chunks[p.chunk][:p.used])
	}
	p.chunk, p.used = 0, 0
}

// checkSegments checks the rules that span segments: the program predicate
// budget, return widths and dominance.
func (b *Builder) checkSegments() error {
	s := b.seg
	if s.predicateNodes > ir.MaxPredicateNodes {
		return fmt.Errorf("program predicate budget %d exceeded: %w", ir.MaxPredicateNodes, ir.ErrPredicateLimit)
	}
	if s.wideReturn && s.narrowReturn {
		return &ir.ValidationError{Index: -1, Rule: "either every return is 128-bit or none is"}
	}
	if len(s.uses) == 0 {
		return nil
	}
	idom, reachable := s.dominators()
	for _, use := range s.uses {
		block := int(use.block)
		if !reachable[block] {
			continue
		}
		def := sort.Search(len(s.blockStarts), func(i int) bool { return s.blockStarts[i] > use.def }) - 1
		for block != def && block != 0 {
			block = int(idom[block])
		}
		if block != def {
			err := &ir.ValidationError{Index: int(use.pos), Rule: fmt.Sprintf("value %d is used in a block its definition does not dominate", use.def)}
			if use.pos >= 0 {
				err.Op = b.values[use.pos].op
			}
			return err
		}
	}
	return nil
}

// dominators returns the immediate dominator of every block reachable from
// the entry, by the iterative algorithm of Cooper, Harvey and Kennedy.
func (s *segmentState) dominators() (idom []int32, reachable []bool) {
	n := len(s.blockStarts)
	successor := func(block, k int) int {
		label := s.blockSucc[block][k]
		if label < 0 || int(label) >= len(s.labelBlock) {
			return -1
		}
		return int(s.labelBlock[label])
	}
	reachable = make([]bool, n)
	order := make([]int32, n) // order[block] is its reverse-postorder number
	postorder := make([]int32, 0, n)
	type frame struct{ block, next int32 }
	stack := []frame{{0, 0}}
	reachable[0] = true
	for len(stack) != 0 {
		top := &stack[len(stack)-1]
		if top.next < 2 {
			next := successor(int(top.block), int(top.next))
			top.next++
			if next >= 0 && !reachable[next] {
				reachable[next] = true
				stack = append(stack, frame{int32(next), 0})
			}
			continue
		}
		postorder = append(postorder, top.block)
		stack = stack[:len(stack)-1]
	}
	for i, block := range postorder {
		order[block] = int32(len(postorder) - 1 - i)
	}
	predecessors := make([][]int32, n)
	for block := range n {
		if !reachable[block] {
			continue
		}
		for k := range 2 {
			if next := successor(block, k); next >= 0 && (k == 0 || successor(block, 0) != next) {
				predecessors[next] = append(predecessors[next], int32(block))
			}
		}
	}
	idom = make([]int32, n)
	for i := range idom {
		idom[i] = -1
	}
	idom[0] = 0
	intersect := func(a, b int32) int32 {
		for a != b {
			for order[a] > order[b] {
				a = idom[a]
			}
			for order[b] > order[a] {
				b = idom[b]
			}
		}
		return a
	}
	for changed := true; changed; {
		changed = false
		for i := len(postorder) - 1; i >= 0; i-- {
			block := postorder[i]
			if block == 0 {
				continue
			}
			next := int32(-1)
			for _, p := range predecessors[block] {
				if idom[p] < 0 {
					continue
				}
				if next < 0 {
					next = p
				} else {
					next = intersect(p, next)
				}
			}
			if next != idom[block] {
				idom[block], changed = next, true
			}
		}
	}
	return idom, reachable
}

// appendScalar is append for an integer operation whose value is a scalar
// of bits, without payload, Type, Variable or C, and with inputs x
// (inputs >= 1) and y (inputs == 2).
func (b *Builder) appendScalar(operation op, bits uint8, x, y int, imm uint64, inputs int) Value {
	return b.appendPlain(operation, ir.Type{}, bits, 0, x, y, imm, inputs)
}

// appendPlain is append for an instruction without payload or C, with
// inputs x (inputs >= 1) and y (inputs == 2). A segment Builder writes the
// instruction in place, because copying a stack temporary stalls store
// forwarding; it otherwise has append's effects.
func (b *Builder) appendPlain(operation op, typ ir.Type, bits uint8, variable, x, y int, imm uint64, inputs int) Value {
	s := b.seg
	if s == nil || b.storageOwner != b || len(b.scopeActive) == 0 || len(b.checkedAddresses) != 0 ||
		b.control != nil && len(b.scopes) > 1 {
		return b.append(instruction{Op: operation, Type: typ, Variable: variable, A: x, B: y, Imm: imm, Bits: bits})
	}
	if s.staged {
		b.flushStaged()
	}
	id := b.codeSize
	if inputs != 0 {
		s.use(x, id)
		if inputs == 2 {
			s.use(y, id)
		}
	}
	n := len(s.code)
	if n == cap(s.code) {
		s.code = append(s.code, instruction{})
	} else {
		s.code = s.code[:n+1]
	}
	d := &s.code[n]
	d.Variable, d.Type, d.Op, d.Bits = variable, typ, operation, bits
	d.EntryReg, d.A, d.B, d.C, d.Imm = 0, x, y, 0, imm
	d.ByteOrder, d.Aligned, d.Contract = 0, false, false
	if s.payloads {
		d.Data, d.Symbol, d.Args, d.Effect = nil, "", nil, nil
	}
	s.codeHigh = max(s.codeHigh, n+1)
	b.codeSize++

	owner := b.scopes[len(b.scopes)-1]
	k := len(b.values)
	if k == cap(b.values) {
		b.values = append(b.values, valueRecord{})
	} else {
		b.values = b.values[:k+1]
	}
	r := &b.values[k]
	if typ == (ir.Type{}) {
		typ = ir.Type{Kind: ir.ScalarKind, Lanes: 1, LaneBits: bits}
	}
	r.typ, r.op, r.flags, r.scope = typ, operation, 0, int32(owner)
	r.lo, r.hi = 0, 0
	switch operation {
	case opConst:
		r.flags = recordLiteral
		r.lo, r.hi = uint32(imm), uint32(imm>>32)
	case opAdd, opSub:
		r.lo, r.hi = uint32(x), uint32(y)
	}
	if owner != 0 && operation.ProducesValue() {
		b.ownedStack = append(b.ownedStack, id)
	}
	return Value{builder: b, id: id}
}
