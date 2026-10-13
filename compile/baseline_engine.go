package compile

import (
	"fmt"
	"reflect"
	"slices"
	"sync"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

// The baseline tier (TierBaseline) compiles a function without building SSA.
// Stream.Segment converts each ir.Segment to pointer-free records (bRec) and
// per-block facts (bBlock); Stream.Unit then runs:
//
//  1. analysis (baseline_analysis.go): label resolution, reverse postorder,
//     dominators and the dominance check, loop depth, liveness of the values
//     used outside their defining block, and Var weights;
//  2. allocation (baseline_alloc.go, baseline_lower.go): one forward pass in
//     layout order that keeps values in registers with next-use eviction,
//     Vars in homes or pinned to callee-saved registers, fixes each block's
//     entry state at its first arriving edge and reconciles later edges with
//     parallel moves, and appends physical records (bPhys);
//  3. emission (baseline_emit.go): sizes, label PCs and branch relaxation,
//     the frame, and direct encoding through the target's assembler.
//
// The architecture-specific part is baselineArch (baseline_arch.go).

const (
	noValue = ^uint32(0)
	// farUse marks a value that is live beyond the current block.
	farUse = ^uint32(0) - 1
)

// bOpCopy is the engine's internal copy, which an OpGet becomes once its
// Var's content is known.
const bOpCopy ir.Opcode = 250

// Record flags.
const (
	rfAligned  uint8 = 1 << iota
	rfContract       // FAdd may contract
	rfSymbol         // OpConst: imm indexes symbols
	rfDefFP          // the result belongs in the floating-point bank
	rfEffect         // call: imm indexes effects
)

// bRec is one instruction in the baseline's compact form (32 bytes, no
// pointers). Value IDs are program-wide, so recs[id] is the record of id.
type bRec struct {
	imm     uint64
	a, b, c uint32
	x       uint32 // OpGet/OpSet: Variable; calls: start in args
	n       uint16 // calls: argument count
	op      ir.Opcode
	bits    uint8
	fl      uint8
	ord     uint8
	fpu     uint8 // bit k: operand k is read in the floating-point bank
	_       uint8
}

// bBlock is one segment: its records, terminator and analysis and allocation
// facts.
type bBlock struct {
	first, end         uint32
	argStart, argOther uint32
	nArgs, nOther      uint32
	result, resultHi   uint32
	to, other          int32 // labels while recording, blocks after analysis
	pred               int32
	scopeEnd           int32 // scope ID active at the end
	term               ir.ControlTerm
	parts              bool
	reach              bool
	depth              uint8
	fixed              bool
	deflt              bool // entry state fixed before any edge arrived
	vars               bool // the block reads or writes Vars or label parameters
	idom, rpo          int32
	pre, post          int32
	stateStart, stateN int32
	nparams            int32
	label              int32 // emission label
}

// bPred is a predicate node; value IDs are program-wide.
type bPred struct {
	imm         int64
	value       uint32
	other       uint32
	left, right int32
	kind        ir.PredicateKind
	cond        ir.BranchCondition
	withValue   bool
}

type bVar struct {
	region    int32
	decl      int32
	part      int32 // first part
	initial   [2]uint32
	parts     uint8
	bits      uint8 // width of each part
	immutable bool
}

type bScope struct {
	id               int
	parent           int32 // index
	enter, position  int32
	exportsStart, nx uint32
	block            int32 // block leaving the scope
}

// baselineEngine holds one function's records and the reusable state of the
// baseline tier. Engines are pooled per Target.
type baselineEngine struct {
	target  *resolvedTarget
	options compileOptions
	f       Func
	arch    baselineArch
	m       baselineModel

	recs        []bRec
	vblock      []uint32
	blocks      []bBlock
	args        []uint32
	preds       []bPred
	vars        []bVar
	partVar     []int32
	scopes      []bScope
	scopeIndex  map[int]int32
	scopeStack  []int32
	labelBlock  []int32
	symbols     []string
	effects     []abi.CallEffect
	params      []uint32 // OpParam IDs in order
	paramTypes  []reflect.Type
	predMemo    map[*ir.PredicateExpr]int32
	usedSymbols map[string]bool

	returns         int
	partsReturns    int
	hasVector       bool
	hasGoCall       bool
	hasCall         bool
	hasSyscall      bool
	memRead         bool
	memWrite        bool
	loads           bool
	stores          bool
	branches        bool // a conditional branch was emitted
	hasFunctionCall bool
	fpArith         bool
	literals        bool // the architecture loads FP constants from a literal pool
	archID          uint8

	alloc baselineAllocState
	emit  baselineEmitState
}

// archID values: targets whose hot paths the engine calls directly.
const (
	archOther uint8 = iota
	archARM64
	archAMD64
)

var baselinePools sync.Map // *resolvedTarget -> *sync.Pool

func newBaselineEngine(t *resolvedTarget, f Func) (*baselineEngine, error) {
	if f.Harden {
		return nil, fmt.Errorf("baseline tier: Func.Harden: %w", ErrUnsupported)
	}
	if f.Context == ContextGoManaged {
		return nil, fmt.Errorf("baseline tier: ContextGoManaged: %w", ErrUnsupported)
	}
	arch := baselineArchFor(t.architecture)
	if arch == nil {
		return nil, fmt.Errorf("baseline tier for %s: %w", t.architecture, ErrUnsupported)
	}
	if f.ABI != abi.C && f.ABI != abi.Go {
		return nil, ErrUnsupportedABI
	}
	if f.Stack.Pointer != abi.StackPointerReserved {
		return nil, ErrUnsupportedStackRole
	}
	pool, ok := baselinePools.Load(t)
	if !ok {
		pool, _ = baselinePools.LoadOrStore(t, &sync.Pool{})
	}
	e, _ := pool.(*sync.Pool).Get().(*baselineEngine)
	if e == nil {
		e = &baselineEngine{scopeIndex: map[int]int32{}, predMemo: map[*ir.PredicateExpr]int32{}}
	}
	e.reset()
	e.target, e.f, e.arch = t, f, arch
	e.archID = archOther
	switch arch.(type) {
	case arm64BaselineArch:
		e.archID = archARM64
	case amd64BaselineArch:
		e.archID = archAMD64
	}
	_, e.literals = arch.(baselineLiteralArch)
	e.options = t.options(f)
	e.m = arch.model(e.options)
	e.scopes = append(e.scopes, bScope{parent: -1, enter: -1, position: -1, block: -1})
	e.scopeStack = append(e.scopeStack, 0)
	return e, nil
}

func (e *baselineEngine) reset() {
	e.recs, e.vblock, e.blocks, e.args, e.preds = e.recs[:0], e.vblock[:0], e.blocks[:0], e.args[:0], e.preds[:0]
	e.vars, e.partVar, e.scopes, e.scopeStack = e.vars[:0], e.partVar[:0], e.scopes[:0], e.scopeStack[:0]
	e.labelBlock, e.symbols, e.effects, e.params = e.labelBlock[:0], e.symbols[:0], e.effects[:0], e.params[:0]
	e.paramTypes = nil
	clear(e.scopeIndex)
	clear(e.predMemo)
	e.usedSymbols = nil
	e.returns, e.partsReturns = 0, 0
	e.hasVector, e.hasGoCall, e.hasCall, e.hasSyscall = false, false, false, false
	e.memRead, e.memWrite, e.fpArith = false, false, false
	e.loads, e.stores, e.branches, e.hasFunctionCall = false, false, false, false
}

// recycle returns the engine to its Target's pool. Large engines are dropped
// so one huge function does not pin its buffers.
func (e *baselineEngine) recycle() {
	if e == nil || e.target == nil {
		return
	}
	t := e.target
	if cap(e.recs) > 1<<20 {
		return
	}
	for i := range e.effects {
		e.effects[i] = abi.CallEffect{}
	}
	for i := range e.symbols {
		e.symbols[i] = ""
	}
	e.paramTypes = nil
	e.target = nil
	if pool, ok := baselinePools.Load(t); ok {
		pool.(*sync.Pool).Put(e)
	}
}

func baselineInvalid(index int, op ir.Opcode, format string, args ...any) error {
	return fmt.Errorf("%w: %w", ErrInvalidFunction, &ir.ValidationError{Index: index, Op: op, Rule: fmt.Sprintf(format, args...)})
}

func baselineUnsupported(format string, args ...any) error {
	return fmt.Errorf("baseline tier: "+format+": %w", append(args, ErrUnsupported)...)
}

// opShape describes an opcode for the baseline: its A, B, C operands, the
// floating-point operands, and whether it is pure.
type opShape struct {
	operands uint8 // 1 = A, 2 = A, B, 3 = A, B, C
	fpUse    uint8 // bit k: operand k is read in the floating-point bank
	defFP    bool
	pure     bool
	value    bool
	ok       bool
	call     bool // OpCallC, OpCallGo or OpSyscall
	// simple marks the integer operations with general operands and result
	// that lowerSimple handles.
	simple bool
}

var baselineShapes = func() (s [256]opShape) {
	set := func(n uint8, fp uint8, defFP, pure bool, ops ...ir.Opcode) {
		for _, o := range ops {
			s[o] = opShape{operands: n, fpUse: fp, defFP: defFP, pure: pure, value: o.ProducesValue(), ok: true}
		}
	}
	set(0, 0, false, true, ir.OpParam, ir.OpConst, ir.OpLabelParam)
	set(0, 0, false, false, ir.OpGet, ir.OpScopeEnter, ir.OpScopeLeave, ir.OpCallC, ir.OpCallGo, ir.OpSyscall)
	set(2, 0, false, true, ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpAddScaled,
		ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy,
		ir.OpMulHigh, ir.OpMulHighSigned, ir.OpCarryAdd, ir.OpBorrowSub, ir.OpDiv, ir.OpDivSigned, ir.OpRem, ir.OpRemSigned)
	set(3, 0, false, true, ir.OpSelect, ir.OpAddWithCarry, ir.OpSubWithBorrow)
	set(1, 0, false, true, ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight,
		ir.OpCastUnsigned, ir.OpSignExtend, ir.OpLeadingZeros, ir.OpTrailingZeros)
	set(1, 0, false, false, ir.OpLoad8, ir.OpLoad16, ir.OpLoad32, ir.OpLoad64, ir.OpSet)
	set(2, 0, false, false, ir.OpStore8, ir.OpStore16, ir.OpStore32, ir.OpStore64)
	set(2, 3, true, true, ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv)
	set(3, 7, true, true, ir.OpFMA, ir.OpFProductError)
	set(1, 1, true, true, ir.OpFSqrt, ir.OpFConvert)
	set(1, 0, true, true, ir.OpFFromSigned, ir.OpFFromUnsigned)
	set(1, 1, false, true, ir.OpFToSigned, ir.OpFToUnsigned)
	set(2, 3, false, true, ir.OpFCompare)
	s[bOpCopy] = opShape{operands: 1, pure: true, value: true, ok: true}
	for _, o := range []ir.Opcode{ir.OpCallC, ir.OpCallGo, ir.OpSyscall} {
		s[o].call = true
	}
	for _, o := range []ir.Opcode{ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpMul, ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic} {
		s[o].simple = true
	}
	return
}()

// baselineValue reports which opcodes produce a value.
var baselineValue = func() (v [256]bool) {
	for o := range baselineShapes {
		v[o] = baselineShapes[o].value
	}
	return
}()

// segment converts one segment to records.
func (e *baselineEngine) segment(seg *ir.Segment) error {
	base := len(e.recs)
	if seg.Base != base {
		return baselineInvalid(-1, 0, "segment base %d does not continue at %d", seg.Base, base)
	}
	step := &seg.Step
	if step.IsLabel || step.Term > ir.TermReturn {
		return baselineInvalid(-1, 0, "segment %d does not end with a jump, branch or return", len(e.blocks))
	}
	if step.Start != base || step.End != base+len(seg.Code) {
		return baselineInvalid(-1, 0, "segment %d step covers [%d, %d), not its code [%d, %d)", len(e.blocks), step.Start, step.End, base, base+len(seg.Code))
	}
	b := int32(len(e.blocks))
	if b == 0 {
		e.paramTypes = append([]reflect.Type(nil), seg.ParameterTypes...)
	} else if len(seg.Labels) == 0 {
		return baselineInvalid(base, 0, "segment %d binds no label", b)
	} else if len(seg.ParameterTypes) != 0 {
		return baselineInvalid(-1, 0, "ParameterTypes on segment %d", b)
	}
	for _, l := range seg.Labels {
		if l < 0 || l > 1<<24 {
			return baselineInvalid(-1, 0, "label %d out of range", l)
		}
		for int(l) >= len(e.labelBlock) {
			e.labelBlock = append(e.labelBlock, -1)
		}
		if e.labelBlock[l] >= 0 {
			return baselineInvalid(-1, 0, "label %d bound twice", l)
		}
		e.labelBlock[l] = b
	}
	for i := range seg.Variables {
		if err := e.variable(&seg.Variables[i]); err != nil {
			return err
		}
	}
	e.blocks = append(e.blocks, bBlock{first: uint32(base), end: uint32(base + len(seg.Code)), pred: -1, stateStart: -1, label: b})
	if free := cap(e.recs) - len(e.recs); free < len(seg.Code) {
		e.recs = slices.Grow(e.recs, len(seg.Code))
		e.vblock = slices.Grow(e.vblock, cap(e.recs)-len(e.vblock))
	}
	for i := range seg.Code {
		if err := e.instruction(base+i, &seg.Code[i], b); err != nil {
			return err
		}
	}
	for i := range seg.ScopeExits {
		x := &seg.ScopeExits[i]
		index, ok := e.scopeIndex[x.ID]
		if !ok || e.scopes[index].position != int32(x.Position) || e.scopes[index].enter != int32(x.Enter) {
			return baselineInvalid(-1, 0, "scope exit %d does not match the scope's entry and leave", x.ID)
		}
		s := &e.scopes[index]
		s.exportsStart, s.nx, s.block = uint32(len(e.args)), uint32(len(x.Exports)), b
		for _, v := range x.Exports {
			if v < 0 || v >= len(e.recs) {
				return baselineInvalid(-1, 0, "scope %d exports value %d out of range", x.ID, v)
			}
			e.args = append(e.args, uint32(v))
		}
	}
	return e.terminator(b, step)
}

func (e *baselineEngine) variable(v *ir.Variable) error {
	parts := uint8(1)
	if v.Type.Kind != ir.ScalarKind || v.Type.Lanes != 1 {
		if v.Type.Valid() {
			return baselineUnsupported("vector or mask Var")
		}
		return baselineInvalid(-1, 0, "Var %d has an invalid type", len(e.vars))
	}
	if v.Type.LaneBits == 128 {
		parts = 2
	}
	if len(v.Initial) != int(parts) {
		return baselineInvalid(-1, 0, "Var %d: %d initial values for type %+v", len(e.vars), len(v.Initial), v.Type)
	}
	bv := bVar{region: int32(v.Region), decl: int32(v.Declaration), part: int32(len(e.partVar)), parts: parts, bits: min(v.Type.LaneBits, 64), immutable: v.Immutable}
	for i, x := range v.Initial {
		if x < 0 || x >= v.Declaration {
			return baselineInvalid(-1, 0, "Var %d: initial value %d does not precede its declaration", len(e.vars), x)
		}
		bv.initial[i] = uint32(x)
	}
	for range parts {
		e.partVar = append(e.partVar, int32(len(e.vars)))
	}
	e.vars = append(e.vars, bv)
	return nil
}

func (e *baselineEngine) operand(id, x int, ins *ir.Instruction) (uint32, error) {
	if x < 0 || x >= id {
		return 0, baselineInvalid(id, ins.Op, "uses value %d, which does not precede it", x)
	}
	if !baselineValue[e.recs[x].op] {
		return 0, baselineInvalid(id, ins.Op, "uses non-value %d", x)
	}
	return uint32(x), nil
}

func (e *baselineEngine) instruction(id int, ins *ir.Instruction, b int32) error {
	if ins.Type != (ir.Type{}) && (ins.Type.Kind != ir.ScalarKind || ins.Type.Lanes != 1) || ins.Op >= ir.OpVLoad && ins.Op <= ir.OpVInsertZero || ins.Op == ir.OpVCarrylessMul {
		e.hasVector = true
		return baselineUnsupported("vector operation %v", ins.Op)
	}
	shape := baselineShapes[ins.Op]
	if !shape.ok || ins.Op == bOpCopy {
		return baselineInvalid(id, ins.Op, "unknown opcode %d", ins.Op)
	}
	bits := ins.Bits
	if bits == 0 {
		bits = 64
	}
	if bits != 8 && bits != 16 && bits != 32 && bits != 64 {
		return baselineInvalid(id, ins.Op, "width %d is not 8, 16, 32 or 64", ins.Bits)
	}
	// The record is written in place: copying a stack temporary into the
	// slice stalls store forwarding.
	n := len(e.recs)
	if n >= cap(e.recs) || n >= cap(e.vblock) {
		e.recs = slices.Grow(e.recs, 1)
		e.vblock = slices.Grow(e.vblock[:n], cap(e.recs)-n)
	}
	e.recs, e.vblock = e.recs[:n+1], e.vblock[:n+1]
	r := &e.recs[n]
	r.imm, r.a, r.b, r.c, r.x, r.n = ins.Imm, noValue, noValue, noValue, 0, 0
	r.op, r.bits, r.fl, r.ord, r.fpu = ins.Op, bits, 0, uint8(ins.ByteOrder), shape.fpUse
	e.vblock[n] = uint32(b)
	if ins.Aligned {
		r.fl |= rfAligned
	}
	if ins.Contract {
		r.fl |= rfContract
	}
	if shape.defFP {
		r.fl |= rfDefFP
		e.fpArith = true
	}
	if e.m.wide && bits == 64 && shape.value {
		r.fl |= rfDefFP
	}
	if shape.fpUse != 0 {
		e.fpArith = true
	}
	// Operands precede their user and are values; operand reports why
	// one is not.
	if shape.operands >= 1 {
		x := ins.A
		if uint(x) >= uint(id) || !baselineValue[e.recs[x].op] {
			_, err := e.operand(id, x, ins)
			return err
		}
		r.a = uint32(x)
		if shape.operands >= 2 {
			x := ins.B
			if uint(x) >= uint(id) || !baselineValue[e.recs[x].op] {
				_, err := e.operand(id, x, ins)
				return err
			}
			r.b = uint32(x)
			if shape.operands >= 3 {
				x := ins.C
				if uint(x) >= uint(id) || !baselineValue[e.recs[x].op] {
					_, err := e.operand(id, x, ins)
					return err
				}
				r.c = uint32(x)
			}
		}
	}
	if e.m.banks != nil && (shape.defFP || shape.fpUse != 0) {
		e.m.banks(e, r)
	}
	switch ins.Op {
	case ir.OpParam:
		if b != 0 {
			return baselineInvalid(id, ins.Op, "OpParam outside the entry block")
		}
		e.params = append(e.params, uint32(id))
	case ir.OpConst:
		if ins.Symbol != "" {
			if value, ok := e.f.Constants[ins.Symbol]; ok {
				r.imm = value
				if bits < 64 {
					r.imm &= 1<<bits - 1
				}
				if e.usedSymbols == nil {
					e.usedSymbols = map[string]bool{}
				}
				e.usedSymbols[ins.Symbol] = true
			} else if len(e.f.Constants) != 0 && ins.Symbol != ir.MathPi4Symbol {
				return ErrUnboundSymbol
			} else {
				r.fl |= rfSymbol
				r.imm = uint64(len(e.symbols))
				e.symbols = append(e.symbols, ins.Symbol)
			}
		}
	case ir.OpLabelParam:
		if b == 0 || ins.Imm > 1<<16 {
			return baselineInvalid(id, ins.Op, "label parameter outside a labeled block")
		}
		e.blocks[b].vars = true
	case ir.OpGet, ir.OpSet:
		if ins.Variable < 0 || ins.Variable >= len(e.vars) {
			return baselineInvalid(id, ins.Op, "Var %d out of range", ins.Variable)
		}
		v := &e.vars[ins.Variable]
		if ins.Imm >= uint64(v.parts) {
			return baselineInvalid(id, ins.Op, "part %d of Var %d", ins.Imm, ins.Variable)
		}
		if ins.Op == ir.OpSet && v.immutable {
			return baselineInvalid(id, ins.Op, "Set of immutable Var %d", ins.Variable)
		}
		r.x = uint32(ins.Variable)
		e.blocks[b].vars = true
	case ir.OpScopeEnter:
		s := bScope{id: int(ins.Imm), parent: e.scopeStack[len(e.scopeStack)-1], enter: int32(id), position: -1, block: -1}
		if s.id <= 0 {
			return baselineInvalid(id, ins.Op, "scope ID %d is not positive", s.id)
		}
		if _, dup := e.scopeIndex[s.id]; dup {
			return baselineInvalid(id, ins.Op, "scope %d entered twice", s.id)
		}
		e.scopeIndex[s.id] = int32(len(e.scopes))
		e.scopeStack = append(e.scopeStack, int32(len(e.scopes)))
		e.scopes = append(e.scopes, s)
	case ir.OpScopeLeave:
		top := e.scopeStack[len(e.scopeStack)-1]
		if top == 0 || e.scopes[top].id != int(ins.Imm) {
			return baselineInvalid(id, ins.Op, "leave of scope %d does not close the innermost scope", ins.Imm)
		}
		e.scopes[top].position = int32(id + 1)
		e.scopeStack = e.scopeStack[:len(e.scopeStack)-1]
	case ir.OpCallC, ir.OpCallGo, ir.OpSyscall:
		if len(ins.Args) == 0 {
			return baselineInvalid(id, ins.Op, "call without a target")
		}
		r.x, r.n = uint32(len(e.args)), uint16(len(ins.Args))
		for _, x := range ins.Args {
			v, err := e.operand(id, x, ins)
			if err != nil {
				return err
			}
			e.args = append(e.args, v)
		}
		if ins.Effect != nil {
			r.fl |= rfEffect
			r.imm = uint64(len(e.effects))
			e.effects = append(e.effects, ins.Effect.Clone())
		}
		e.hasCall = true
		e.hasFunctionCall = e.hasFunctionCall || ins.Op != ir.OpSyscall
		e.hasGoCall = e.hasGoCall || ins.Op == ir.OpCallGo
		e.hasSyscall = e.hasSyscall || ins.Op == ir.OpSyscall
		e.memRead, e.memWrite = true, true
	case ir.OpLoad8, ir.OpLoad16, ir.OpLoad32, ir.OpLoad64:
		e.memRead, e.loads = true, true
	case ir.OpStore8, ir.OpStore16, ir.OpStore32, ir.OpStore64:
		e.memWrite, e.stores = true, true
	}
	return nil
}

// terminator records the segment's step.
func (e *baselineEngine) terminator(b int32, step *ir.ControlStep) error {
	blk := &e.blocks[b]
	blk.term = step.Term
	blk.scopeEnd = e.scopeStack[len(e.scopeStack)-1]
	end := int(blk.end)
	value := func(x int, what string) (uint32, error) {
		if x < 0 || x >= end || !baselineShapes[e.recs[x].op].value {
			return 0, baselineInvalid(-1, 0, "segment %d %s %d is not a preceding value", b, what, x)
		}
		return uint32(x), nil
	}
	edge := func(label int, args []int) (int32, uint32, error) {
		if label < 0 {
			return 0, 0, baselineInvalid(-1, 0, "segment %d jumps to label %d", b, label)
		}
		start := uint32(len(e.args))
		for _, a := range args {
			v, err := value(a, "edge argument")
			if err != nil {
				return 0, 0, err
			}
			e.args = append(e.args, v)
		}
		return int32(label), start, nil
	}
	var err error
	switch step.Term {
	case ir.TermJump:
		blk.to, blk.argStart, err = edge(step.To, step.Args)
		blk.nArgs = uint32(len(step.Args))
	case ir.TermBranch:
		if step.Predicate == nil {
			return baselineInvalid(-1, 0, "segment %d branches without a predicate", b)
		}
		if blk.to, blk.argStart, err = edge(step.To, step.Args); err != nil {
			return err
		}
		blk.nArgs = uint32(len(step.Args))
		if blk.other, blk.argOther, err = edge(step.Other, step.OtherArgs); err != nil {
			return err
		}
		blk.nOther = uint32(len(step.OtherArgs))
		for _, x := range step.Values {
			if _, err := value(x, "branch value"); err != nil {
				return err
			}
		}
		blk.pred, err = e.predicate(step.Predicate, end)
	case ir.TermReturn:
		if blk.result, err = value(step.Result, "result"); err != nil {
			return err
		}
		e.returns++
		switch len(step.ResultParts) {
		case 0:
		case 2:
			if step.ResultParts[0] != step.Result {
				return baselineInvalid(-1, 0, "segment %d ResultParts[0] is not Result", b)
			}
			if blk.resultHi, err = value(step.ResultParts[1], "high result"); err != nil {
				return err
			}
			blk.parts = true
			e.partsReturns++
		default:
			return baselineInvalid(-1, 0, "segment %d returns %d result parts", b, len(step.ResultParts))
		}
	}
	return err
}

// predicate copies a predicate DAG into preds, preserving sharing.
func (e *baselineEngine) predicate(root *ir.PredicateExpr, end int) (int32, error) {
	if root.Kind == ir.PredicateCompare && root.Left == nil && root.Right == nil {
		return e.predicateNode(root, end, -1, -1)
	}
	clear(e.predMemo)
	type frame struct {
		n    *ir.PredicateExpr
		done bool
	}
	work := []frame{{n: root}}
	for len(work) != 0 {
		f := work[len(work)-1]
		work = work[:len(work)-1]
		if f.n == nil {
			return 0, baselineInvalid(-1, 0, "predicate has a missing operand")
		}
		if !f.done {
			if _, ok := e.predMemo[f.n]; ok {
				continue
			}
			if f.n.Kind == ir.PredicateCompare {
				index, err := e.predicateNode(f.n, end, -1, -1)
				if err != nil {
					return 0, err
				}
				e.predMemo[f.n] = index
				continue
			}
			e.predMemo[f.n] = -2 // visiting
			work = append(work, frame{n: f.n, done: true})
			if f.n.Right != nil {
				work = append(work, frame{n: f.n.Right})
			}
			work = append(work, frame{n: f.n.Left})
			continue
		}
		delete(e.predMemo, f.n)
		left, okLeft := e.predMemo[f.n.Left]
		right, okRight := int32(-1), true
		if f.n.Right != nil {
			right, okRight = e.predMemo[f.n.Right]
		}
		if !okLeft || !okRight || left < 0 || f.n.Right != nil && right < 0 {
			return 0, baselineInvalid(-1, 0, "predicate is cyclic")
		}
		index, err := e.predicateNode(f.n, end, left, right)
		if err != nil {
			return 0, err
		}
		e.predMemo[f.n] = index
	}
	return e.predMemo[root], nil
}

func (e *baselineEngine) predicateNode(n *ir.PredicateExpr, end int, left, right int32) (int32, error) {
	p := bPred{kind: n.Kind, cond: n.Condition, imm: n.Immediate, withValue: n.WithValue, left: left, right: right, value: noValue, other: noValue}
	switch n.Kind {
	case ir.PredicateCompare:
		if n.Condition > ir.BranchAbove || n.Value < 0 || n.Value >= end || n.WithValue && (n.Other < 0 || n.Other >= end) {
			return 0, baselineInvalid(-1, 0, "predicate comparison operands out of range")
		}
		p.value = uint32(n.Value)
		if n.WithValue {
			p.other = uint32(n.Other)
		}
	case ir.PredicateAnd, ir.PredicateOr:
		if left < 0 || right < 0 {
			return 0, baselineInvalid(-1, 0, "predicate conjunction needs two operands")
		}
	case ir.PredicateNot:
		if left < 0 || right >= 0 {
			return 0, baselineInvalid(-1, 0, "predicate negation needs one operand")
		}
	default:
		return 0, baselineInvalid(-1, 0, "unknown predicate kind %d", n.Kind)
	}
	e.preds = append(e.preds, p)
	return int32(len(e.preds) - 1), nil
}
