package compile

import (
	"errors"
	"os"
	"slices"
	"strconv"

	"github.com/moriyoshi/nyanjit/ir"
)

// Var promotion turns Vars into SSA values before cross-block threading.
// A Set defines a new value, a Get is replaced by the value that reaches it,
// and where different values reach a block, the block receives an internal
// label parameter that every incoming edge passes. threadCrossBlockValues
// then carries the reaching values to the blocks that use them, exactly as it
// carries any other dominating value.
//
// The construction is pruned SSA over the sealed block graph. A Var is live
// into a block when some path from the block's start reaches a Get before a
// Set; a live-in block with several reachable predecessors gets a parameter,
// and every other block inherits the value at the end of its only
// predecessor. Trivial parameters, whose arguments are all one value or the
// parameter itself, are replaced by that value, so a Var that a loop does not
// change crosses the loop as an ordinary dominating value. Scopes need no
// special treatment: a Var is declared in its region's entry sequence and the
// region is entered only through its OpScopeEnter, so the declaration
// dominates every access and no parameter is ever needed outside the region.
//
// Blocks that promotion leaves holding only a jump, such as the fallthrough
// of Branch followed by Jump, are folded into their predecessors' edges, and
// a Get that reads a constant from another block becomes a copy of the
// constant, so neither costs edge copies.
//
// A Var keeps its home, and its Gets and Sets lower as before, when:
//   - a Get of it feeds a CallGo target, because callableGoTargets proves the
//     target from the Get and its immutable capture;
//   - its value would be read before its definition in layout, or would
//     reach the entry undefined (neither happens for Builder programs);
//   - the policy says its home is the better code (promotionPolicy): FP
//     state that crosses blocks, loop-carried state for the per-block
//     allocator, state that loops carry without accessing it, and state
//     beyond a loop's or a block's capacity;
//   - its parameters would exceed the budget that bounds the work, as
//     threading's budget does.
//
// Promotion changes code quality, never results or what Lower accepts: Get
// snapshots, 128-bit and vector Vars, captures and scope exports keep their
// meaning, and a promoted program that exceeds a backend limit lowers again
// with homes.

// variablePromotion enables Var promotion. NYANJIT_INTERNAL_VAR_PROMOTION=0
// disables it, to compare against home-based Var lowering; "strict" reports a
// promoted program that fails validation instead of falling back.
var (
	variablePromotion = os.Getenv("NYANJIT_INTERNAL_VAR_PROMOTION") != "0"
	promotionStrict   = os.Getenv("NYANJIT_INTERNAL_VAR_PROMOTION") == "strict"
)

// promotionPolicy says which Vars that cross blocks are promoted. The
// quality corpus decides it (see promotionPolicyFor). A Var that never
// crosses a block boundary, which includes every Var of a straight-line
// program, is always promoted: its Gets and Sets become plain SSA values.
type promotionPolicy struct {
	// loopCarried promotes Vars that are live into a loop header, when the
	// loop accesses them. A Var that is only carried through a loop stays in
	// its home.
	loopCarried bool
	// capacity bounds the promoted Var parts that one loop header carries
	// and the loop changes, per register bank (0 general purpose, 1
	// floating point).
	capacity [2]int
	// blockCapacity bounds the promoted parts live into any block, or 0 for
	// no bound.
	blockCapacity int
	// wordBits is the width of a general register; a part wider than it
	// takes several of capacity's registers (64-bit parts on 386).
	wordBits uint8
}

// promotionPolicyFor returns the policy for lowering with options.
//
// The per-block allocator gives each block's label parameters their own
// registers, so values that cross blocks cost edge copies, and loop-carried
// Vars do better in a home than in label parameters: it promotes Vars that
// cross blocks only one at a time and keeps loop-carried ones in homes.
// The function-wide Online allocator coalesces label parameters, so it
// promotes loop-carried Vars too, until a loop carries more changed Vars
// than the bank has registers. A 32-bit target (386) has too few registers
// for that, and 64-bit Vars take pairs there, so it keeps the per-block
// policy on both allocators. FP state that crosses blocks stays in its home
// on every allocator (see admit).
func promotionPolicyFor(options compileOptions) promotionPolicy {
	t := newMachineTarget(options)
	if t == nil {
		return promotionPolicy{blockCapacity: 1}
	}
	if !onlineAllocation || t.RegisterBits() < 64 {
		return promotionPolicy{blockCapacity: 1}
	}
	return promotionPolicy{loopCarried: true, capacity: [2]int{t.Registers().Len() + len(preservedAllocatable(t)), 0}, wordBits: t.RegisterBits()}
}

// promotionRetry reports whether lowering that failed with err after
// promotion retries without it. Limits depend on the code promotion
// produces; under NYANJIT_INTERNAL_VAR_PROMOTION=strict, other errors are
// reported, so tests see a promoted program that lowering rejects.
func promotionRetry(err error) bool {
	return !promotionStrict || errors.Is(err, ErrBackendLimit) || errors.Is(err, ErrStackRequired)
}

type promotionBlock struct {
	step       int
	start, end int
	succ       [2]int
	succSlot   [2]int // index of this block in each successor's preds
	nsucc      int
	reachable  bool
	preds      []int // distinct predecessors, reachable ones first
	nreachable int   // reachable entries of preds
	phis       []int // promoted parameters added to the block, in order
}

// promotionPhi is a parameter the pass adds to a block for one Var part.
type promotionPhi struct {
	block, key int
	ops        []int // value per entry of the block's preds
	repl       int   // replacement value, or -1
	live       bool
	dead       bool // its Var keeps its home
	newID      int
}

type variablePromoter struct {
	p       *ir.Program
	policy  promotionPolicy
	facts   *programFacts
	n       int // len(p.Instructions); refs >= n name phis
	blocks  []promotionBlock
	blockAt []int // block index of each instruction
	order   []int // reachable blocks in reverse postorder
	rpo     []int // index of each block in order, or -1
	// Loops: headers in reverse postorder, the innermost header of each
	// block (or -1), and each header's enclosing header (or -1).
	headers    []int
	loopOf     []int
	loopParent []int
	// keys: one per promoted Var part.
	keyBase  []int // first key of each Var, or -1 when not promoted
	keyVar   []int
	keyPart  []int
	keyType  []ir.Type
	getRepl  []int // replacement ref of each promoted OpGet, else -1
	phis     []promotionPhi
	phiUsers [][]int
	// per-key scratch, stamped by key
	liveStamp, defStamp, outStamp []int
	phiStamp, phiIndex            []int
	defOut                        []int
}

// promoteVariables returns p with its promotable Vars converted to SSA
// values, or p itself when it promotes none. p is a validated program with
// facts as its analysis; it is not modified.
func promoteVariables(p *ir.Program, facts *programFacts, policy promotionPolicy) *ir.Program {
	if !variablePromotion || len(p.Variables) == 0 || facts == nil {
		return p
	}
	promote := make([]bool, len(p.Variables))
	for v := range promote {
		promote[v] = true
	}
	pinCallGoTargets(p, promote)
	pr := &variablePromoter{p: p, facts: facts, n: len(p.Instructions), policy: policy}
	pr.buildBlocks()
	// The policy keeps Vars in homes as it goes; only a Var whose values
	// would be read before their definition in layout needs another pass.
	for attempt := 0; attempt < 4; attempt++ {
		if !slices.Contains(promote, true) {
			return p
		}
		failed := pr.build(promote)
		if len(failed) == 0 {
			if !slices.ContainsFunc(pr.keyBase, func(k int) bool { return k >= 0 }) {
				return p
			}
			return pr.emit()
		}
		for _, v := range failed {
			promote[v] = false
		}
	}
	return p
}

// pinCallGoTargets keeps the Vars whose Gets callableGoTargets reads to prove
// a CallGo target.
func pinCallGoTargets(p *ir.Program, promote []bool) {
	for n := range p.Instructions {
		ins := &p.Instructions[n]
		if ins.Op != ir.OpCallGo || len(ins.Args) == 0 {
			continue
		}
		target := ins.Args[0]
		for steps := 0; steps < len(p.Instructions); steps++ {
			t := &p.Instructions[target]
			if t.Op == ir.OpCastUnsigned && t.Bits >= uint8(strconv.IntSize) {
				target = t.A
				continue
			}
			if t.Op == ir.OpGet {
				promote[t.Variable] = false
				v := p.Variables[t.Variable]
				if v.Immutable && len(v.Initial) == 1 {
					target = v.Initial[0]
					continue
				}
			}
			break
		}
	}
}

// variableParts returns the type of each part of v and the number of parts:
// a 128-bit Var has two 64-bit parts.
func variableParts(v *ir.Variable) (ir.Type, int) {
	if v.Type == ir.Scalar(128) {
		return ir.Scalar(64), 2
	}
	return v.Type, 1
}

// buildBlocks splits p into blocks and computes reachability, predecessors
// and a reverse postorder.
func (pr *variablePromoter) buildBlocks() {
	p := pr.p
	pr.blockAt = make([]int, len(p.Instructions))
	if p.Control == nil {
		pr.blocks = []promotionBlock{{step: -1, start: 0, end: len(p.Instructions), reachable: true}}
		pr.order, pr.rpo = []int{0}, []int{0}
		return
	}
	control := p.Control
	labelBlock := make([]int, len(control.Labels))
	pr.blocks = make([]promotionBlock, 0, len(control.Steps)-len(control.Labels))
	for index, step := range control.Steps {
		if step.IsLabel {
			labelBlock[step.Label] = len(pr.blocks)
			continue
		}
		for pos := step.Start; pos < step.End; pos++ {
			pr.blockAt[pos] = len(pr.blocks)
		}
		pr.blocks = append(pr.blocks, promotionBlock{step: index, start: step.Start, end: step.End})
	}
	for b := range pr.blocks {
		step := &control.Steps[pr.blocks[b].step]
		switch step.Term {
		case ir.TermJump:
			pr.blocks[b].succ[0], pr.blocks[b].nsucc = labelBlock[step.To], 1
		case ir.TermBranch:
			pr.blocks[b].succ = [2]int{labelBlock[step.To], labelBlock[step.Other]}
			pr.blocks[b].nsucc = 2
		}
	}
	// Reverse postorder of the reachable blocks, iteratively.
	type frame struct{ b, next int }
	post := make([]int, 0, len(pr.blocks))
	pr.blocks[0].reachable = true
	stack := []frame{{0, 0}}
	for len(stack) != 0 {
		top := &stack[len(stack)-1]
		block := &pr.blocks[top.b]
		if top.next < block.nsucc {
			s := block.succ[top.next]
			top.next++
			if !pr.blocks[s].reachable {
				pr.blocks[s].reachable = true
				stack = append(stack, frame{s, 0})
			}
			continue
		}
		post = append(post, top.b)
		stack = stack[:len(stack)-1]
	}
	pr.order = make([]int, len(post))
	pr.rpo = make([]int, len(pr.blocks))
	for b := range pr.rpo {
		pr.rpo[b] = -1
	}
	for i, b := range post {
		pr.order[len(post)-1-i] = b
		pr.rpo[b] = len(post) - 1 - i
	}
	// Distinct predecessors, reachable ones first, in one slab.
	counts := make([]int, len(pr.blocks)+1)
	for b := range pr.blocks {
		block := &pr.blocks[b]
		for k := 0; k < block.nsucc; k++ {
			if k == 0 || block.succ[1] != block.succ[0] {
				counts[block.succ[k]]++
			}
		}
	}
	slab := make([]int, 0, len(pr.blocks)*2)
	for b := range pr.blocks {
		pr.blocks[b].preds = slab[len(slab) : len(slab) : len(slab)+counts[b]]
		slab = slab[:len(slab)+counts[b]]
	}
	for pass := 0; pass < 2; pass++ {
		for b := range pr.blocks {
			if pr.blocks[b].reachable != (pass == 0) {
				continue
			}
			block := &pr.blocks[b]
			for k := 0; k < block.nsucc; k++ {
				s := block.succ[k]
				if k == 1 && block.succ[0] == s {
					block.succSlot[1] = block.succSlot[0]
					continue
				}
				block.succSlot[k] = len(pr.blocks[s].preds)
				pr.blocks[s].preds = append(pr.blocks[s].preds, b)
				if pass == 0 {
					pr.blocks[s].nreachable++
				}
			}
		}
	}
	pr.findLoops()
}

// findLoops finds the loops of the reachable blocks: a header is the target
// of a retreating edge in reverse postorder, and its loop holds the blocks
// that reach a latch without passing the header. Inner loops are found
// first, so each block's loop is its innermost one.
func (pr *variablePromoter) findLoops() {
	nb := len(pr.blocks)
	rpo := pr.rpo
	pr.loopOf = make([]int, nb)
	pr.loopParent = make([]int, nb)
	for b := range pr.loopOf {
		pr.loopOf[b], pr.loopParent[b] = -1, -1
	}
	for _, h := range pr.order {
		for _, pred := range pr.blocks[h].preds[:pr.blocks[h].nreachable] {
			if rpo[pred] >= rpo[h] {
				pr.headers = append(pr.headers, h)
				break
			}
		}
	}
	// top returns the outermost loop found so far that contains b's loop.
	top := func(h int) int {
		for pr.loopParent[h] >= 0 {
			h = pr.loopParent[h]
		}
		return h
	}
	var work []int
	for i := len(pr.headers) - 1; i >= 0; i-- {
		h := pr.headers[i]
		work = work[:0]
		for _, pred := range pr.blocks[h].preds[:pr.blocks[h].nreachable] {
			if rpo[pred] >= rpo[h] {
				work = append(work, pred)
			}
		}
		for len(work) != 0 {
			x := work[len(work)-1]
			work = work[:len(work)-1]
			if x == h {
				continue
			}
			if l := pr.loopOf[x]; l >= 0 {
				// x belongs to an inner loop: attach that loop's outermost
				// header and continue from it.
				t := top(l)
				if t == h {
					continue
				}
				pr.loopParent[t] = h
				x = t
				for _, pred := range pr.blocks[x].preds[:pr.blocks[x].nreachable] {
					if rpo[pred] > rpo[h] && top(pr.loopOfOr(pred)) != h {
						work = append(work, pred)
					}
				}
				continue
			}
			pr.loopOf[x] = h
			for _, pred := range pr.blocks[x].preds[:pr.blocks[x].nreachable] {
				if rpo[pred] > rpo[h] {
					work = append(work, pred)
				}
			}
		}
		if pr.loopOf[h] < 0 {
			pr.loopOf[h] = h
		}
	}
}

// loopOfOr returns b's innermost loop header, or b itself outside loops.
func (pr *variablePromoter) loopOfOr(b int) int {
	if l := pr.loopOf[b]; l >= 0 {
		return l
	}
	return b
}

// build constructs SSA form for the Vars marked in promote. It returns the
// Vars that cannot be promoted; the caller retries without them.
func (pr *variablePromoter) build(promote []bool) (failed []int) {
	p := pr.p
	pr.phis = pr.phis[:0]
	pr.keyBase = make([]int, len(p.Variables))
	keys := 0
	for v := range p.Variables {
		if promote[v] {
			_, parts := variableParts(&p.Variables[v])
			keys += parts
		}
	}
	pr.keyVar, pr.keyPart, pr.keyType = make([]int, 0, keys), make([]int, 0, keys), make([]ir.Type, 0, keys)
	for v := range p.Variables {
		pr.keyBase[v] = -1
		if !promote[v] {
			continue
		}
		pr.keyBase[v] = len(pr.keyVar)
		t, parts := variableParts(&p.Variables[v])
		for part := range parts {
			pr.keyVar = append(pr.keyVar, v)
			pr.keyPart = append(pr.keyPart, part)
			pr.keyType = append(pr.keyType, t)
		}
	}
	pr.getRepl = make([]int, len(p.Instructions))
	for i := range pr.getRepl {
		pr.getRepl[i] = -1
	}
	nb := len(pr.blocks)
	stamps := make([]int, 6*nb)
	for i := range stamps[:4*nb] {
		stamps[i] = -1
	}
	pr.liveStamp, pr.defStamp, pr.outStamp, pr.phiStamp = stamps[:nb:nb], stamps[nb:2*nb:2*nb], stamps[2*nb:3*nb:3*nb], stamps[3*nb:4*nb:4*nb]
	pr.phiIndex, pr.defOut = stamps[4*nb:5*nb:5*nb], stamps[5*nb:]
	budget := 8*len(p.Instructions) + 1<<16
	used := 0
	var accesses []int
	var work []int
	// A Var the policy or the construction rejects keeps its home. Its
	// parts are independent of the others: a part's values reach other
	// parts' parameters only through its Gets, which keep their meaning once
	// their replacements are dropped below.
	isFailed := make([]bool, len(p.Variables))
	var kept []int
	fail := func(v int) {
		if !isFailed[v] {
			isFailed[v] = true
			kept = append(kept, v)
		}
	}
	// Loop-carried parts compete for each header's capacity; the parts
	// accessed most often inside loops come first.
	load := promotionLoad{blocks: make([][2]int, nb), carried: make([][2]int, nb), accessed: make([]int, nb), changed: make([]int, nb), header: make([]bool, nb)}
	for b := range load.accessed {
		load.accessed[b], load.changed[b] = -1, -1
	}
	for _, h := range pr.headers {
		load.header[h] = true
	}
	var live, defs, unreachable, visit []int
	for _, key := range pr.keyOrder() {
		v, part := pr.keyVar[key], pr.keyPart[key]
		variable := &p.Variables[v]
		if isFailed[v] {
			continue
		}
		// Accesses of this part in program order, including the capture's
		// definition at its scope entry.
		accesses = accesses[:0]
		for _, pos := range pr.facts.VarUses(v) {
			if int(p.Instructions[pos].Imm) == part {
				accesses = append(accesses, pos)
			}
		}
		// Definition blocks and upward-exposed uses. A part the policy
		// cannot promote stops the walk at the first block that shows it.
		work, live = work[:0], live[:0]
		bad := false
		floating := pr.floatValued(key, accesses)
		if pr.policy.loopCarried && len(pr.headers) != 0 {
			pr.markLoops(key, accesses, &load)
		}
		markLive := func(b int) {
			pr.liveStamp[b] = key
			work = append(work, b)
			live = append(live, b)
			if floating || load.header[b] && (!pr.policy.loopCarried || load.accessed[b] != key) ||
				pr.policy.blockCapacity > 0 && load.blocks[b][0] >= pr.policy.blockCapacity {
				bad = true
			}
		}
		defs, unreachable = defs[:0], unreachable[:0]
		markDef := func(b int) {
			if pr.defStamp[b] != key {
				pr.defStamp[b] = key
				defs = append(defs, b)
			}
		}
		if variable.Immutable {
			markDef(pr.blockAt[variable.Declaration])
		}
		for _, pos := range accesses {
			b := pr.blockAt[pos]
			if !pr.blocks[b].reachable {
				if len(unreachable) == 0 || unreachable[len(unreachable)-1] != b {
					unreachable = append(unreachable, b)
				}
			}
			if p.Instructions[pos].Op == ir.OpSet {
				markDef(b)
				continue
			}
			if pr.defStamp[b] != key && pr.blocks[b].reachable && pr.liveStamp[b] != key {
				markLive(b)
			}
		}
		// Liveness: walk back from upward-exposed uses to definitions.
		for len(work) != 0 && !bad {
			b := work[len(work)-1]
			work = work[:len(work)-1]
			block := &pr.blocks[b]
			if b == 0 || block.nreachable == 0 {
				bad = true // reaches the entry undefined
				break
			}
			for _, pred := range block.preds[:block.nreachable] {
				// A block that defines the part supplies it at its end.
				if pr.liveStamp[pred] == key || pr.defStamp[pred] == key {
					continue
				}
				markLive(pred)
			}
		}
		if !bad {
			bad = !pr.admit(key, accesses, live, &load)
		}
		if bad {
			fail(v)
			continue
		}
		// Parameters at live-in merges.
		firstPhi := len(pr.phis)
		for _, b := range live {
			block := &pr.blocks[b]
			if block.nreachable >= 2 {
				if used += 1 + len(block.preds); used > budget {
					bad = true
					break
				}
				pr.phiStamp[b], pr.phiIndex[b] = key, len(pr.phis)
				pr.phis = append(pr.phis, promotionPhi{block: b, key: key, repl: -1})
			}
		}
		if bad {
			pr.phis = pr.phis[:firstPhi]
			fail(v)
			continue
		}
		// The value live into each block, in reverse postorder: a merge's
		// parameter, or the value at the end of the only predecessor.
		initial := variable.Initial[part]
		visit = append(visit[:0], live...)
		for _, b := range defs {
			if pr.liveStamp[b] != key && pr.blocks[b].reachable {
				visit = append(visit, b)
			}
		}
		slices.SortFunc(visit, func(x, y int) int { return pr.rpo[x] - pr.rpo[y] })
		for _, b := range visit {
			value := -1
			if pr.liveStamp[b] == key {
				if pr.phiStamp[b] == key {
					value = pr.n + pr.phiIndex[b]
				} else {
					pred := pr.blocks[b].preds[0]
					if pr.outStamp[pred] != key {
						bad = true // the predecessor comes later in the order
						break
					}
					value = pr.defOut[pred]
				}
			}
			value = pr.walkBlock(b, key, value, initial, accesses)
			pr.outStamp[b], pr.defOut[b] = key, value
		}
		if bad {
			pr.phis = pr.phis[:firstPhi]
			fail(v)
			continue
		}
		// Unreachable blocks see the initial value; their code never runs.
		unreachableOut := func(b int) {
			if pr.outStamp[b] != key {
				pr.outStamp[b], pr.defOut[b] = key, pr.walkBlock(b, key, initial, initial, accesses)
			}
		}
		for _, b := range unreachable {
			unreachableOut(b)
		}
		// Parameter arguments: the value at the end of each predecessor.
		for i := firstPhi; i < len(pr.phis); i++ {
			phi := &pr.phis[i]
			block := &pr.blocks[phi.block]
			phi.ops = make([]int, len(block.preds))
			for k, pred := range block.preds {
				if k >= block.nreachable {
					unreachableOut(pred)
				}
				if pr.outStamp[pred] != key {
					// A reachable predecessor where the Var is not live out
					// cannot occur; treat it as unpromotable.
					bad = true
					break
				}
				phi.ops[k] = pr.defOut[pred]
			}
			if bad {
				break
			}
		}
		// Every Get has its value; anything else would be a bug, which
		// keeps the Var in its home rather than miscompile it.
		for _, pos := range accesses {
			bad = bad || p.Instructions[pos].Op == ir.OpGet && pr.getRepl[pos] < 0
		}
		if bad {
			pr.phis = pr.phis[:firstPhi]
			fail(v)
		}
	}
	for _, v := range kept {
		pr.keyBase[v] = -1
		for _, pos := range pr.facts.VarUses(v) {
			pr.getRepl[pos] = -1
		}
	}
	for i := range pr.phis {
		if isFailed[pr.keyVar[pr.phis[i].key]] {
			pr.phis[i].dead = true
		}
	}
	pr.removeTrivialPhis()
	return pr.checkOrder()
}

// keyOrder returns the promoted parts, those accessed most often inside
// loops first, then in Var order.
func (pr *variablePromoter) keyOrder() []int {
	order := make([]int, len(pr.keyVar))
	weight := make([]int, len(pr.keyVar))
	for key := range order {
		order[key] = key
		if len(pr.headers) == 0 {
			continue
		}
		for _, pos := range pr.facts.VarUses(pr.keyVar[key]) {
			if pr.loopOf[pr.blockAt[pos]] >= 0 {
				weight[key]++
			}
		}
	}
	if len(pr.headers) != 0 {
		slices.SortStableFunc(order, func(x, y int) int { return weight[y] - weight[x] })
	}
	return order
}

// floatValued reports whether a part holds FP values: its Var has an FP
// home, or one of its Sets or its capture stores an FP result.
func (pr *variablePromoter) floatValued(key int, accesses []int) bool {
	v := pr.keyVar[key]
	if pr.facts.IsFloating(v) {
		return true
	}
	fp := func(id int) bool {
		switch pr.p.Instructions[id].Op {
		case ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv, ir.OpFMA, ir.OpFSqrt, ir.OpFConvert, ir.OpFProductError, ir.OpFFromSigned, ir.OpFFromUnsigned:
			return true
		}
		return false
	}
	if variable := &pr.p.Variables[v]; variable.Immutable && fp(variable.Initial[pr.keyPart[key]]) {
		return true
	}
	for _, pos := range accesses {
		if ins := &pr.p.Instructions[pos]; ins.Op == ir.OpSet && fp(ins.A) {
			return true
		}
	}
	return false
}

// promotionLoad is what the parts admitted so far cost: the parts live into
// each block per bank, those that loop headers carry and their loops
// change, and the loops that access or change the part being
// admitted (stamped with its key).
type promotionLoad struct {
	blocks, carried   [][2]int
	accessed, changed []int
	header            []bool
}

// admit applies the policy to a part that is live into the blocks in live.
// It reports whether the part may be promoted, and then charges it to them.
func (pr *variablePromoter) admit(key int, accesses, live []int, load *promotionLoad) bool {
	if len(live) == 0 {
		return true // the part never crosses a block boundary
	}
	// The walk has rejected FP parts (FP label parameters keep a dead
	// integer copy on every edge today, so FP state that crosses blocks
	// stays in its FP home), loop-carried parts the policy keeps in homes,
	// parts carried through loops that do not access them, and blocks at
	// their capacity.
	const bank = 0
	words := pr.partWords(key)
	for _, b := range live {
		// A part the loop only reads crosses it as a dominating value; a
		// part it changes needs registers for the parameter.
		if load.header[b] && load.changed[b] == key && load.carried[b][bank]+words > pr.policy.capacity[bank] {
			return false
		}
	}
	for _, b := range live {
		load.blocks[b][bank]++
		if load.header[b] && load.changed[b] == key {
			load.carried[b][bank] += words
		}
	}
	return true
}

// markLoops stamps the loops that access the part, and those that change it.
func (pr *variablePromoter) markLoops(key int, accesses []int, load *promotionLoad) {
	for _, pos := range accesses {
		for h := pr.loopOf[pr.blockAt[pos]]; h >= 0 && load.accessed[h] != key; h = pr.loopParent[h] {
			load.accessed[h] = key
		}
		if pr.p.Instructions[pos].Op == ir.OpSet {
			for h := pr.loopOf[pr.blockAt[pos]]; h >= 0 && load.changed[h] != key; h = pr.loopParent[h] {
				load.changed[h] = key
			}
		}
	}
}

// walkBlock replaces the Gets of key in block b, which starts with value in,
// and returns the value at the block's end. For an immutable capture the
// declaration at the scope entry defines initial.
func (pr *variablePromoter) walkBlock(b, key, in, initial int, accesses []int) int {
	p := pr.p
	block := &pr.blocks[b]
	value := in
	variable := &p.Variables[pr.keyVar[key]]
	if variable.Immutable && pr.blockAt[variable.Declaration] == b && variable.Declaration >= block.start && variable.Declaration < block.end {
		// Accesses come after the declaration.
		value = initial
	}
	lo, _ := slices.BinarySearch(accesses, block.start)
	for _, pos := range accesses[lo:] {
		if pos >= block.end {
			break
		}
		ins := &p.Instructions[pos]
		if ins.Op == ir.OpSet {
			value = ins.A
		} else {
			pr.getRepl[pos] = value
		}
	}
	return value
}

// resolve follows Get and parameter replacements to a final value.
func (pr *variablePromoter) resolve(ref int) int {
	for {
		switch {
		case ref < 0:
			return ref
		case ref < pr.n:
			if r := pr.getRepl[ref]; r >= 0 {
				ref = r
				continue
			}
			return ref
		default:
			if r := pr.phis[ref-pr.n].repl; r >= 0 {
				ref = r
				continue
			}
			return ref
		}
	}
}

// removeTrivialPhis replaces each parameter whose reachable arguments are one
// value, or the parameter itself, by that value, until none is left, then
// marks the parameters that some replaced Get still reads.
func (pr *variablePromoter) removeTrivialPhis() {
	pr.phiUsers = make([][]int, len(pr.phis))
	for i := range pr.phis {
		for _, op := range pr.phis[i].ops {
			if r := pr.resolve(op); r >= pr.n && r-pr.n != i {
				pr.phiUsers[r-pr.n] = append(pr.phiUsers[r-pr.n], i)
			}
		}
	}
	work := make([]int, 0, len(pr.phis))
	for i := range pr.phis {
		work = append(work, i)
	}
	for len(work) != 0 {
		i := work[len(work)-1]
		work = work[:len(work)-1]
		phi := &pr.phis[i]
		if phi.repl >= 0 || phi.dead {
			continue
		}
		self := pr.n + i
		same := -1
		trivial := true
		for _, op := range phi.ops[:pr.blocks[phi.block].nreachable] {
			r := pr.resolve(op)
			if r == self || r == same {
				continue
			}
			if same >= 0 {
				trivial = false
				break
			}
			same = r
		}
		if !trivial || same < 0 {
			continue
		}
		phi.repl = same
		work = append(work, pr.phiUsers[i]...)
		if same >= pr.n {
			pr.phiUsers[same-pr.n] = append(pr.phiUsers[same-pr.n], pr.phiUsers[i]...)
		}
	}
	// Liveness of the remaining parameters, from the Gets that read them.
	var mark []int
	visit := func(ref int) {
		if r := pr.resolve(ref); r >= pr.n && !pr.phis[r-pr.n].live {
			pr.phis[r-pr.n].live = true
			mark = append(mark, r-pr.n)
		}
	}
	for pos, ref := range pr.getRepl {
		if ref >= 0 && pr.p.Instructions[pos].Op == ir.OpGet {
			visit(ref)
		}
	}
	for len(mark) != 0 {
		i := mark[len(mark)-1]
		mark = mark[:len(mark)-1]
		for _, op := range pr.phis[i].ops {
			visit(op)
		}
	}
}

// checkOrder reports the Vars whose replaced Gets or parameter arguments
// would read a value defined later in the layout. The reaching value always
// dominates its use; a front end that lays a dominator out after the blocks
// it dominates keeps that Var in memory.
func (pr *variablePromoter) checkOrder() (failed []int) {
	var bad []int
	// before reports whether ref is defined before position pos of block b.
	before := func(ref, pos, b int) bool {
		if ref >= pr.n {
			return pr.phis[ref-pr.n].block <= b
		}
		return ref < pos
	}
	for pos, ref := range pr.getRepl {
		if ref >= 0 && pr.p.Instructions[pos].Op == ir.OpGet && !before(pr.resolve(ref), pos, pr.blockAt[pos]) {
			bad = append(bad, pr.p.Instructions[pos].Variable)
		}
	}
	for i := range pr.phis {
		phi := &pr.phis[i]
		if !phi.live || phi.repl >= 0 {
			continue
		}
		for k, op := range phi.ops {
			pred := pr.blocks[phi.block].preds[k]
			if r := pr.resolve(op); r >= 0 && !before(r, pr.blocks[pred].end, pred) {
				bad = append(bad, pr.keyVar[phi.key])
			}
		}
	}
	slices.Sort(bad)
	return slices.Compact(bad)
}

// emit builds the promoted program.
func (pr *variablePromoter) emit() *ir.Program {
	p := pr.p
	n := pr.n
	removed := func(pos int) bool {
		ins := &p.Instructions[pos]
		return (ins.Op == ir.OpGet || ins.Op == ir.OpSet) && pr.keyBase[ins.Variable] >= 0
	}
	// Live parameters of each block, in key order.
	for i := range pr.phis {
		if phi := &pr.phis[i]; phi.live && phi.repl < 0 {
			pr.blocks[phi.block].phis = append(pr.blocks[phi.block].phis, i)
		}
	}
	for b := range pr.blocks {
		slices.SortFunc(pr.blocks[b].phis, func(x, y int) int { return pr.phis[x].key - pr.phis[y].key })
	}
	newID := make([]int, n+1) // new position of each kept instruction; n maps to the end
	out := &ir.Program{
		ParameterTypes: p.ParameterTypes, Arity: p.Arity,
		Instructions: make([]ir.Instruction, 0, len(p.Instructions)),
	}
	starts := make([]int, len(pr.blocks)+1)
	// A Get that reads a constant from another block becomes a copy of the
	// constant, so the constant is not carried across the blocks between.
	remat := make([]bool, n)
	for pos, ref := range pr.getRepl {
		if ref < 0 || p.Instructions[pos].Op != ir.OpGet {
			continue
		}
		if r := pr.resolve(ref); r < n && pr.blockAt[r] != pr.blockAt[pos] && p.Instructions[r].Op == ir.OpConst && p.Instructions[r].Symbol == "" {
			remat[pos] = true
		}
	}
	mapRef := func(ref int) int {
		if ref >= 0 && ref < n && remat[ref] {
			return newID[ref]
		}
		r := pr.resolve(ref)
		if r < 0 {
			return r
		}
		if r >= n {
			return pr.phis[r-n].newID
		}
		return newID[r]
	}
	// keptVar maps each Var that keeps its home to its new index; captures
	// lists each region's immutable Vars in order.
	keptVar := make([]int, len(p.Variables))
	captures := make(map[int][]int)
	kept := 0
	for v := range p.Variables {
		keptVar[v] = kept
		if pr.keyBase[v] < 0 {
			kept++
		}
		if p.Variables[v].Immutable {
			captures[p.Variables[v].Region] = append(captures[p.Variables[v].Region], v)
		}
	}
	var buffer [4]int
	var paramBase []int
	if p.Control != nil {
		labelBlock := make([]int, len(p.Control.Labels))
		block := 0
		for _, step := range p.Control.Steps {
			if step.IsLabel {
				labelBlock[step.Label] = block
			} else {
				block++
			}
		}
		paramBase = make([]int, len(pr.blocks))
		for label, b := range labelBlock {
			paramBase[b] = p.Control.Params[label]
		}
	}
	for b := range pr.blocks {
		block := &pr.blocks[b]
		starts[b] = len(out.Instructions)
		for k, i := range block.phis {
			phi := &pr.phis[i]
			t := pr.keyType[phi.key]
			param := ir.Instruction{Op: ir.OpLabelParam, Imm: uint64(paramBase[b] + k), Bits: t.LaneBits}
			if t.Kind != ir.ScalarKind {
				param.Type = t
			}
			if t.Kind == ir.MaskKind {
				param.Bits = 64
			}
			phi.newID = len(out.Instructions)
			out.Instructions = append(out.Instructions, param)
		}
		for pos := block.start; pos < block.end; pos++ {
			if remat[pos] {
				newID[pos] = len(out.Instructions)
				out.Instructions = append(out.Instructions, p.Instructions[pr.resolve(pos)].Clone())
				continue
			}
			if removed(pos) {
				newID[pos] = len(out.Instructions) // position marker only
				continue
			}
			ins := p.Instructions[pos].Clone()
			switch ins.Op {
			case ir.OpScopeEnter:
				args := ins.Args[:0]
				// Args are the Initial values of the region's captures, in
				// Var order; promoted captures leave the list.
				k := 0
				for _, v := range captures[int(ins.Imm)] {
					for range p.Variables[v].Initial {
						if pr.keyBase[v] < 0 {
							args = append(args, mapRef(p.Instructions[pos].Args[k]))
						}
						k++
					}
				}
				ins.Args = args
			case ir.OpScopeLeave, ir.OpSyscall, ir.OpCallC, ir.OpCallGo:
				for i, id := range ins.Args {
					ins.Args[i] = mapRef(id)
				}
			default:
				switch len(ins.InputsInto(buffer[:0])) {
				case 3:
					ins.C = mapRef(ins.C)
					fallthrough
				case 2:
					ins.B = mapRef(ins.B)
					fallthrough
				case 1:
					ins.A = mapRef(ins.A)
				}
			}
			if ins.Op == ir.OpGet || ins.Op == ir.OpSet {
				ins.Variable = keptVar[ins.Variable]
			}
			newID[pos] = len(out.Instructions)
			out.Instructions = append(out.Instructions, ins)
		}
	}
	starts[len(pr.blocks)] = len(out.Instructions)
	newID[n] = len(out.Instructions)
	mapIDs := func(ids []int) []int {
		if ids == nil {
			return nil
		}
		mapped := make([]int, len(ids))
		for i, id := range ids {
			mapped[i] = mapRef(id)
		}
		return mapped
	}
	out.Result = p.Result
	if p.Result >= 0 {
		out.Result = mapRef(p.Result)
	}
	out.ResultParts = mapIDs(p.ResultParts)
	// Scope exits.
	out.ScopeExits = make([]ir.ScopeExit, len(p.ScopeExits))
	for i, exit := range p.ScopeExits {
		next := ir.ScopeExit{ID: exit.ID, Parent: exit.Parent, Enter: newID[exit.Enter], Position: newID[exit.Position-1] + 1}
		for _, id := range exit.Hidden {
			if !removed(id) {
				next.Hidden = append(next.Hidden, newID[id])
			}
		}
		next.Exports = mapIDs(exit.Exports)
		out.ScopeExits[i] = next
	}
	// Variables that keep their homes.
	for v, variable := range p.Variables {
		if pr.keyBase[v] >= 0 {
			continue
		}
		variable.Declaration = newID[variable.Declaration]
		variable.Initial = mapIDs(variable.Initial)
		out.Variables = append(out.Variables, variable)
	}
	if p.Control == nil {
		return out
	}
	// Control: labels gain the parameters of their blocks.
	control := p.Control
	nc := &ir.Control{
		Labels: append([]bool(nil), control.Labels...), LabelScopes: append([]int(nil), control.LabelScopes...),
		Params: append([]int(nil), control.Params...), ParamBits: make([][]uint8, len(control.Labels)),
		Steps: make([]ir.ControlStep, len(control.Steps)),
	}
	typed := len(control.ParamTypes) != 0
	for i := range pr.phis {
		if phi := &pr.phis[i]; phi.live && phi.repl < 0 && pr.keyType[phi.key].Kind != ir.ScalarKind {
			typed = true
		}
	}
	if typed {
		nc.ParamTypes = make([][]ir.Type, len(control.Labels))
	}
	labelBlock := make([]int, len(control.Labels))
	{
		block := 0
		for _, step := range control.Steps {
			if step.IsLabel {
				labelBlock[step.Label] = block
			} else {
				block++
			}
		}
	}
	for label := range control.Labels {
		nc.ParamBits[label] = append([]uint8(nil), control.ParamBits[label]...)
		if typed {
			if len(control.ParamTypes) != 0 {
				nc.ParamTypes[label] = append([]ir.Type(nil), control.ParamTypes[label]...)
			} else {
				for _, bits := range control.ParamBits[label] {
					nc.ParamTypes[label] = append(nc.ParamTypes[label], ir.Scalar(bits))
				}
			}
		}
		for _, i := range pr.blocks[labelBlock[label]].phis {
			t := pr.keyType[pr.phis[i].key]
			bits := t.LaneBits
			if t.Kind == ir.MaskKind {
				bits = 64
			}
			nc.Params[label]++
			nc.ParamBits[label] = append(nc.ParamBits[label], bits)
			if typed {
				nc.ParamTypes[label] = append(nc.ParamTypes[label], t)
			}
		}
	}
	// Edges into empty jump blocks go straight to the jump's target. A
	// promoted Var leaves blocks such as the fallthrough of Branch followed
	// by Jump empty; carrying values through them would cost edge copies.
	forward := pr.forwardEmptyJumps(func(pos int) bool { return removed(pos) && !remat[pos] })
	targetOf := func(b, k int) (block, slot int) {
		block, slot = pr.blocks[b].succ[k], pr.blocks[b].succSlot[k]
		if f := forward[block]; f.block >= 0 && pr.blocks[b].step >= 0 {
			// The other edge of a branch must not reach the same block twice.
			other := -1
			if pr.blocks[b].nsucc == 2 {
				other = pr.blocks[b].succ[1-k]
				if g := forward[other]; g.block >= 0 {
					other = g.block
				}
			}
			if other != f.block {
				return f.block, f.slot
			}
		}
		return block, slot
	}
	remaining := make([]int, len(pr.blocks))
	for b := range pr.blocks {
		for k := 0; k < pr.blocks[b].nsucc; k++ {
			if t, _ := targetOf(b, k); t == pr.blocks[b].succ[k] {
				remaining[t]++
			}
		}
	}
	folded := func(b int) bool { return forward[b].block >= 0 && remaining[b] == 0 }
	blockLabels := make([][]int, len(pr.blocks))
	{
		block := 0
		for _, step := range control.Steps {
			if step.IsLabel {
				blockLabels[block] = append(blockLabels[block], step.Label)
			} else {
				block++
			}
		}
	}
	// aliases lists the folded blocks whose labels each block takes over.
	aliases := make(map[int][]int)
	for b := range pr.blocks {
		if !folded(b) {
			continue
		}
		t := forward[b].block
		aliases[t] = append(aliases[t], b)
		model := blockLabels[t][0]
		for _, label := range blockLabels[b] {
			nc.Params[label] = nc.Params[model]
			nc.ParamBits[label] = append([]uint8(nil), nc.ParamBits[model]...)
			if typed {
				nc.ParamTypes[label] = append([]ir.Type(nil), nc.ParamTypes[model]...)
			}
		}
	}
	edgeArgs := func(from, k int, args []int) (int, []int) {
		t, slot := targetOf(from, k)
		if t != pr.blocks[from].succ[k] {
			args = forward[pr.blocks[from].succ[k]].args
		}
		target := &pr.blocks[t]
		if len(target.phis) == 0 {
			return blockLabels[t][0], mapIDs(args)
		}
		mapped := make([]int, 0, len(args)+len(target.phis))
		for _, id := range args {
			mapped = append(mapped, mapRef(id))
		}
		for _, i := range target.phis {
			mapped = append(mapped, mapRef(pr.phis[i].ops[slot]))
		}
		return blockLabels[t][0], mapped
	}
	memo := make(map[*ir.PredicateExpr]*ir.PredicateExpr)
	nc.Steps = nc.Steps[:0]
	for b := range pr.blocks {
		if folded(b) {
			continue
		}
		for _, label := range blockLabels[b] {
			nc.Steps = append(nc.Steps, ir.ControlStep{IsLabel: true, Label: label})
		}
		for _, e := range aliases[b] {
			for _, label := range blockLabels[e] {
				nc.Steps = append(nc.Steps, ir.ControlStep{IsLabel: true, Label: label})
			}
		}
		step := control.Steps[pr.blocks[b].step]
		next := step
		next.Start, next.End = starts[b], starts[b+1]
		if step.Term != ir.TermJump && step.Result >= 0 {
			next.Result = mapRef(step.Result)
		}
		next.ResultParts = mapIDs(step.ResultParts)
		next.Values = mapIDs(step.Values)
		if step.Term != ir.TermReturn {
			// A jump's target keeps its original label, unless forwarded.
			label, args := edgeArgs(b, 0, step.Args)
			if t, _ := targetOf(b, 0); t != pr.blocks[b].succ[0] {
				next.To = label
			}
			next.Args = args
		}
		if step.Term == ir.TermBranch {
			label, args := edgeArgs(b, 1, step.OtherArgs)
			if t, _ := targetOf(b, 1); t != pr.blocks[b].succ[1] {
				next.Other = label
			}
			next.OtherArgs = args
			next.Predicate = remapPredicate(step.Predicate, memo, mapRef)
		}
		nc.Steps = append(nc.Steps, next)
	}
	out.Control = nc
	return out
}

// promotionForward is where an edge into an empty jump block goes instead:
// the block that the jump chain ends at, the predecessor slot whose
// parameter arguments the edge passes, and the jump's arguments.
type promotionForward struct {
	block, slot int
	args        []int
}

// forwardEmptyJumps finds the blocks that hold nothing but a jump once
// promoted accesses are removed: no instructions, scope exits, parameters
// or promoted parameters, in the scope of their target.
func (pr *variablePromoter) forwardEmptyJumps(removed func(int) bool) []promotionForward {
	p := pr.p
	control := p.Control
	forward := make([]promotionForward, len(pr.blocks))
	empty := make([]bool, len(pr.blocks))
	labelScope := make([]int, len(pr.blocks))
	params := make([]int, len(pr.blocks))
	{
		block := 0
		for _, step := range control.Steps {
			if step.IsLabel {
				labelScope[block] = control.LabelScopes[step.Label]
				params[block] += control.Params[step.Label]
			} else {
				block++
			}
		}
	}
	for b := range pr.blocks {
		forward[b].block = -1
		block := &pr.blocks[b]
		step := &control.Steps[block.step]
		if b == 0 || step.Term != ir.TermJump || step.LeaveStart != step.LeaveEnd || len(block.phis) != 0 || params[b] != 0 ||
			labelScope[b] != labelScope[block.succ[0]] {
			continue
		}
		empty[b] = true
		for pos := block.start; pos < block.end; pos++ {
			if !removed(pos) {
				empty[b] = false
				break
			}
		}
	}
	for b := range pr.blocks {
		if !empty[b] {
			continue
		}
		// Follow the chain, stopping at a cycle of empty blocks.
		last, t := b, pr.blocks[b].succ[0]
		for steps := 0; empty[t] && t != b && steps < len(pr.blocks); steps++ {
			last, t = t, pr.blocks[t].succ[0]
		}
		if empty[t] {
			continue
		}
		forward[b] = promotionForward{block: t, slot: pr.blocks[last].succSlot[0], args: control.Steps[pr.blocks[last].step].Args}
	}
	return forward
}

// partWords returns how many general registers a part of key's Var takes.
func (pr *variablePromoter) partWords(key int) int {
	bits := int(pr.p.Variables[pr.keyVar[key]].Type.LaneBits)
	if bits == 0 || bits > 64 {
		bits = 64
	}
	word := int(pr.policy.wordBits)
	if word == 0 {
		return 1
	}
	return (bits + word - 1) / word
}
