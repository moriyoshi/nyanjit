package riscv64

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

func TestConstantWordSizesAndText(t *testing.T) {
	for _, test := range []struct {
		value uint64
		words int
	}{
		{0, 1},
		{2047, 1},
		{^uint64(2047), 1},
		{2048, 2},
		{0x7fffffff, 2},
		{^uint64(0x7fffffff), 1},
	} {
		words := ConstantWords(10, test.value)
		if len(words) != test.words {
			t.Errorf("constant %#x uses %d words, want %d", test.value, len(words), test.words)
		}
		ins := machine.Instruction{Op: machine.OpConst, A: 10, Imm: test.value}
		if got := PhysicalSize(ins, abi.StackPlatform, 0); got != len(words)*4 {
			t.Errorf("constant %#x physical size = %d, want %d", test.value, got, len(words)*4)
		}
		a := NewTextAssembler(abi.StackPlatform, 0)
		a.Constant(10, test.value)
		if strings.Contains(a.Text(), ".word ") {
			t.Errorf("constant %#x has undecoded listing:\n%s", test.value, a.Text())
		}
	}
}
