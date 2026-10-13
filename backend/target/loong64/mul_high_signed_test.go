package loong64

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// The words match the Go assembler's MULHV, MULH, SLLV and SRLV encodings.
func TestMulHighSignedEncoding(t *testing.T) {
	for _, test := range []struct {
		w     uint8
		words []uint32
		text  string
	}{
		{8, []uint32{0x0041e0b3, 0x0041e0d4, 0x001e5264, 0x0045e084}, "slli.d r19, r5, 56|slli.d r20, r6, 56|mulh.d r4, r19, r20|srli.d r4, r4, 56"},
		{32, []uint32{0x001c98a4, 0x00df0084}, "mulh.w r4, r5, r6|bstrpick.d r4, r4, 31, 0"},
		{64, []uint32{0x001e18a4}, "mulh.d r4, r5, r6"},
	} {
		e := NewTextAssembler(abi.StackPlatform, 0)
		e.MulHighSigned(test.w, 4, 5, 6)
		code := e.Bytes()
		if e.Err() != nil || len(code) != 4*len(test.words) {
			t.Fatalf("%d bits: %v, %d bytes", test.w, e.Err(), len(code))
		}
		for i, word := range test.words {
			if got := binary.LittleEndian.Uint32(code[4*i:]); got != word {
				t.Errorf("%d bits word %d = %#08x, want %#08x", test.w, i, got, word)
			}
		}
		var lines []string
		for _, line := range strings.Split(strings.TrimSpace(e.Text()), "\n") {
			lines = append(lines, strings.TrimSpace(line[strings.Index(line, " "):]))
		}
		if got := strings.Join(lines, "|"); got != test.text {
			t.Errorf("%d bits text %q, want %q", test.w, got, test.text)
		}
		writes := ImplicitWrites(machine.Instruction{Op: machine.OpMulHighSigned, Bits: test.w, A: 4, B: 5, C: 6}, abi.StackPlatform, 0)
		if test.w < 32 && len(writes) != 2 || test.w >= 32 && len(writes) != 0 {
			t.Errorf("%d bits implicit writes %v", test.w, writes)
		}
	}
}
