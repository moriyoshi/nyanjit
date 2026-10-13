package ir

import "sort"

// blockGraph is the control-flow graph of a labeled program. Each segment
// step is one block, numbered in step order, so block 0 is the entry. Labels
// that precede the same segment name the same block.
type blockGraph struct {
	steps      []int // control step index of each block's segment
	ends       []int // instruction end of each block, ascending
	labelBlock []int // block of each label, or -1
	succStart  []int // successors of block b are succ[succStart[b]:succStart[b+1]]
	succ       []int
	predStart  []int
	pred       []int
}

// newBlockGraph returns nil for a program without labeled control, which is
// one block. Steps must already be checked to alternate correctly.
func newBlockGraph(c *Control) *blockGraph {
	if c == nil {
		return nil
	}
	g := &blockGraph{labelBlock: make([]int, len(c.Labels))}
	for i := range g.labelBlock {
		g.labelBlock[i] = -1
	}
	var pending []int
	for index, step := range c.Steps {
		if step.IsLabel {
			if step.Label >= 0 && step.Label < len(g.labelBlock) {
				pending = append(pending, step.Label)
			}
			continue
		}
		for _, label := range pending {
			g.labelBlock[label] = len(g.steps)
		}
		pending = pending[:0]
		g.steps = append(g.steps, index)
		g.ends = append(g.ends, step.End)
	}
	n := len(g.steps)
	targets := func(step *ControlStep, visit func(int)) {
		switch step.Term {
		case TermJump:
			visit(step.To)
		case TermBranch:
			visit(step.To)
			visit(step.Other)
		}
	}
	g.succStart = make([]int, n+1)
	g.predStart = make([]int, n+2)
	for b, index := range g.steps {
		targets(&c.Steps[index], func(label int) {
			if label >= 0 && label < len(g.labelBlock) && g.labelBlock[label] >= 0 {
				g.succStart[b+1]++
				g.predStart[g.labelBlock[label]+2]++
			}
		})
	}
	for b := 0; b < n; b++ {
		g.succStart[b+1] += g.succStart[b]
		g.predStart[b+2] += g.predStart[b+1]
	}
	g.succ = make([]int, g.succStart[n])
	g.pred = make([]int, g.predStart[n+1])
	for b, index := range g.steps {
		at := g.succStart[b]
		targets(&c.Steps[index], func(label int) {
			if label >= 0 && label < len(g.labelBlock) && g.labelBlock[label] >= 0 {
				to := g.labelBlock[label]
				g.succ[at] = to
				at++
				g.pred[g.predStart[to+1]] = b
				g.predStart[to+1]++
			}
		})
	}
	return g
}

func (g *blockGraph) successors(b int) []int   { return g.succ[g.succStart[b]:g.succStart[b+1]] }
func (g *blockGraph) predecessors(b int) []int { return g.pred[g.predStart[b]:g.predStart[b+1]] }

// blockOf returns the block containing instruction pos. Empty segments
// contain no instruction.
func (g *blockGraph) blockOf(pos int) int {
	return sort.Search(len(g.ends), func(b int) bool { return g.ends[b] > pos })
}

// dominance answers dominance queries in O(1) through an interval numbering
// of the dominator tree.
type dominance struct {
	graph     *blockGraph
	pre, post []int32 // -1 for blocks unreachable from the entry
}

// newDominance computes dominators with the Lengauer-Tarjan algorithm
// (simple version with path compression, O(E log V)). Every traversal uses
// explicit stacks, so the depth of the graph never reaches the Go stack.
func newDominance(g *blockGraph) *dominance {
	n := len(g.steps)
	d := &dominance{graph: g, pre: make([]int32, n), post: make([]int32, n)}
	if n == 0 {
		return d
	}
	dfnum := make([]int, n)
	for i := range dfnum {
		dfnum[i] = -1
	}
	vertex := make([]int, 0, n)
	parent := make([]int, n)
	type frame struct{ block, next int }
	stack := []frame{{0, 0}}
	dfnum[0] = 0
	vertex = append(vertex, 0)
	for len(stack) != 0 {
		top := &stack[len(stack)-1]
		succ := g.successors(top.block)
		if top.next == len(succ) {
			stack = stack[:len(stack)-1]
			continue
		}
		next := succ[top.next]
		top.next++
		if dfnum[next] < 0 {
			dfnum[next] = len(vertex)
			parent[len(vertex)] = dfnum[top.block]
			vertex = append(vertex, next)
			stack = append(stack, frame{next, 0})
		}
	}
	reached := len(vertex)
	semi := make([]int, reached)
	idom := make([]int, reached)
	ancestor := make([]int, reached)
	label := make([]int, reached)
	bucketHead := make([]int, reached)
	bucketNext := make([]int, reached)
	for i := range reached {
		semi[i], ancestor[i], label[i], bucketHead[i] = i, -1, i, -1
	}
	var path []int
	eval := func(v int) int {
		if ancestor[v] < 0 {
			return v
		}
		path = path[:0]
		for u := v; ancestor[ancestor[u]] >= 0; u = ancestor[u] {
			path = append(path, u)
		}
		for i := len(path) - 1; i >= 0; i-- {
			u := path[i]
			a := ancestor[u]
			if semi[label[a]] < semi[label[u]] {
				label[u] = label[a]
			}
			ancestor[u] = ancestor[a]
		}
		return label[v]
	}
	for w := reached - 1; w > 0; w-- {
		for _, p := range g.predecessors(vertex[w]) {
			if v := dfnum[p]; v >= 0 {
				if u := eval(v); semi[u] < semi[w] {
					semi[w] = semi[u]
				}
			}
		}
		bucketNext[w], bucketHead[semi[w]] = bucketHead[semi[w]], w
		p := parent[w]
		ancestor[w] = p
		for v := bucketHead[p]; v >= 0; v = bucketNext[v] {
			if u := eval(v); semi[u] < semi[v] {
				idom[v] = u
			} else {
				idom[v] = p
			}
		}
		bucketHead[p] = -1
	}
	for w := 1; w < reached; w++ {
		if idom[w] != semi[w] {
			idom[w] = idom[idom[w]]
		}
	}
	// Number the dominator tree in preorder and postorder.
	childStart := make([]int, reached+1)
	for w := 1; w < reached; w++ {
		childStart[idom[w]+1]++
	}
	for i := range reached {
		childStart[i+1] += childStart[i]
	}
	children := make([]int, reached)
	fill := append([]int(nil), childStart[:reached]...)
	for w := 1; w < reached; w++ {
		children[fill[idom[w]]] = w
		fill[idom[w]]++
	}
	for i := range d.pre {
		d.pre[i], d.post[i] = -1, -1
	}
	var counter int32
	stack = append(stack[:0], frame{0, childStart[0]})
	d.pre[vertex[0]] = counter
	counter++
	for len(stack) != 0 {
		top := &stack[len(stack)-1]
		if top.next == childStart[top.block+1] {
			d.post[vertex[top.block]] = counter
			counter++
			stack = stack[:len(stack)-1]
			continue
		}
		child := children[top.next]
		top.next++
		d.pre[vertex[child]] = counter
		counter++
		stack = append(stack, frame{child, childStart[child]})
	}
	return d
}

// reachable reports whether block b is reachable from the entry.
func (d *dominance) reachable(b int) bool { return d.pre[b] >= 0 }

// blockDominates reports whether every path from the entry to block b passes
// through block a. Unreachable blocks are dominated by every block.
func (d *dominance) blockDominates(a, b int) bool {
	switch {
	case a == b || d.pre[b] < 0:
		return true
	case d.pre[a] < 0:
		return false
	}
	return d.pre[a] <= d.pre[b] && d.post[b] <= d.post[a]
}

// dominates reports whether instruction a dominates instruction b.
func (d *dominance) dominates(a, b int) bool {
	if d == nil {
		return a <= b
	}
	ba, bb := d.graph.blockOf(a), d.graph.blockOf(b)
	if ba == bb {
		return a <= b
	}
	return d.blockDominates(ba, bb)
}
