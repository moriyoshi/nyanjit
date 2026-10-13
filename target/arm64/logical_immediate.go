package arm64

import "math/bits"

// logicalImmediatePlan returns the N:immr:imms fields and the register width.
// A logical bitmask is a rotated run of ones repeated across 32 or 64 bits.
func logicalImmediatePlan(width uint8, value uint64) (uint32, uint8, bool) {
	if width != 8 && width != 16 && width != 32 && width != 64 {
		return 0, 0, false
	}
	if width < 64 {
		if encoding, ok := logicalImmediateEncoding(uint64(uint32(value)), 32); ok {
			return encoding, 32, true
		}
	}
	if encoding, ok := logicalImmediateEncoding(value, 64); ok {
		return encoding, 64, true
	}
	return 0, 0, false
}

func logicalImmediateEncoding(value uint64, dataWidth int) (uint32, bool) {
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
