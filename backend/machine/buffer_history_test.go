package machine

import (
	"fmt"
	"testing"
	"unsafe"
)

func TestBufferHistoryAlternationAndSteadyDemand(t *testing.T) {
	for _, gap := range []int{1, 2, 3, 10} {
		t.Run(fmt.Sprint(gap), func(t *testing.T) {
			var h BufferHistory
			for cycle := 0; cycle < 100; cycle++ {
				h.Observe(20000)
				if h.FinishCycle(20000) {
					t.Fatal("current peak discarded")
				}
				for i := 0; i < gap; i++ {
					h.Observe(4)
					if h.FinishCycle(20000) {
						t.Fatal("short gap discarded peak")
					}
				}
			}
			released := false
			for i := 0; i < 100; i++ {
				h.Observe(1000)
				released = released || h.FinishCycle(20000)
			}
			if !released {
				t.Fatal("steady demand never released excess")
			}
		})
	}
}

func TestBufferHistorySmallAndExtremeDemand(t *testing.T) {
	var h BufferHistory
	h.Observe(1)
	h.FinishCycle(1)
	for i := 0; i < 10; i++ {
		if h.FinishCycle(1) {
			t.Fatal("fractional one-element demand forgotten")
		}
	}
	var extreme BufferHistory
	extreme.Observe(int(^uint(0) >> 1))
	if extreme.FinishCycle(int(^uint(0) >> 1)) {
		t.Fatal("extreme demand overflowed")
	}
	var empty BufferHistory
	empty.Observe(-1)
	if empty.FinishCycle(-1) || empty.FinishCycle(0) {
		t.Fatal("nonpositive capacity is excess")
	}
}

// The packed cycle count must preserve the original policy throughout the
// range of actual buffer sizes, including fractional decay near one element.
func TestBufferHistoryPackedPolicy(t *testing.T) {
	type reference struct {
		recent uint64
		need   int
		small  uint8
	}
	var old reference
	var packed BufferHistory
	next := uint64(1)
	for cycle := 0; cycle < 20000; cycle++ {
		next = next*6364136223846793005 + 1
		need := int(next >> 33)
		if cycle%3 == 0 {
			need = cycle % 7
		}
		capacity := int(next >> 35)
		old.need = max(old.need, need)
		packed.Observe(need)
		if uint64(capacity) > 2*uint64(old.need) {
			old.small = min(old.small+1, 32)
		} else {
			old.small = 0
		}
		demand := uint64(old.need) * 64
		old.recent = max(demand, old.recent-min(old.recent, max(uint64(1), old.recent/16)))
		recent := (old.recent + 63) / 64
		band := uint64(4)
		if old.small == 32 {
			band = 2
		}
		want := capacity > 0 && uint64(capacity-1)/band >= max(uint64(old.need), recent)
		old.need = 0
		if got := packed.FinishCycle(capacity); got != want {
			t.Fatalf("cycle %d: got %v want %v", cycle, got, want)
		}
	}
}

func TestBufferHistoryCompact(t *testing.T) {
	if got := unsafe.Sizeof(BufferHistory{}); got > 16 {
		t.Fatalf("history retained %d bytes, want at most 16", got)
	}
}
