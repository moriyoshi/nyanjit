package ir

import (
	"sort"
	"sync"
	"sync/atomic"
)

// facts holds architecture-independent analyses of one valid program.
type facts struct {
	Regions                    []int
	Loop                       bool
	Scopes                     []int
	Parents                    map[int]int
	Bounds                     map[int][2]int
	Local                      map[int][]int
	VariableUses, VariableSets [][]int
	Uses                       [][]int
	Last, ParamIndex           []int
	Floating                   []bool
	LabelFloating              [][]bool
	EntryParamBits             []uint8
	// dom is the dominator tree, computed by the first Analysis.Dominates
	// call. It lives here rather than in Analysis because the facts
	// allocation has room for it, and every SealedProgram embeds an
	// Analysis.
	dom atomic.Pointer[dominance]
}

func (f *facts) used(variable, scope int, modified bool) bool {
	positions := f.VariableUses[variable]
	if modified {
		positions = f.VariableSets[variable]
	}
	bounds := f.Bounds[scope]
	index := sort.SearchInts(positions, bounds[0])
	return index < len(positions) && positions[index] < bounds[1]
}

// scopeIndex maps source scope IDs to compact table ordinals. Builder IDs are
// dense; hand-written sparse IDs use the sorted immutable scope list.
func (f *facts) scopeIndex(id int) int {
	if id >= 0 && id < len(f.Scopes) && f.Scopes[id] == id {
		return id
	}
	return sort.SearchInts(f.Scopes, id)
}

// scopeRegions returns the highest-priority active scope at every position,
// including the end boundary. Later starts win; equal starts retain the
// caller's order. Expired intervals below the stack top can wait: no earlier
// start can outrank the top. Each interval is pushed and popped once, even for
// overlapping intervals.
func scopeRegions(n, count int, bounds func(int) (id, start, end int)) []int {
	if count == 0 {
		return make([]int, n+1)
	}
	heads := make([]int, n+1)
	next := make([]int, count)
	for i := count - 1; i >= 0; i-- {
		_, start, end := bounds(i)
		if start < 0 || start > n || start >= end {
			continue
		}
		next[i] = heads[start]
		heads[start] = i + 1
	}
	type active struct{ id, end int }
	stack := make([]active, 0, count)
	regions := make([]int, n+1)
	for pos := range regions {
		for len(stack) > 0 && stack[len(stack)-1].end <= pos {
			stack = stack[:len(stack)-1]
		}
		for event := heads[pos]; event != 0; event = next[event-1] {
			id, _, end := bounds(event - 1)
			stack = append(stack, active{id, end})
		}
		if len(stack) > 0 {
			regions[pos] = stack[len(stack)-1].id
		}
	}
	return regions
}

// Analysis holds a program's architecture-independent analyses: scope
// regions and bounds, variable locality and uses, value uses, loops and
// floating-point homes. It is immutable and safe for concurrent use.
//
// Positions are instruction indices; position len(Instructions) is the end of
// the program. Scope IDs are those of ScopeExit, with zero for the root.
// Methods returning slices return storage borrowed from the Analysis: read
// it, but never modify it or keep it past the program's lifetime. All
// queries cost O(1) after the analysis is computed, except where noted.
type Analysis struct {
	program *Program
	base    *facts // computed eagerly
	once    sync.Once
	full    *facts // base plus use analyses for programs without variables
}

func newAnalysis(p *Program, f *facts) *Analysis { return &Analysis{program: p, base: f} }

// Analyze computes the analyses of p without validating it. p must be valid
// (see Program.Validate) and must not change while the Analysis is in use.
// Analyze returns a *ValidationError when it finds p malformed, but it does not
// check everything Validate does: queries on an invalid program may panic.
// Sealed programs carry their analysis: use SealedProgram.Analysis instead.
func Analyze(p *Program) (a *Analysis, err error) {
	if p == nil {
		return nil, invalidRule("nil program")
	}
	defer func() {
		if recover() != nil {
			a, err = nil, invalidRule("program is malformed; validate it before analysis")
		}
	}()
	return newAnalysis(p, analyzeProgram(p, false)), nil
}

// complete returns analyses that include value uses. Programs without
// variables compute them on first use.
func (a *Analysis) complete() *facts {
	if f := a.base; f.Last != nil {
		return f
	}
	return a.completeUses()
}

func (a *Analysis) completeUses() *facts {
	a.once.Do(func() { a.full = analyzeProgram(a.program, true) })
	return a.full
}

// HasLoop reports whether control flow can repeat a block.
func (a *Analysis) HasLoop() bool { return a.base.Loop }

// EntryParamBits returns each entry parameter's scalar width, with zero for
// a vector parameter. It is nil when the parameters do not match Arity.
func (a *Analysis) EntryParamBits() []uint8 { return a.base.EntryParamBits }

// LabelFloating returns, for each parameter of label, whether it carries a
// floating-point value. It is nil for a label without such parameters.
func (a *Analysis) LabelFloating(label int) []bool {
	if label < 0 || label >= len(a.base.LabelFloating) {
		return nil
	}
	return a.base.LabelFloating[label]
}

// IsFloating reports whether variable holds floating-point values.
func (a *Analysis) IsFloating(variable int) bool {
	f := a.complete()
	return variable >= 0 && variable < len(f.Floating) && f.Floating[variable]
}

// FloatingVariables returns, per variable, whether it holds floating-point
// values (borrowed; the same data as IsFloating).
func (a *Analysis) FloatingVariables() []bool { return a.complete().Floating }

// ScopeBounds returns every scope's [start, end) positions, keyed by scope
// ID (borrowed; the same data as Bounds). Never modify the map.
func (a *Analysis) ScopeBounds() map[int][2]int { return a.complete().Bounds }

// ScopeAt returns the innermost scope active at pos.
func (a *Analysis) ScopeAt(pos int) int { return a.complete().Regions[pos] }

// Scopes returns every scope ID, including the root, in ascending order.
func (a *Analysis) Scopes() []int { return a.complete().Scopes }

// ScopeIndex returns scope's ordinal in Scopes. It costs O(1) for dense IDs
// and O(log scopes) otherwise.
func (a *Analysis) ScopeIndex(scope int) int {
	f := a.complete()
	if scope >= 0 && scope < len(f.Scopes) && f.Scopes[scope] == scope {
		return scope
	}
	return sort.SearchInts(f.Scopes, scope)
}

// Parent returns scope's enclosing scope; the root's parent is -1. ok is
// false for an unknown scope.
func (a *Analysis) Parent(scope int) (parent int, ok bool) {
	parent, ok = a.complete().Parents[scope]
	return parent, ok
}

// Bounds returns the positions [start, end) a scope covers.
func (a *Analysis) Bounds(scope int) (start, end int) {
	b := a.complete().Bounds[scope]
	return b[0], b[1]
}

// Locals returns the variables declared in scope.
func (a *Analysis) Locals(scope int) []int { return a.complete().Local[scope] }

// VarUses returns the positions that read or write variable, ascending.
func (a *Analysis) VarUses(variable int) []int { return a.complete().VariableUses[variable] }

// VarSets returns the positions that assign variable after its
// initialization, ascending.
func (a *Analysis) VarSets(variable int) []int { return a.complete().VariableSets[variable] }

// VariableUsed reports whether variable is read or written (or, with
// modified, assigned) inside scope. It costs O(log uses).
func (a *Analysis) VariableUsed(variable, scope int, modified bool) bool {
	return a.complete().used(variable, scope, modified)
}

// Uses returns the positions that use value as an operand, ascending.
// Control-step operands count at their step's end position.
func (a *Analysis) Uses(value int) []int { return a.complete().Uses[value] }

// LastUse returns the last position that uses value, or value itself when
// nothing uses it.
func (a *Analysis) LastUse(value int) int { return a.complete().Last[value] }

// ParamIndex returns the entry-parameter index of value, or -1 when value is
// not an OpParam.
func (a *Analysis) ParamIndex(value int) int { return a.complete().ParamIndex[value] }

// Dominates reports whether instruction x dominates instruction y: every path
// from the entry to y passes through x. An instruction dominates itself and
// the later instructions of its block, and blocks unreachable from the entry
// are dominated by every instruction. The first call computes the dominator
// tree in O(E log V) over the program's blocks; later calls cost
// O(log blocks).
func (a *Analysis) Dominates(x, y int) bool {
	d := a.base.dom.Load()
	if d == nil && a.program.Control != nil {
		d = newDominance(newBlockGraph(a.program.Control))
		if !a.base.dom.CompareAndSwap(nil, d) {
			d = a.base.dom.Load()
		}
	}
	return d.dominates(x, y)
}
