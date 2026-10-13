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
	// The element is the shortest period that repeats across the data width.
	period := dataWidth
	for period > 2 {
		half := period / 2
		mask := uint64(1)<<half - 1
		if value&mask != value>>half&mask {
			break
		}
		period = half
	}
	mask := ^uint64(0) >> (64 - period)
	element := value & mask
	// The element is a run of ones rotated right by rotation within the
	// period; a run that wraps is the complement of one that does not.
	var rotation, run int
	if shiftedMask(element) {
		low := bits.TrailingZeros64(element)
		run = bits.OnesCount64(element)
		rotation = (period - low) & (period - 1)
	} else {
		filled := element | ^mask
		if !shiftedMask(^filled) {
			return 0, false
		}
		high := bits.LeadingZeros64(^filled) - (64 - period)
		run = high + bits.TrailingZeros64(^filled)
		rotation = high
	}
	if run == 0 || run == period {
		return 0, false
	}
	encoding := uint32(rotation)<<16 | (uint32(-2*period)&63|uint32(run-1))<<10
	if period == 64 {
		encoding |= 1 << 22
	}
	return encoding, true
}

// shiftedMask reports whether x is one nonempty run of contiguous ones.
func shiftedMask(x uint64) bool {
	if x == 0 {
		return false
	}
	filled := x | (x - 1)
	return filled&(filled+1) == 0
}
