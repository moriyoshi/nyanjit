package machine

// BufferHistory tracks recent buffer demand across reuse cycles. Its zero value
// is ready to use. Observe records demand during a cycle; FinishCycle ends that cycle
// and reports whether a retained capacity is substantially larger than demand.
// A history belongs to one buffer and must not be used concurrently.
type BufferHistory struct {
	recent uint64 // Low 58 bits: demand in 1/64-element units; high 6: steady cycles.
	need   int
}

// Observe records nonnegative demand, in elements, during the current cycle.
// Multiple observations retain the largest demand; negative values count as zero.
func (h *BufferHistory) Observe(n int) { h.need = max(h.need, n) }

// FinishCycle ends a cycle and tests capacity against a decaying demand band.
// The fourfold band preserves a peak for approximately 21 small requests. After 32 consecutive
// requests below half capacity, a twofold band releases persistent medium slack.
// Thirty-two cycles cover two 16-cycle decay periods, separating steady demand
// from the short alternating workloads this policy is designed to preserve.
func (h *BufferHistory) FinishCycle(capacity int) bool {
	// Packing the cycle count keeps per-scope histories to 16 bytes. The demand
	// range still covers buffers of 2^52 elements, beyond Go's addressable heap.
	const recentMask uint64 = 1<<58 - 1
	small := h.recent >> 58
	previous := h.recent & recentMask
	if uint64(max(capacity, 0)) > 2*uint64(h.need) {
		small = min(small+1, 32)
	} else {
		small = 0
	}
	need := uint64(h.need)
	demand := need
	if demand > recentMask/64 {
		demand = recentMask
	} else {
		demand *= 64
	}
	next := max(demand, previous-min(previous, max(uint64(1), previous/16)))
	h.recent = next | small<<58
	h.need = 0
	recent := next / 64
	if next%64 != 0 {
		recent++
	}
	bandShift := uint(2)
	if small == 32 {
		bandShift = 1
	}
	return capacity > 0 && uint64(capacity-1)>>bandShift >= max(need, recent)
}
