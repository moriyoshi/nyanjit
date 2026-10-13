package compile

import (
	"slices"
	"testing"
)

func TestPairedSpillSlotReuse(t *testing.T) {
	for _, test := range []struct {
		name string
		free []int
		ok   bool
	}{
		{"empty", nil, false}, {"single", []int{3}, false}, {"no-pair", []int{1, 4, 7}, false},
		{"tail", []int{7, 3, 4}, true}, {"reverse-tail", []int{7, 4, 3}, true},
		{"interleaved", []int{8, 1, 9, 4}, true}, {"reverse-interleaved", []int{9, 1, 8, 4}, true},
		{"slot-zero", []int{0, 7, 1, 9}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := slices.Clone(test.free)
			base, rest, ok := takeFreeSpillPair(test.free)
			if ok != test.ok {
				t.Fatalf("take(%v): ok=%t want %t", before, ok, test.ok)
			}
			if !ok {
				if !slices.Equal(rest, before) {
					t.Fatal("unsuccessful allocation changed the free list")
				}
				return
			}
			if len(rest) != len(before)-2 {
				t.Fatalf("remaining slots=%v", rest)
			}
			restored := append(slices.Clone(rest), base, base+1)
			slices.Sort(restored)
			slices.Sort(before)
			if !slices.Equal(restored, before) {
				t.Fatalf("slot reuse lost or duplicated storage: %v want %v", restored, before)
			}
		})
	}
}
