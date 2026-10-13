package machine

import "slices"

type workDiscard struct {
	bytes int
	drop  func()
}

func addWorkDiscard[T any](out *[]workDiscard, value *[]T) {
	if n := workBytes(*value); n != 0 {
		*out = append(*out, workDiscard{n, func() {
			if workspacePoison {
				poisonDiscarded(*value)
			}
			*value = nil
		}})
	}
}

// TrimToBudget discards the largest backing buffers first until retained storage
// is at most budget bytes. It returns bytes freed and the largest remaining
// buffer's size. Accounting matches RetainedBytes. Discarded borrows expire.
func (w *Workspace) TrimToBudget(budget int) (freed, largest int) {
	budget = max(0, budget)
	total, _ := w.RetainedBytes(0)
	if total <= budget {
		for _, s := range w.streams {
			for _, n := range s.workSizes() {
				largest = max(largest, n)
			}
		}
		return
	}
	var choices []workDiscard
	for _, s := range w.streams {
		addWorkDiscard(&choices, &s.instructions)
		addWorkDiscard(&choices, &s.sizes)
		addWorkDiscard(&choices, &s.marks)
		addWorkDiscard(&choices, &s.layout.located)
		addWorkDiscard(&choices, &s.layout.old)
		addWorkDiscard(&choices, &s.layout.kept)
		addWorkDiscard(&choices, &s.layout.relax)
		addWorkDiscard(&choices, &s.layout.positions)
		addWorkDiscard(&choices, &s.layout.labelIndex)
		addWorkDiscard(&choices, &s.layout.oldToNew)
		addWorkDiscard(&choices, &s.layout.newPositions)
		addWorkDiscard(&choices, &s.layout.sizes)
		addWorkDiscard(&choices, &s.layout.pending)
		addWorkDiscard(&choices, &s.layout.remove)
		addWorkDiscard(&choices, &s.layout.referenced)
		addWorkDiscard(&choices, &s.layout.reachable)
		addWorkDiscard(&choices, &s.layout.threadNext)
		addWorkDiscard(&choices, &s.layout.threadDest)
		addWorkDiscard(&choices, &s.layout.threadPath)
		addWorkDiscard(&choices, &s.layout.threadState)
		addWorkDiscard(&choices, &s.layout.threadCyclic)
	}
	slices.SortFunc(choices, func(a, b workDiscard) int { return b.bytes - a.bytes })
	for i, c := range choices {
		if total-freed <= budget {
			largest = c.bytes
			return
		}
		c.drop()
		freed += c.bytes
		if i == len(choices)-1 {
			largest = 0
		}
	}
	return
}
