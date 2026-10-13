package arm64

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

// Encodings checked against GNU as for the listed text.
func TestMulHighSignedEncoding(t *testing.T) {
	for _, test := range []struct {
		w     uint8
		words []uint32
		text  string
	}{
		{8, []uint32{0x13001c30, 0x13001c51, 0x1b117e00, 0x53083c00}, "sxtb W16, W1|sxtb W17, W2|mul W0, W16, W17|ubfx W0, W0, #8, #8"},
		{16, []uint32{0x13003c30, 0x13003c51, 0x1b117e00, 0x53107c00}, "sxth W16, W1|sxth W17, W2|mul W0, W16, W17|ubfx W0, W0, #16, #16"},
		{32, []uint32{0x9b227c20, 0xd360fc00}, "smull X0, W1, W2|lsr X0, X0, #32"},
		{64, []uint32{0x9b427c20}, "smulh X0, X1, X2"},
	} {
		e := NewByteAssembler(abi.StackPlatform, 0)
		e.MulHighSigned(test.w, 0, 1, 2)
		if e.Err() != nil || len(e.words) != len(test.words) {
			t.Fatalf("%d bits: %v, %d words", test.w, e.Err(), len(e.words))
		}
		for i, word := range test.words {
			if e.words[i] != word {
				t.Errorf("%d bits word %d = %#08x, want %#08x", test.w, i, e.words[i], word)
			}
		}
		text := NewTextAssembler(abi.StackPlatform, 0)
		text.MulHighSigned(test.w, 0, 1, 2)
		var lines []string
		for _, line := range strings.Split(strings.TrimSpace(text.Text()), "\n") {
			lines = append(lines, strings.TrimSpace(line[strings.Index(line, " "):]))
		}
		if got := strings.Join(lines, "|"); got != test.text {
			t.Errorf("%d bits text %q, want %q", test.w, got, test.text)
		}
	}
	e := NewByteAssembler(abi.StackPlatform, 0)
	e.MulHighSigned(24, 0, 1, 2)
	if e.Err() == nil {
		t.Error("24-bit signed high product accepted")
	}
}
