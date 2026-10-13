package ppc64_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
)

func TestNarrowMemoryEncoding(t *testing.T) {
	ops := []machine.Opcode{machine.OpLoad16, machine.OpStore16, machine.OpLoad32, machine.OpStore32}
	want := []uint32{0xa0220000, 0xb0410000, 0x80220000, 0x90410000}
	e := emitterFor(t, "ppc64le", abi.StackNoUse)
	for k, op := range ops {
		seq, err := e.Expand(machine.Instruction{Op: op, A: 1, B: 2}, 0)
		if err != nil || len(seq) != 1 {
			t.Fatalf("seq%v err%v", seq, err)
		}
		w, err := ppc64.Encode(seq[0])
		if err != nil || w != want[k] {
			t.Fatalf("got%x want%x err%v", w, want[k], err)
		}
		for _, disp := range []int64{-32768, -1, 1, 32767} {
			i := seq[0]
			i.Imm = disp
			w, err := ppc64.Encode(i)
			if err != nil || w != want[k]|uint32(uint16(disp)) {
				t.Fatalf("disp%d got%x err%v", disp, w, err)
			}
		}
		for _, disp := range []int64{-32769, 32768} {
			i := seq[0]
			i.Imm = disp
			if _, err := ppc64.Encode(i); err == nil {
				t.Fatal("accepted overflowing displacement")
			}
		}
	}
}
