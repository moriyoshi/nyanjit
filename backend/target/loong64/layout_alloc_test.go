package loong64

import (
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// TestSizeQueriesReuseAssemblers checks that sizing and implicit-write
// queries borrow pooled assemblers, allocating less than a fresh assembler
// does (the plan's listing arguments may still box large immediates), and
// that a borrowed assembler keeps nothing from an earlier query, including a
// rejected layout.
func TestSizeQueriesReuseAssemblers(t *testing.T) {
	for _, tc := range []struct {
		i    machine.Instruction
		want []backend.Register
	}{
		{machine.Instruction{Op: machine.OpMove, A: 4, B: 5}, nil},
		{machine.Instruction{Op: machine.OpConst, A: 4, Imm: 0x123456789abcdef0}, nil},
		{machine.Instruction{Op: machine.OpSelect, A: 4, B: 5, C: 6, D: 7}, []backend.Register{19, 20}},
		{machine.Instruction{Op: machine.OpStoreOffset, A: 4, B: 5, Disp: 8192}, []backend.Register{19}},
	} {
		size := PhysicalSize(tc.i, abi.StackPlatform, 0)
		fresh := NewByteAssembler(abi.StackPlatform, 0)
		if err := machine.Visit(tc.i, fresh); err != nil || size == 0 || size != fresh.AssembledSize() {
			t.Fatalf("op%d size %d, fresh assembler %d (%v)", tc.i.Op, size, fresh.AssembledSize(), err)
		}
		unpooled := testing.AllocsPerRun(100, func() {
			e := NewByteAssembler(abi.StackPlatform, 0)
			e.sizeOnly = true
			machine.Visit(tc.i, e)
		})
		if tc.want == nil {
			if n := testing.AllocsPerRun(100, func() { ImplicitWrites(tc.i, abi.StackPlatform, 0) }); n >= unpooled {
				t.Fatalf("op%d implicit-write query allocates %g, a fresh assembler %g", tc.i.Op, n, unpooled)
			}
		}
		if n := testing.AllocsPerRun(100, func() { PhysicalSize(tc.i, abi.StackPlatform, 0) }); n >= unpooled {
			t.Fatalf("op%d size query allocates %g, a fresh assembler %g", tc.i.Op, n, unpooled)
		}
		if PhysicalSize(tc.i, abi.StackPlatform, 5000) != 0 || ImplicitWrites(tc.i, abi.StackPlatform, 5000) != nil {
			t.Fatalf("op%d accepted an invalid home count", tc.i.Op)
		}
		for range 2 {
			if got := PhysicalSize(tc.i, abi.StackPlatform, 0); got != size {
				t.Fatalf("op%d size %d after earlier queries, want %d", tc.i.Op, got, size)
			}
			if got := ImplicitWrites(tc.i, abi.StackPlatform, 0); !slices.Equal(got, tc.want) {
				t.Fatalf("op%d writes %v after earlier queries, want %v", tc.i.Op, got, tc.want)
			}
		}
	}
}
