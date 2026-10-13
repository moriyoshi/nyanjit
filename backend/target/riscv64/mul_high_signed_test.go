package riscv64

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

func machineMulHighSigned(w uint8) machine.Instruction {
	return machine.Instruction{Op: machine.OpMulHighSigned, Bits: w, A: 10, B: 11, C: 12}
}

// The SLLI and MULH words match the Go assembler's encodings.
func TestMulHighSignedEncoding(t *testing.T) {
	for _, test := range []struct {
		w     uint8
		words []uint32
		text  string
	}{
		{8, []uint32{0x03859f13, 0x03861f93, 0x03ff1533, 0x03855513}, "slli x30, x11, 56|slli x31, x12, 56|mulh x10, x30, x31|srli x10, x10, 56"},
		{32, []uint32{0x02059f13, 0x02061f93, 0x03ff1533, 0x02055513}, "slli x30, x11, 32|slli x31, x12, 32|mulh x10, x30, x31|srli x10, x10, 32"},
		{64, []uint32{0x02c59533}, "mulh x10, x11, x12"},
	} {
		e := NewByteAssembler(abi.StackPlatform, 0)
		e.MulHighSigned(test.w, 10, 11, 12)
		if e.Err() != nil || len(e.words) != len(test.words) {
			t.Fatalf("%d bits: %v, %d words", test.w, e.Err(), len(e.words))
		}
		for i, word := range test.words {
			if e.words[i] != word {
				t.Errorf("%d bits word %d = %#08x, want %#08x", test.w, i, e.words[i], word)
			}
		}
		text := NewTextAssembler(abi.StackPlatform, 0)
		text.MulHighSigned(test.w, 10, 11, 12)
		var lines []string
		for _, line := range strings.Split(strings.TrimSpace(text.Text()), "\n") {
			lines = append(lines, strings.TrimSpace(line[strings.Index(line, " "):]))
		}
		if got := strings.Join(lines, "|"); got != test.text {
			t.Errorf("%d bits text %q, want %q", test.w, got, test.text)
		}
	}
	e := NewByteAssembler(abi.StackPlatform, 0)
	e.MulHighSigned(12, 10, 11, 12)
	if e.Err() == nil {
		t.Error("12-bit signed high product accepted")
	}
	if got := ImplicitWrites(machineMulHighSigned(16), abi.StackPlatform, 0); len(got) != 2 {
		t.Errorf("narrow implicit writes %v, want x30 and x31", got)
	}
	if got := ImplicitWrites(machineMulHighSigned(64), abi.StackPlatform, 0); len(got) != 0 {
		t.Errorf("64-bit implicit writes %v", got)
	}
}
