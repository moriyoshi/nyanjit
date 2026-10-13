package compile

// takeFreeSpillPair removes two consecutive logical slots from the free list.
// A released register pair usually occupies the tail; the fallback also joins
// adjacent slots released separately or interleaved with other values.
func takeFreeSpillPair(free []int) (base int, rest []int, ok bool) {
	n := len(free)
	if n < 2 {
		return 0, free, false
	}
	a, b := free[n-2], free[n-1]
	if a+1 == b || b+1 == a {
		return min(a, b), free[:n-2], true
	}
	seen := make(map[int]int, n)
	for i, slot := range free {
		for _, neighbor := range []int{slot - 1, slot + 1} {
			if j, found := seen[neighbor]; found {
				free = append(free[:i], free[i+1:]...)
				free = append(free[:j], free[j+1:]...)
				return min(slot, neighbor), free, true
			}
		}
		seen[slot] = i
	}
	return 0, free, false
}
