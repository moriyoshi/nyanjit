package regalloc

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
)

// FunctionBuilder builds a Function in layout order and implements it from
// slices, for clients without a representation of their own to describe.
// Add values, then blocks and their positions in order: AddBlock starts a
// block at the next position, and the previous block ends there. The zero
// value is empty; Reset empties it and keeps its storage. An Allocator may
// read the builder's storage in place until Finish, so the builder must not
// change between Allocate and Finish.
type FunctionBuilder struct {
	values    []Value
	uses      []int32 // uses and edge arguments by value, up to the last used, for Online
	ops       []Operand
	opStart   []int // position p's operands are ops[opStart[p]:opStart[p+1]]
	positions []builderPosition
	calls     []*abi.CallEffect
	clobbers  []backend.RegisterSet
	blocks    []builderBlock
	params    []int
	edges     []builderEdge
	args      []int
	scopes    []Scope
	layout    abi.EntryLayout
}

// builderPosition is a position's call effect and clobbers, each 1 + an
// index into calls or clobbers or 0 for none, and whether it returns.
// Without pointers, the slice needs no scanning.
type builderPosition struct {
	call, clobber int32
	returns       bool
}

// builderBlock starts at position start; its parameters are
// params[from:to]. Offsets rather than subslices keep earlier blocks valid
// while params grows.
type builderBlock struct{ start, from, to int }

// builderEdge passes args[argFrom:argTo].
type builderEdge struct{ from, to, argFrom, argTo int }

// Reset empties the builder and keeps its storage.
func (b *FunctionBuilder) Reset() {
	b.values, b.uses, b.ops, b.opStart = b.values[:0], b.uses[:0], b.ops[:0], b.opStart[:0]
	clear(b.calls)
	b.positions, b.calls, b.clobbers = b.positions[:0], b.calls[:0], b.clobbers[:0]
	b.blocks, b.params, b.edges, b.args, b.scopes = b.blocks[:0], b.params[:0], b.edges[:0], b.args[:0], b.scopes[:0]
	b.layout = abi.EntryLayout{}
}

// AddValue adds a value and returns it.
func (b *FunctionBuilder) AddValue(v Value) int {
	b.values = append(reserve(b.values, 1), v)
	return len(b.values) - 1
}

// AddBlock starts a block whose parameters are params at the next position
// and returns it. Block 0 is the entry.
func (b *FunctionBuilder) AddBlock(params ...int) int {
	from := len(b.params)
	b.params = append(reserve(b.params, len(params)), params...)
	b.blocks = append(reserve(b.blocks, 1), builderBlock{start: b.Positions(), from: from, to: len(b.params)})
	return len(b.blocks) - 1
}

// AddPosition adds a position with ops and returns it.
func (b *FunctionBuilder) AddPosition(ops ...Operand) int {
	return b.add(nil, false, ops)
}

// AddCall adds a call position with ops: its uses are the arguments and
// its first definition the result, as Function.Call describes.
func (b *FunctionBuilder) AddCall(effect *abi.CallEffect, ops ...Operand) int {
	return b.add(effect, false, ops)
}

// AddReturn adds a returning position whose first use is the result.
func (b *FunctionBuilder) AddReturn(ops ...Operand) int {
	return b.add(nil, true, ops)
}

func (b *FunctionBuilder) add(effect *abi.CallEffect, returns bool, ops []Operand) int {
	if len(b.opStart) == 0 {
		b.opStart = append(b.opStart, 0)
	}
	b.ops = append(reserve(b.ops, len(ops)), ops...)
	for i := range ops {
		if ops[i].Def {
			continue
		}
		if v := ops[i].Value; uint(v) < uint(len(b.uses)) {
			b.uses[v]++
		} else {
			b.use(v)
		}
	}
	b.opStart = append(reserve(b.opStart, 1), len(b.ops))
	x := builderPosition{returns: returns}
	if effect != nil {
		b.calls = append(b.calls, effect)
		x.call = int32(len(b.calls))
	}
	b.positions = append(reserve(b.positions, 1), x)
	return len(b.positions) - 1
}

// use counts a use of value, which may not be added yet.
func (b *FunctionBuilder) use(value int) {
	if value < 0 {
		return
	}
	if value >= len(b.uses) {
		b.uses = append(reserve(b.uses, value+1-len(b.uses)), make([]int32, value+1-len(b.uses))...)
	}
	b.uses[value]++
}

// AddEdge adds an edge from block from to block to passing args to its
// parameters, and returns it.
func (b *FunctionBuilder) AddEdge(from, to int, args ...int) int {
	start := len(b.args)
	b.args = append(reserve(b.args, len(args)), args...)
	for _, a := range args {
		b.use(a)
	}
	b.edges = append(reserve(b.edges, 1), builderEdge{from: from, to: to, argFrom: start, argTo: len(b.args)})
	return len(b.edges) - 1
}

// AddScope adds a scope and returns it; a child's Parent is the index an
// earlier AddScope returned.
func (b *FunctionBuilder) AddScope(s Scope) int {
	b.scopes = append(b.scopes, s)
	return len(b.scopes) - 1
}

// SetClobbers sets the registers an added position's instruction overwrites
// besides its definitions.
func (b *FunctionBuilder) SetClobbers(position int, registers backend.RegisterSet) {
	if i := b.positions[position].clobber; i != 0 {
		b.clobbers[i-1] = registers
		return
	}
	b.clobbers = append(b.clobbers, registers)
	b.positions[position].clobber = int32(len(b.clobbers))
}

// SetLayout sets the entry layout: block 0's parameters and the result.
func (b *FunctionBuilder) SetLayout(layout abi.EntryLayout) { b.layout = layout }

// Values through Layout implement Function.

func (b *FunctionBuilder) Values() int           { return len(b.values) }
func (b *FunctionBuilder) Value(value int) Value { return b.values[value] }
func (b *FunctionBuilder) Positions() int        { return len(b.positions) }

func (b *FunctionBuilder) Operands(position int, dst []Operand) []Operand {
	from, to := b.opStart[position], b.opStart[position+1]
	return b.ops[from:to:to]
}

func (b *FunctionBuilder) Blocks() int { return len(b.blocks) }

func (b *FunctionBuilder) Block(block int) Block {
	r := b.blocks[block]
	end := b.Positions()
	if block+1 < len(b.blocks) {
		end = b.blocks[block+1].start
	}
	return Block{Start: r.start, End: end, Params: b.params[r.from:r.to:r.to]}
}

func (b *FunctionBuilder) Edges() int { return len(b.edges) }

func (b *FunctionBuilder) Edge(edge int) Edge {
	e := b.edges[edge]
	return Edge{From: e.from, To: e.to, Args: b.args[e.argFrom:e.argTo:e.argTo]}
}

func (b *FunctionBuilder) Call(position int) *abi.CallEffect {
	if i := b.positions[position].call; i != 0 {
		return b.calls[i-1]
	}
	return nil
}
func (b *FunctionBuilder) Returns(position int) bool { return b.positions[position].returns }
func (b *FunctionBuilder) Layout() abi.EntryLayout   { return b.layout }
func (b *FunctionBuilder) Scopes() int               { return len(b.scopes) }
func (b *FunctionBuilder) Scope(scope int) Scope     { return b.scopes[scope] }
func (b *FunctionBuilder) Clobbers(position int) backend.RegisterSet {
	if i := b.positions[position].clobber; i != 0 {
		return b.clobbers[i-1]
	}
	return backend.RegisterSet{}
}

// reserve returns s with room for n more elements, growing from a useful
// size rather than through append's smallest steps.
func reserve[T any](s []T, n int) []T {
	if cap(s)-len(s) >= n {
		return s
	}
	grown := make([]T, len(s), max(2*cap(s), len(s)+n, 32))
	copy(grown, s)
	return grown
}
