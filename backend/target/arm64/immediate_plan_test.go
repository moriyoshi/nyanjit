package arm64

import (
	"fmt"
	"strings"
	"testing"
)

func TestImmediatePlanOperations(t *testing.T) {
	for _, tc := range []struct {
		value uint64
		want  string
	}{
		{0, "movz 0x0<<0"},
		{0x1234, "movz 0x1234<<0"},
		{0x12340000abcd, "movz 0xabcd<<0 movk 0x1234<<32"},
		{^uint64(0), "movn 0x0<<0"},
		{0xffff_ffff_edcb_0000, "movn 0xffff<<0 movk 0xedcb<<16"},
		{0x123456789abcdef0, "movz 0xdef0<<0 movk 0x9abc<<16 movk 0x5678<<32 movk 0x1234<<48"},
	} {
		var got []string
		for _, part := range ImmediatePlan(tc.value) {
			got = append(got, fmt.Sprintf("%s %#x<<%d", part.Op, part.Imm, part.Shift))
		}
		if strings.Join(got, " ") != tc.want {
			t.Errorf("%#x: %v, want %s", tc.value, got, tc.want)
		}
	}
	values := []uint64{0, 1, ^uint64(0), 0xffff, 0xffff0000, 0xffff_0000_ffff_0000, 0x0000_ffff_0000_ffff, 1 << 63}
	for x := uint64(0x9e3779b97f4a7c15); len(values) < 4096; x = x*6364136223846793005 + 1442695040888963407 {
		values = append(values, x, x&0xffff_0000_ffff_0000, x|0xffff_ffff_0000_0000, x&0xffff)
	}
	for _, value := range values {
		plan := immediatePlan(value)
		if n := immediateCount(value); n != plan.n {
			t.Fatalf("%#x: count %d, plan %d", value, n, plan.n)
		}
		// The wide moves rebuild the value.
		var built uint64
		for _, part := range plan.parts[:plan.n] {
			word := part.word(5)
			imm, shift := uint64(word>>5&0xffff), (word>>21&3)*16
			switch word >> 29 & 3 {
			case 0:
				built = ^(imm << shift)
			case 2:
				built = imm << shift
			case 3:
				built = built&^(0xffff<<shift) | imm<<shift
			default:
				t.Fatalf("%#x: word %#x", value, word)
			}
		}
		if built != value {
			t.Fatalf("%#x: plan builds %#x", value, built)
		}
		words := appendImmediateWords(nil, 5, value)
		if len(words) != plan.n {
			t.Fatalf("%#x: %d words for %d parts", value, len(words), plan.n)
		}
		for i, part := range plan.parts[:plan.n] {
			if words[i] != part.word(5) {
				t.Fatalf("%#x: word %d is %#x, plan %#x", value, i, words[i], part.word(5))
			}
		}
	}
}
