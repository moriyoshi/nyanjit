package regalloc

import "github.com/moriyoshi/nyanjit/backend"

// Scope is a run of positions [Start, End) in layout order with its own
// reserved registers: while inside it, the allocator keeps its values out of
// Registers, which the client owns (for example as homes of variables).
// Scopes form a forest: a scope lies within its parent, and siblings do not
// overlap. At a position, the innermost scope covering it decides: its
// Registers are the complete reserved set there, replacing its parent's
// rather than adding to them. Positions outside every scope reserve nothing
// beyond Config.Blocked. Scope boundaries need not fall on block boundaries:
// where the reserved set grows inside a block, the allocator vacates the
// new registers before the position; across an edge, the edge's moves avoid
// both ends' reserved registers.
//
// A position's operand may be Fixed to (or placed by abi in) a register
// reserved there; nothing else of the allocator's is in a reserved register
// during a position's instruction, and the allocator writes a register
// while it is reserved only to place such an operand.
type Scope struct {
	Parent     int // -1 for a root
	Start, End int
	Registers  backend.RegisterSet
}

// scopeMap is each position's innermost scope, or -1. Without scopes, at
// is empty.
type scopeMap struct {
	at   []int
	sets []Pins // per scope, its registers
	// readScopes' buffers
	scopes []Scope
	depth  []int
}

// scope returns position's innermost scope, or -1.
func (m *scopeMap) scope(position int) int {
	if position < 0 || position >= len(m.at) {
		return -1
	}
	return m.at[position]
}

// reserved returns the registers reserved at position, or none for a
// position outside the function.
func (m *scopeMap) reserved(position int) Pins {
	if s := m.scope(position); s >= 0 {
		return m.sets[s]
	}
	return Pins{}
}

// readScopes checks f's scopes and finds each position's innermost one.
func readScopes(f Function, positions int, registers backend.RegisterSet, m *scopeMap) error {
	n := f.Scopes()
	m.sets = resize(m.sets, n)
	if n == 0 {
		m.at = m.at[:0]
		return nil
	}
	m.at = resize(m.at, positions)
	for p := range m.at {
		m.at[p] = -1
	}
	m.scopes, m.depth = resize(m.scopes, n), resize(m.depth, n)
	scopes, depth := m.scopes, m.depth
	for s := range n {
		scopes[s] = f.Scope(s)
		x := scopes[s]
		if x.Start < 0 || x.End <= x.Start || x.End > positions || x.Parent < -1 || x.Parent >= n || x.Parent == s {
			return invalid("scope %d covers [%d, %d) with parent %d", s, x.Start, x.End, x.Parent)
		}
		for r := range x.Registers.All() {
			if !registers.Contains(r) {
				return invalid("scope %d reserves register %d outside the allocatable set", s, r)
			}
			m.sets[s].AddRegister(r)
		}
	}
	maxDepth := 0
	for s := range n {
		d := 0
		for q := scopes[s].Parent; q >= 0; q = scopes[q].Parent {
			if d++; d > n {
				return invalid("scope %d has a parent cycle", s)
			}
			if c := scopes[q]; scopes[s].Start < c.Start || scopes[s].End > c.End {
				return invalid("scope %d lies outside its ancestor %d", s, q)
			}
		}
		depth[s] = d
		maxDepth = max(maxDepth, d)
	}
	// Fill from the outermost scopes in: each position a scope covers must
	// still belong to its parent, or a sibling overlaps it.
	for d := 0; d <= maxDepth; d++ {
		for s, x := range scopes {
			if depth[s] != d {
				continue
			}
			for p := x.Start; p < x.End; p++ {
				if m.at[p] != x.Parent {
					return invalid("scope %d overlaps scope %d at position %d", s, m.at[p], p)
				}
				m.at[p] = s
			}
		}
	}
	return nil
}
