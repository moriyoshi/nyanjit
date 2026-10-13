package arm64

import (
	"math/bits"
	"testing"
)

// searchLogicalImmediateEncoding is the exhaustive rotation search the
// closed form replaced; it serves as the oracle.
func searchLogicalImmediateEncoding(value uint64, dataWidth int) (uint32, bool) {
	if value == 0 || value == ^uint64(0) || dataWidth == 32 && value>>32 != 0 {
		return 0, false
	}
	if dataWidth == 32 && value == 0xffffffff {
		return 0, false
	}
	for period := 2; period <= dataWidth; period *= 2 {
		mask := ^uint64(0) >> (64 - period)
		element := value & mask
		var repeated uint64
		for shift := 0; shift < dataWidth; shift += period {
			repeated |= element << shift
		}
		if repeated != value {
			continue
		}
		run := bits.OnesCount64(element)
		if run == 0 || run == period {
			continue
		}
		ones := uint64(1)<<run - 1
		for rotation := 0; rotation < period; rotation++ {
			rotated := (ones>>rotation | ones<<(period-rotation)) & mask
			if rotated != element {
				continue
			}
			encoding := uint32(rotation)<<16 | (uint32(-2*period)&63|uint32(run-1))<<10
			if period == 64 {
				encoding |= 1 << 22
			}
			return encoding, true
		}
	}
	return 0, false
}

func TestLogicalImmediateEncodingMatchesSearch(t *testing.T) {
	check := func(value uint64) {
		for _, width := range []int{32, 64} {
			v := value
			if width == 32 {
				v = uint64(uint32(value))
			}
			got, gotOK := logicalImmediateEncoding(v, width)
			want, wantOK := searchLogicalImmediateEncoding(v, width)
			if got != want || gotOK != wantOK {
				t.Fatalf("%#x/%d: %#x %t, want %#x %t", v, width, got, gotOK, want, wantOK)
			}
		}
	}
	valid := 0
	// Every element size, run and rotation, replicated to 64 bits.
	for period := 2; period <= 64; period *= 2 {
		mask := ^uint64(0) >> (64 - period)
		for run := 1; run < period; run++ {
			ones := uint64(1)<<run - 1
			for rotation := 0; rotation < period; rotation++ {
				element := (ones>>rotation | ones<<(period-rotation)) & mask
				var value uint64
				for shift := 0; shift < 64; shift += period {
					value |= element << shift
				}
				valid++
				for _, v := range []uint64{value, value + 1, value - 1, ^value, value << 1, value >> 1, value ^ 1<<63, value ^ 1<<31} {
					check(v)
				}
			}
		}
	}
	if valid != 5334 {
		t.Fatalf("%d valid patterns, want 5334", valid)
	}
	for x := uint64(0x9e3779b97f4a7c15); valid < 200000; x = x*6364136223846793005 + 1442695040888963407 {
		check(x)
		check(x & (x >> 7))
		valid++
	}
}
