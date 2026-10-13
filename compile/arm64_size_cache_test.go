package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	targetarm64 "github.com/moriyoshi/nyanjit/backend/target/arm64"
)

func TestARM64LiteralRelaxationReusesMeasuredSizes(t *testing.T) {
	instructions := []machine.Instruction{
		{Op: machine.OpLiteralLoad, A: backend.VectorRegister(1), Bits: 128, Data: make([]byte, 16)},
		{Op: machine.OpMove, A: 0, B: 1},
		{Op: machine.OpReturn},
	}
	for _, function := range []bool{false, true} {
		calls := 0
		size := func(i machine.Instruction) int {
			calls++
			return targetarm64.PhysicalSize(i, abi.StackNoUse, 0)
		}
		if function {
			p := physicalProgram{}
			for _, i := range instructions {
				p.insns = append(p.insns, locatedInsn{ins: i})
			}
			if err := relaxFunctionLiterals(&p, &physicalEmitter{size: size}, "arm64"); err != nil {
				t.Fatal(err)
			}
			if p.codeBytes != 12 {
				t.Fatalf("function layout bytes = %d", p.codeBytes)
			}
		} else {
			s := machine.NewStream(machine.StreamOptions{Size: size, FlagEffect: func(machine.Instruction) bool { return false }})
			for _, i := range instructions {
				s.Append(i)
			}
			if _, err := relaxLiteralStream(s, nil, nil, "arm64"); err != nil {
				t.Fatal(err)
			}
		}
		// Append measures once. No load needs the long form, so nothing is
		// committed, and reading the positions and reconstructing the final
		// program reuse caches.
		if want := len(instructions); calls != want {
			t.Fatalf("function=%v size calls = %d, want %d", function, calls, want)
		}
	}
}
