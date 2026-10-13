package main

import (
	"fmt"
	"slices"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/regalloc"
)

// Function is a compiled program for one target.
type Function struct {
	Code       []byte // machine code; nil for a listing
	Listing    string // assembly text; empty for machine code
	FrameBytes int
}

// slotLimit bounds the frame slots of one function, locals and spills
// together, well inside the 4 KiB stack that execution allows a foreign entry.
const slotLimit = 256

var arithmetic = [...]op.Opcode{Add: op.OpAdd, Sub: op.OpSub, Mul: op.OpMul, And: op.OpAnd, Or: op.OpOr, Xor: op.OpXor}

// Compiler compiles programs with Alloc. It keeps the allocator's storage
// and its own from one program to the next, so a long-lived Compiler
// allocates little beyond the code it returns. It must not be used
// concurrently.
type Compiler struct {
	Alloc regalloc.Allocator
	g     generator
	work  machine.Workspace
}

// Compile translates p into machine instructions for t, allocating registers
// with a, and assembles them. With listing set, it produces assembly text
// instead of machine code, which works for every target on any host.
//
// The bytecode becomes a regalloc.Function: every value pushed on the stack
// is a virtual value, locals become values that block parameters carry
// across jumps, and Get, Set, Dup and Drop only rename values. Pushed
// constants are materialized only where they are used. The emitter walks
// the function as the regalloc.Allocator contract describes, so any
// allocator works: both keep locals in registers across jumps where they
// fit; regalloc.Online fixes each block's entry registers at the first edge
// into it, regalloc.Batch from liveness.
func (c *Compiler) Compile(p *Program, t *target, listing bool) (*Function, error) {
	if _, err := p.check(); err != nil {
		return nil, err
	}
	g, a := &c.g, c.Alloc
	g.reset(p, t, a)
	for r := range t.policy.Registers().All() {
		// The scratch register is reserved for the encoders' own expansions.
		if r != t.policy.ScratchRegister() {
			g.registers = append(g.registers, r)
		}
	}
	if err := g.describe(); err != nil {
		return nil, err
	}
	err := a.Allocate(regalloc.Config{
		// Derived from the policy's set, it names its registers for the abi
		// entry layout and call effects.
		Registers: t.policy.Registers().Derive(g.registers...), SlotLimit: slotLimit,
	}, &g.fn, g)
	if err != nil {
		return nil, fmt.Errorf("%s for %s: %w", p.Name, t.arch, err)
	}
	defer g.alloc.Finish()
	if err := g.emitFunction(); err != nil {
		return nil, fmt.Errorf("%s for %s: %w", p.Name, t.arch, err)
	}
	c.work.Reset()
	stream, labels, bound := g.layOut(a.Slots(), &c.work)
	labels, err = machine.RelaxBranches(stream, labels, bound, t.policy.BranchFits)
	if err != nil {
		return nil, err
	}
	located, err := machine.ResolveBranches(stream, labels, bound, t.policy.BranchFits)
	if err != nil {
		return nil, err
	}
	asm := t.newAssembler(listing)
	for _, l := range located {
		asm.SetPC(l.PC)
		if err := asm.Emit(l.Instruction); err != nil {
			return nil, fmt.Errorf("%s for %s: assemble %+v: %w", p.Name, t.arch, l.Instruction, err)
		}
	}
	code, text, err := asm.Output()
	if err != nil {
		return nil, err
	}
	if !listing && len(code) != stream.PC() {
		return nil, fmt.Errorf("%s for %s: assembled %d bytes, laid out %d", p.Name, t.arch, len(code), stream.PC())
	}
	return &Function{Code: code, Listing: trimListing(text), FrameBytes: g.frame}, nil
}

// positionKind is what a position emits besides the allocator's moves.
type positionKind uint8

const (
	emitNothing positionKind = iota // constants: materialized at their uses
	emitBinary
	emitJz
	emitJmp
	emitFallthrough
	emitRet
)

type generator struct {
	program   *Program
	target    *target
	width     uint8
	registers []backend.Register // allocatable
	alloc     regalloc.Allocator

	// The function description: a synthetic entry block that zeroes the
	// locals beyond the parameters, then one block per bytecode block.
	fn    regalloc.FunctionBuilder
	imm   []uint64 // each Constant value's number
	succ  []int    // per block, its first edge: a Jz block's taken edge, then its fallthrough
	preds []int    // predecessor count per block
	kinds []positionKind
	arith []op.Opcode

	body      []item
	stubs     []stub   // edges that need a block of their own
	stubMoves []item   // the stubs' moves
	atStart   [][]item // moves of edges that start their destination, by block
	captured  []item   // the moves of the edge being captured
	sink      *[]item  // where emit puts instructions while an edge is resolved
	frame     int

	// Buffers kept from one program to the next.
	leader        []bool
	blockOf       []int
	words, locals []int
	zeros         []regalloc.Operand
	stack         []int
	labels        []int
	bound         []bool
}

// stub is the block an edge's moves need, stubMoves[from:end]: then it jumps
// to its destination.
type stub struct{ to, from, end int }

// reset starts compiling p for t with a, keeping the buffers.
func (g *generator) reset(p *Program, t *target, a regalloc.Allocator) {
	g.program, g.target, g.width, g.alloc = p, t, t.policy.RegisterBits(), a
	g.registers = g.registers[:0]
	g.fn.Reset()
	g.imm, g.kinds, g.arith = g.imm[:0], g.kinds[:0], g.arith[:0]
	g.body, g.stubs, g.stubMoves = g.body[:0], g.stubs[:0], g.stubMoves[:0]
	g.sink, g.frame = nil, 0
}

// resize returns s with length n, zeroed, reusing its storage.
func resize[T any](s []T, n int) []T {
	if cap(s) < n {
		return make([]T, n)
	}
	s = s[:n]
	clear(s)
	return s
}

// item is a machine instruction, or the binding of a label when label >= 0.
// Labels are block indices, then one per stub.
type item struct {
	ins   machine.Instruction
	label int
}

func (g *generator) value(constant bool, imm uint64) int {
	g.imm = append(g.imm, imm)
	return g.fn.AddValue(regalloc.Value{Constant: constant})
}

func (g *generator) position(kind positionKind, ops ...regalloc.Operand) int {
	g.kinds = append(g.kinds, kind)
	g.arith = append(g.arith, 0)
	if kind == emitRet {
		return g.fn.AddReturn(ops...)
	}
	return g.fn.AddPosition(ops...)
}

// edge adds an edge from from to to; a block's edges are consecutive.
func (g *generator) edge(from, to int, args []int) {
	if e := g.fn.AddEdge(from, to, args...); g.succ[from] < 0 {
		g.succ[from] = e
	}
	g.preds[to]++
}

// describe turns the bytecode into a regalloc.Function.
func (g *generator) describe() error {
	p, t := g.program, g.target
	code := p.Code
	g.leader = resize(g.leader, len(code)+1)
	leader := g.leader
	leader[0] = true
	for pc, ins := range code {
		switch ins.Op {
		case Jz, Jmp:
			leader[ins.Arg] = true
			leader[pc+1] = true
		case Ret:
			leader[pc+1] = true
		}
	}
	g.blockOf = resize(g.blockOf, len(code)+1)
	blockOf := g.blockOf
	blocks := 1
	for pc := range code {
		if leader[pc] {
			blockOf[pc] = blocks
			blocks++
		}
	}
	g.succ, g.preds = resize(g.succ, blocks), resize(g.preds, blocks)
	for b := range g.succ {
		g.succ[b] = -1
	}
	// Each instruction adds at most one value and one position.
	g.imm = slices.Grow(g.imm, len(code)+blocks*p.Locals+p.Locals)
	g.kinds, g.arith = slices.Grow(g.kinds, len(code)+blocks), slices.Grow(g.arith, len(code)+blocks)

	// The entry block: parameters in their ABI places, zeroed locals.
	g.words = resize(g.words, p.Params)
	words := g.words
	for i := range words {
		words[i] = 1
	}
	layout, err := abi.ClassifyEntry(t.arch, abi.C, words, abi.MaxArgumentsFor(t.arch), func(i int) string {
		r, _ := t.policy.ParamRegister(i)
		return t.policy.RegisterName(r)
	}, t.policy.RegisterName(t.policy.ResultRegister()))
	if err != nil {
		return err
	}
	layout.Result.Words = layout.Result.Words[:1] // one-word results (EAX on 386)
	g.fn.SetLayout(layout)
	g.locals = resize(g.locals, p.Locals)
	locals, zeros := g.locals, g.zeros[:0]
	for i := range locals {
		if i < p.Params {
			locals[i] = g.value(false, 0)
		} else {
			locals[i] = g.value(true, 0)
			zeros = append(zeros, regalloc.Operand{Value: locals[i], Def: true})
		}
	}
	g.zeros = zeros
	g.fn.AddBlock(locals[:p.Params]...)
	g.position(emitFallthrough, zeros...)
	g.edge(0, 1, locals)

	stack := g.stack[:0]
	defer func() { g.stack = stack }()
	pop := func() int {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		return v
	}
	for pc := 0; pc < len(code); {
		b := blockOf[pc]
		for i := range locals {
			locals[i] = g.value(false, 0)
		}
		g.fn.AddBlock(locals...)
		stack = stack[:0]
		ended := false
		for {
			ins := code[pc]
			switch ins.Op {
			case Push:
				v := g.value(true, uint64(ins.Arg))
				g.position(emitNothing, regalloc.Operand{Value: v, Def: true})
				stack = append(stack, v)
			case Get:
				stack = append(stack, locals[ins.Arg])
			case Set:
				locals[ins.Arg] = pop()
			case Dup:
				stack = append(stack, stack[len(stack)-1])
			case Drop:
				pop()
			case Add, Sub, Mul, And, Or, Xor:
				right, left := pop(), pop()
				result := g.value(false, 0)
				// Two-operand form: the result reuses the left operand's
				// register.
				at := g.position(emitBinary, regalloc.Operand{Value: left}, regalloc.Operand{Value: right},
					regalloc.Operand{Value: result, Def: true, Tied: true, TiedUse: 0})
				g.arith[at] = arithmetic[ins.Op]
				stack = append(stack, result)
			case Jz:
				g.position(emitJz, regalloc.Operand{Value: pop()})
				g.edge(b, blockOf[ins.Arg], locals)
				g.edge(b, blockOf[pc+1], locals)
				ended = true
			case Jmp:
				g.position(emitJmp)
				g.edge(b, blockOf[ins.Arg], locals)
				ended = true
			case Ret:
				g.position(emitRet, regalloc.Operand{Value: pop()})
				ended = true
			}
			if pc++; ended || leader[pc] {
				break
			}
		}
		if !ended {
			g.position(emitFallthrough)
			g.edge(b, blockOf[pc], locals)
		}
	}
	return nil
}

func (g *generator) emit(ins machine.Instruction) {
	if g.sink != nil {
		*g.sink = append(*g.sink, item{ins: ins, label: -1})
		return
	}
	g.body = append(g.body, item{ins: ins, label: -1})
}
func (g *generator) bind(label int) { g.body = append(g.body, item{label: label}) }

// Move, Spill, Reload, LoadIncoming and Rematerialize implement
// regalloc.Mover; every value is one word.
func (g *generator) Move(value int, to, from regalloc.Group) {
	g.emit(machine.Instruction{Op: machine.OpMove, A: to.Registers[0], B: from.Registers[0]})
}

func (g *generator) Spill(value int, slot int, from regalloc.Group) {
	g.emit(machine.Instruction{Op: machine.OpSpill, A: from.Registers[0], Slot: slot})
}

func (g *generator) Reload(value int, to regalloc.Group, slot int) {
	g.emit(machine.Instruction{Op: machine.OpReload, A: to.Registers[0], Slot: slot})
}

func (g *generator) LoadIncoming(value int, to regalloc.Group, offsets [2]int) {
	g.emit(machine.Instruction{Op: machine.OpLoadIncoming, A: to.Registers[0], Imm: uint64(offsets[0])})
}

func (g *generator) Rematerialize(value int, to regalloc.Group) error {
	g.emit(machine.Instruction{Op: machine.OpConst, A: to.Registers[0], Imm: g.imm[value]})
	return nil
}

// capture resolves edge e and returns its moves instead of emitting them.
// The moves are valid until the next capture.
func (g *generator) capture(e int) ([]item, error) {
	g.captured = g.captured[:0]
	g.sink = &g.captured
	err := g.alloc.Edge(e)
	g.sink = nil
	return g.captured, err
}

// emitFunction walks the function, emitting each position's instruction
// between the allocator's moves. Each block's edges are resolved after its
// last position: a fallthrough's or a jump's moves go right there, and a
// taken branch's at the start of a later destination with no other
// predecessor, or in a stub block.
func (g *generator) emitFunction() error {
	if n := g.fn.Blocks(); cap(g.atStart) < n {
		g.atStart = make([][]item, n)
	} else {
		g.atStart = g.atStart[:n]
		for b := range g.atStart {
			g.atStart[b] = g.atStart[b][:0]
		}
	}
	// About an item per instruction: moves make up for the renames.
	g.body = slices.Grow(g.body, len(g.program.Code)+len(g.program.Code)/4+8)
	if err := g.alloc.Entry(); err != nil {
		return err
	}
	for b := range g.fn.Blocks() {
		block := g.fn.Block(b)
		g.bind(b)
		g.body = append(g.body, g.atStart[b]...)
		for p := block.Start; p < block.End; p++ {
			if err := g.alloc.Before(p); err != nil {
				return err
			}
			switch g.kinds[p] {
			case emitBinary:
				result := g.alloc.Operand(p, 2).Registers[0]
				right := g.alloc.Operand(p, 1).Registers[0]
				g.emit(machine.Instruction{Op: machine.OpBinary, Bits: g.width, Arithmetic: g.arith[p], A: result, B: result, C: right})
			case emitJz:
				g.emit(machine.Instruction{Op: machine.OpCompareImmediate, Bits: g.width, A: g.alloc.Operand(p, 0).Registers[0]})
				g.emit(machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchEqual})
				jump := len(g.body) - 1
				if err := g.alloc.After(p); err != nil {
					return err
				}
				taken, next := g.succ[b], g.succ[b]+1
				moves, err := g.capture(taken)
				if err != nil {
					return err
				}
				to := g.fn.Edge(taken).To
				switch {
				case to > b && g.preds[to] == 1:
					g.atStart[to] = append(g.atStart[to], moves...)
				case len(moves) != 0:
					g.stubs = append(g.stubs, stub{to: to, from: len(g.stubMoves), end: len(g.stubMoves) + len(moves)})
					g.stubMoves = append(g.stubMoves, moves...)
					to = g.fn.Blocks() + len(g.stubs) - 1
				}
				g.body[jump].ins.Imm = uint64(to)
				if err := g.alloc.Edge(next); err != nil {
					return err
				}
				continue
			case emitJmp:
				if err := g.alloc.After(p); err != nil {
					return err
				}
				e := g.succ[b]
				if err := g.alloc.Edge(e); err != nil {
					return err
				}
				g.emit(machine.Instruction{Op: machine.OpJump, Imm: uint64(g.fn.Edge(e).To)})
				continue
			case emitFallthrough:
				if err := g.alloc.After(p); err != nil {
					return err
				}
				if err := g.alloc.Edge(g.succ[b]); err != nil {
					return err
				}
				continue
			case emitRet:
				g.emit(machine.Instruction{Op: machine.OpFrameLeave})
				g.emit(machine.Instruction{Op: machine.OpReturn})
			}
			if err := g.alloc.After(p); err != nil {
				return err
			}
		}
	}
	for i, st := range g.stubs {
		g.bind(g.fn.Blocks() + i)
		g.body = append(g.body, g.stubMoves[st.from:st.end]...)
		g.emit(machine.Instruction{Op: machine.OpJump, Imm: uint64(st.to)})
	}
	return nil
}

// layOut wraps the body in a frame now that allocation has fixed its slots
// and records each label's byte PC.
func (g *generator) layOut(slots int, work *machine.Workspace) (*machine.Stream, []int, []bool) {
	g.frame = g.target.frameBytes(slots)
	stream := work.Stream(machine.StreamOptions{Size: g.target.size})
	if g.frame > 0 {
		stream.Append(machine.Instruction{Op: machine.OpFrameEnter, Imm: uint64(g.frame)})
	}
	g.labels, g.bound = resize(g.labels, g.fn.Blocks()+len(g.stubs)), resize(g.bound, g.fn.Blocks()+len(g.stubs))
	labels, bound := g.labels, g.bound
	for _, it := range g.body {
		if it.label >= 0 {
			labels[it.label], bound[it.label] = stream.PC(), true
			continue
		}
		ins := it.ins
		if ins.Op == machine.OpFrameLeave {
			if g.frame == 0 {
				continue
			}
			ins.Imm = uint64(g.frame)
		}
		stream.Append(ins)
	}
	return stream, labels, bound
}
