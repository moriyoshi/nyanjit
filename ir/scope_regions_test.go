package ir

import (
	"math/rand"
	"testing"
)

func TestScopeRegionsMatchesIntervalPriority(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	type interval struct{ start, end int }
	for trial := 0; trial < 1000; trial++ {
		list := make([]interval, 64)
		for i := range list {
			list[i].start = rng.Intn(33)
			list[i].end = list[i].start + rng.Intn(34-list[i].start)
		}
		got := scopeRegions(32, len(list), func(i int) (int, int, int) { s := list[i]; return i + 1, s.start, s.end })
		for pos := range got {
			want, start := 0, -1
			for i, s := range list {
				if pos >= s.start && pos < s.end && s.start >= start {
					want, start = i+1, s.start
				}
			}
			if got[pos] != want {
				t.Fatalf("trial%d pos%d: got%d want%d", trial, pos, got[pos], want)
			}
		}
	}
}
