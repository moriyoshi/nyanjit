package compile

// homeCandidates indexes eligible visible variables by ID. Each leaf stores
// its next source use; range-min queries find used outer variables in stable
// ID order without rescanning every ancestor's variables for every scope.
type homeCandidates struct {
	size    int
	minimum []int
}

const noHomeCandidate = int(^uint(0) >> 1)

func newHomeCandidates(count int) *homeCandidates {
	q := new(homeCandidates)
	q.reset(count)
	return q
}
func (q *homeCandidates) reset(count int) {
	size := 1
	for size < count {
		size *= 2
	}
	q.size = size
	resetBuffer(&q.minimum, 2*size)
	for n := range q.minimum {
		q.minimum[n] = noHomeCandidate
	}
}
func (q *homeCandidates) set(id, next int) {
	at := q.size + id
	q.minimum[at] = next
	for at /= 2; at != 0; at /= 2 {
		q.minimum[at] = min(q.minimum[2*at], q.minimum[2*at+1])
	}
}
func (q *homeCandidates) first(from, before int) int {
	var find func(int, int, int) int
	find = func(at, lo, hi int) int {
		if hi <= from || q.minimum[at] >= before {
			return -1
		}
		if hi-lo == 1 {
			return lo
		}
		middle := lo + (hi-lo)/2
		if found := find(2*at, lo, middle); found >= 0 {
			return found
		}
		return find(2*at+1, middle, hi)
	}
	return find(1, 0, q.size)
}
