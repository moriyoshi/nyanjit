package machine

// threadedLabels resolves the functional graph of jump-only labels once.
// A multi-label cycle retains the original starting label, matching the old
// search. A self-loop is a terminal destination, so incoming jumps can fold to it.
func threadedLabels(insns []Instruction, labelIndex []int, bound []bool, context ...*layoutBuffers) ([]int, error) {
	var localWork layoutBuffers
	work := &localWork
	if len(context) > 0 && context[0] != nil {
		work = context[0]
	}
	next := workResetTracked(&work.threadNext, &work.history.threadNext, len(bound))
	dest := workResetTracked(&work.threadDest, &work.history.threadDest, len(bound))
	state := workResetTracked(&work.threadState, &work.history.threadState, len(bound))
	cyclic := workResetTracked(&work.threadCyclic, &work.history.threadCyclic, len(bound))
	for _, ins := range insns {
		if isBranch(ins.Op) && (ins.Imm >= uint64(len(bound)) || !bound[int(ins.Imm)]) {
			return nil, ErrInvalidBranch
		}
	}
	for label, index := range labelIndex {
		next[label] = -1
		dest[label] = label
		if bound[label] && index < len(insns) && insns[index].Op == OpJump {
			next[label] = int(insns[index].Imm)
		}
	}
	path := workAppendTracked(&work.threadPath, &work.history.threadPath, len(bound))
	defer func() { work.threadPath = path }()
	for label := range bound {
		if !bound[label] || state[label] != 0 {
			continue
		}
		path = path[:0]
		current := label
		for state[current] == 0 {
			state[current] = 1
			path = append(path, current)
			if next[current] < 0 || next[current] == current {
				state[current] = 2
				dest[current] = current
				break
			}
			current = next[current]
		}
		cycle := state[current] == 1 || cyclic[current]
		destination := dest[current]
		for i := len(path) - 1; i >= 0; i-- {
			id := path[i]
			state[id] = 2
			cyclic[id] = cycle
			if cycle {
				dest[id] = id
			} else {
				dest[id] = destination
			}
		}
	}
	return dest, nil
}
