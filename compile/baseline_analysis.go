package compile

import (
	"slices"

	"github.com/moriyoshi/nyanjit/ir"
)

// analyze resolves labels and computes the facts allocation needs: reachable
// blocks, dominance (checked for every cross-block use), loop depth, the
// liveness of values used outside their defining block, and Var weights.
func (e *baselineEngine) analyze() error {
	if len(e.blocks) == 0 {
		return baselineInvalid(-1, 0, "function has no segments")
	}
	if len(e.scopeStack) != 1 {
		return baselineInvalid(-1, 0, "scope entered without a matching leave")
	}
	if e.partsReturns != 0 && e.partsReturns != e.returns {
		return baselineInvalid(-1, 0, "some returns are 128-bit and others are not")
	}
	a := &e.alloc
	n := len(e.blocks)
	// Labels to blocks, and each block's parameter count.
	for b := range e.blocks {
		blk := &e.blocks[b]
		blk.nparams = -1
	}
	resolve := func(b int, label int32, nargs uint32) (int32, error) {
		if int(label) >= len(e.labelBlock) || e.labelBlock[label] < 0 {
			return 0, baselineInvalid(-1, 0, "label %d is unbound", label)
		}
		t := e.labelBlock[label]
		tb := &e.blocks[t]
		if tb.nparams >= 0 && tb.nparams != int32(nargs) {
			return 0, baselineInvalid(-1, 0, "label %d receives %d arguments; another edge passes %d", label, nargs, tb.nparams)
		}
		tb.nparams = int32(nargs)
		return t, nil
	}
	for b := range e.blocks {
		blk := &e.blocks[b]
		var err error
		switch blk.term {
		case ir.TermJump:
			blk.to, err = resolve(b, blk.to, blk.nArgs)
		case ir.TermBranch:
			if blk.to, err = resolve(b, blk.to, blk.nArgs); err == nil {
				blk.other, err = resolve(b, blk.other, blk.nOther)
			}
		}
		if err != nil {
			return err
		}
	}
	if e.blocks[0].nparams > 0 {
		return baselineInvalid(-1, 0, "the entry label takes no parameters")
	}
	e.threadJumps()
	for b := range e.blocks {
		blk := &e.blocks[b]
		if blk.nparams < 0 {
			blk.nparams = 0
		}
		for i := blk.first; i < blk.end; i++ {
			if r := &e.recs[i]; r.op == ir.OpLabelParam && r.imm >= uint64(blk.nparams) {
				if blk.nparams == 0 {
					return baselineInvalid(int(i), r.op, "a block with parameters needs an incoming edge")
				}
				return baselineInvalid(int(i), r.op, "label parameter %d of %d", r.imm, blk.nparams)
			}
		}
	}
	// Reverse postorder from the entry, and predecessors.
	a.rpo = a.rpo[:0]
	a.order = growInt32(a.order, n)
	for b := range e.blocks {
		e.blocks[b].rpo, e.blocks[b].reach = -1, false
	}
	a.stack = append(a.stack[:0], 0, 0)
	e.blocks[0].reach = true
	for len(a.stack) != 0 {
		top := len(a.stack) - 2
		b, k := a.stack[top], a.stack[top+1]
		succ, ns := e.successors(int(b))
		if int(k) < ns {
			a.stack[top+1]++
			s := succ[k]
			if !e.blocks[s].reach {
				e.blocks[s].reach = true
				a.stack = append(a.stack, s, 0)
			}
			continue
		}
		a.stack = a.stack[:top]
		a.rpo = append(a.rpo, b)
	}
	slices.Reverse(a.rpo)
	for i, b := range a.rpo {
		e.blocks[b].rpo = int32(i)
	}
	// Predecessor lists (reachable sources only).
	a.predStart = growInt32(a.predStart, n+1)
	clear(a.predStart)
	for b := range e.blocks {
		if !e.blocks[b].reach {
			continue
		}
		succ, ns := e.successors(b)
		for _, s := range succ[:ns] {
			a.predStart[s+1]++
		}
	}
	for b := 0; b < n; b++ {
		a.predStart[b+1] += a.predStart[b]
	}
	a.preds = growInt32(a.preds, int(a.predStart[n]))
	copy(a.order, a.predStart[:n])
	for b := range e.blocks {
		if !e.blocks[b].reach {
			continue
		}
		succ, ns := e.successors(b)
		for _, s := range succ[:ns] {
			a.preds[a.order[s]] = int32(b)
			a.order[s]++
		}
	}
	if a.predStart[1] != 0 && e.hasEntryParams() {
		return baselineUnsupported("an edge to the entry block of a function with parameters")
	}
	// Dominators (Cooper, Harvey and Kennedy) over reverse postorder.
	for b := range e.blocks {
		e.blocks[b].idom = -1
	}
	e.blocks[0].idom = 0
	for changed := true; changed; {
		changed = false
		for _, b := range a.rpo[1:] {
			idom := int32(-1)
			for _, p := range a.preds[a.predStart[b]:a.predStart[b+1]] {
				if e.blocks[p].idom < 0 {
					continue
				}
				if idom < 0 {
					idom = p
					continue
				}
				x, y := p, idom
				for x != y {
					for e.blocks[x].rpo > e.blocks[y].rpo {
						x = e.blocks[x].idom
					}
					for e.blocks[y].rpo > e.blocks[x].rpo {
						y = e.blocks[y].idom
					}
				}
				idom = x
			}
			if e.blocks[b].idom != idom {
				e.blocks[b].idom, changed = idom, true
			}
		}
	}
	// Dominator tree intervals for constant-time queries.
	a.order = growInt32(a.order, n+1)
	clear(a.order)
	for _, b := range a.rpo[1:] {
		a.order[e.blocks[b].idom+1]++
	}
	for b := 0; b < n; b++ {
		a.order[b+1] += a.order[b]
	}
	a.children = growInt32(a.children, len(a.rpo))
	a.cursor = append(a.cursor[:0], a.order[:n]...)
	for _, b := range a.rpo[1:] {
		p := e.blocks[b].idom
		a.children[a.cursor[p]] = b
		a.cursor[p]++
	}
	clock := int32(0)
	a.stack = append(a.stack[:0], 0, 0)
	e.blocks[0].pre = clock
	clock++
	for len(a.stack) != 0 {
		top := len(a.stack) - 2
		b, k := a.stack[top], a.stack[top+1]
		kids := a.children[a.order[b]:a.order[b+1]]
		if int(k) < len(kids) {
			a.stack[top+1]++
			c := kids[k]
			e.blocks[c].pre = clock
			clock++
			a.stack = append(a.stack, c, 0)
			continue
		}
		e.blocks[b].post = clock
		clock++
		a.stack = a.stack[:top]
	}
	return e.analyzeUses()
}

func (e *baselineEngine) hasEntryParams() bool { return len(e.params) != 0 }

// threadJumps retargets edges past blocks that only jump on: a two-way
// branch written as Branch followed by Jump then needs one conditional
// branch. Blocks left without predecessors become unreachable.
func (e *baselineEngine) threadJumps() {
	through := func(t int32) int32 {
		for hops := 0; hops < 8; hops++ {
			tb := &e.blocks[t]
			if tb.first != tb.end || tb.term != ir.TermJump || tb.nparams != 0 || tb.nArgs != 0 || tb.to == t || t == 0 {
				return t
			}
			t = tb.to
		}
		return t
	}
	for b := range e.blocks {
		blk := &e.blocks[b]
		switch blk.term {
		case ir.TermJump:
			if blk.nArgs == 0 {
				blk.to = through(blk.to)
			}
		case ir.TermBranch:
			if blk.nArgs == 0 {
				blk.to = through(blk.to)
			}
			if blk.nOther == 0 {
				blk.other = through(blk.other)
			}
		}
	}
}

func (e *baselineEngine) successors(b int) ([2]int32, int) {
	blk := &e.blocks[b]
	switch blk.term {
	case ir.TermJump:
		return [2]int32{blk.to}, 1
	case ir.TermBranch:
		if blk.to == blk.other {
			return [2]int32{blk.to}, 1
		}
		return [2]int32{blk.to, blk.other}, 2
	}
	return [2]int32{}, 0
}

func (e *baselineEngine) dominates(d, u int32) bool {
	bd, bu := &e.blocks[d], &e.blocks[u]
	return bd.pre <= bu.pre && bu.post <= bd.post
}

// analyzeUses checks dominance, numbers the cross-block values, computes
// their liveness, loop depth and Var weights.
func (e *baselineEngine) analyzeUses() error {
	a := &e.alloc
	nrec := len(e.recs)
	a.xid = growInt32(a.xid, nrec)
	for i := range a.xid {
		a.xid[i] = -1
	}
	a.cross = a.cross[:0]
	a.crossUses = a.crossUses[:0]
	use := func(v uint32, u int32, at int) error {
		if v >= uint32(nrec) {
			return baselineInvalid(at, 0, "uses value %d beyond the function", v)
		}
		d := int32(e.vblock[v])
		if d == u {
			return nil
		}
		if !e.blocks[d].reach || !e.dominates(d, u) {
			op := ir.Opcode(0)
			if at >= 0 {
				op = e.recs[at].op
			}
			return baselineInvalid(at, op, "value %d is not dominated by its definition", v)
		}
		if a.xid[v] < 0 {
			a.xid[v] = int32(len(a.cross))
			a.cross = append(a.cross, v)
		}
		a.crossUses = append(a.crossUses, uint64(u)<<32|uint64(a.xid[v]))
		return nil
	}
	for _, ub := range a.rpo {
		u := ub
		blk := &e.blocks[u]
		// Operands precede their users and blocks are contiguous, so an
		// operand at or after the block's first record is defined in it.
		first := blk.first
		// Every operand of the entry block is its own (operand IDs precede
		// their users); only a capture's initial value needs a check.
		start := blk.first
		if first == 0 && !blk.vars {
			start = blk.end
		}
		for i := start; i < blk.end; i++ {
			r := &e.recs[i]
			for _, v := range [3]uint32{r.a, r.b, r.c} {
				if v < first {
					if err := use(v, u, int(i)); err != nil {
						return err
					}
				}
			}
			switch r.op {
			case ir.OpCallC, ir.OpCallGo, ir.OpSyscall:
				for _, v := range e.args[r.x : r.x+uint32(r.n)] {
					if err := use(v, u, int(i)); err != nil {
						return err
					}
				}
			case ir.OpGet:
				v := &e.vars[r.x]
				if v.immutable {
					if v.initial[r.imm] >= uint32(nrec) || !baselineShapes[e.recs[v.initial[r.imm]].op].value {
						return baselineInvalid(int(i), r.op, "capture's initial value is not a value")
					}
					if err := use(v.initial[r.imm], u, int(i)); err != nil {
						return err
					}
				}
			}
		}
		end := -1
		for _, v := range e.args[blk.argStart : blk.argStart+blk.nArgs] {
			if err := use(v, u, end); err != nil {
				return err
			}
		}
		if blk.term == ir.TermBranch {
			for _, v := range e.args[blk.argOther : blk.argOther+blk.nOther] {
				if err := use(v, u, end); err != nil {
					return err
				}
			}
			if err := e.predicateUses(blk.pred, func(v uint32) error { return use(v, u, end) }); err != nil {
				return err
			}
		}
		if blk.term == ir.TermReturn {
			if err := use(blk.result, u, end); err != nil {
				return err
			}
			if blk.parts {
				if err := use(blk.resultHi, u, end); err != nil {
					return err
				}
			}
		}
	}
	for _, s := range e.scopes[1:] {
		if s.block >= 0 && e.blocks[s.block].reach {
			for _, v := range e.args[s.exportsStart : s.exportsStart+s.nx] {
				d := int32(e.vblock[v])
				if d != s.block && (!e.blocks[d].reach || !e.dominates(d, s.block)) {
					return baselineInvalid(-1, 0, "scope %d export %d is not dominated by its definition", s.id, v)
				}
			}
		}
	}
	for v := range e.vars {
		bv := &e.vars[v]
		if !bv.immutable {
			for k := uint8(0); k < bv.parts; k++ {
				at := int(bv.decl) + int(k)
				if at >= nrec || e.recs[at].op != ir.OpSet || e.recs[at].x != uint32(v) || e.recs[at].imm != uint64(k) || e.recs[at].a != bv.initial[k] {
					return baselineInvalid(at, 0, "declaration of Var %d must Set part %d to its initial value", v, k)
				}
			}
		}
	}
	// Liveness of cross-block values, by blocks in postorder until stable.
	n := len(e.blocks)
	x := len(a.cross)
	a.words = (x + 63) / 64
	if a.words != 0 {
		size := n * a.words
		a.liveIn = growUint64(a.liveIn, size)
		a.gen = growUint64(a.gen, size)
		a.kill = growUint64(a.kill, size)
		clear(a.liveIn)
		clear(a.gen)
		clear(a.kill)
		for _, u := range a.crossUses {
			b, id := int(u>>32), int(uint32(u))
			a.gen[b*a.words+id/64] |= 1 << (id % 64)
		}
		for id, v := range a.cross {
			d := int(e.vblock[v])
			a.kill[d*a.words+id/64] |= 1 << (id % 64)
		}
		a.liveOut = growUint64(a.liveOut, a.words)
		for changed := true; changed; {
			changed = false
			for i := len(a.rpo) - 1; i >= 0; i-- {
				b := int(a.rpo[i])
				out := a.liveOut
				clear(out)
				succ, ns := e.successors(b)
				for _, s := range succ[:ns] {
					in := a.liveIn[int(s)*a.words : (int(s)+1)*a.words]
					for w := range out {
						out[w] |= in[w]
					}
				}
				in := a.liveIn[b*a.words : (b+1)*a.words]
				gen, kill := a.gen[b*a.words:(b+1)*a.words], a.kill[b*a.words:(b+1)*a.words]
				for w := range in {
					next := gen[w] | out[w]&^kill[w]
					if next != in[w] {
						in[w], changed = next, true
					}
				}
			}
		}
	}
	// Loop depth: a back edge in layout order marks its header..latch span.
	a.depth = growInt32(a.depth, n+1)
	clear(a.depth)
	for b := range e.blocks {
		if !e.blocks[b].reach {
			continue
		}
		succ, ns := e.successors(b)
		for _, s := range succ[:ns] {
			if int(s) <= b {
				a.depth[s]++
				a.depth[b+1]--
			}
		}
	}
	depth := int32(0)
	for b := range e.blocks {
		depth += a.depth[b]
		e.blocks[b].depth = uint8(min(max(depth, 0), 7))
	}
	return nil
}

func (e *baselineEngine) predicateUses(root int32, use func(uint32) error) error {
	// Shared subtrees are visited once per reference; the predicate budget
	// bounds the expansion.
	work := append(e.alloc.stack[:0], root)
	defer func() { e.alloc.stack = work[:0] }()
	for len(work) != 0 {
		p := &e.preds[work[len(work)-1]]
		work = work[:len(work)-1]
		switch p.kind {
		case ir.PredicateCompare:
			if err := use(p.value); err != nil {
				return err
			}
			if p.withValue {
				if err := use(p.other); err != nil {
					return err
				}
			}
		case ir.PredicateNot:
			work = append(work, p.left)
		default:
			work = append(work, p.right, p.left)
		}
	}
	return nil
}

func growInt32(s []int32, n int) []int32 {
	if cap(s) < n {
		return make([]int32, n, n+n/4)
	}
	return s[:n]
}

func growUint32(s []uint32, n int) []uint32 {
	if cap(s) < n {
		return make([]uint32, n, n+n/4)
	}
	return s[:n]
}

func growUint64(s []uint64, n int) []uint64 {
	if cap(s) < n {
		return make([]uint64, n, n+n/4)
	}
	return s[:n]
}
