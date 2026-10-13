package compile

import (
	"errors"
	"math/rand"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// Text assemblers validate operations with the target's byte encoder and report
// rejections through Err. Arbitrary operands must not panic, frame
// adjustments stay within the Assembler bound, and a branch is checked at the
// listing's own pc.
func TestTextAssemblersAcceptArbitraryOperands(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for _, arch := range []string{"amd64", "386", "arm64", "riscv64", "loong64"} {
		options := mustTarget(t, TargetSpec{Architecture: arch}).options(Func{Stack: StackPolicy{Practice: abi.StackPlatform}})
		for _, op := range []machine.Opcode{machine.OpFrameEnter, machine.OpFrameLeave} {
			assembler := newMachineAssembler(options, emitText).(machine.Assembler)
			if err := machine.Visit(machine.Instruction{Op: op, Imm: 7e18}, assembler); !errors.Is(err, machine.ErrInvalidInstruction) {
				t.Fatalf("%s %d with a 7e18-byte frame: %v", arch, op, err)
			}
		}
		bad := machine.Instruction{Op: machine.OpMove, A: 200, B: 0}
		if err := machine.Visit(bad, newMachineAssembler(options, emitText).(machine.Assembler)); !errors.Is(err, machine.ErrInvalidInstruction) {
			t.Fatalf("%s move from register 200: %v", arch, err)
		}
		// amd64 and arm64 listings advance their own pc, so a listing driven
		// without SetPC validates branches there, not at pc 0.
		branches := []machine.Instruction{
			{Op: machine.OpJumpIf, Condition: ir.BranchEqual},
			{Op: machine.OpLoop, A: 1},
		}
		if arch != "amd64" && arch != "arm64" {
			branches = nil
		}
		for _, branch := range branches {
			near := newMachineAssembler(options, emitText).(machine.Assembler)
			far := newMachineAssembler(options, emitText).(machine.Assembler)
			for far.AssembledSize() < 1<<20+64 {
				if err := machine.Visit(machine.Instruction{Op: machine.OpMove, A: 1, B: 2}, far); err != nil {
					t.Fatalf("%s move: %v", arch, err)
				}
			}
			short := branch
			short.Imm = uint64(near.AssembledSize() + 8)
			want := machine.Visit(short, near)
			short.Imm = uint64(far.AssembledSize() + 8)
			if got := machine.Visit(short, far); (got == nil) != (want == nil) {
				t.Fatalf("%s short %d at pc %#x: %v, at pc 0: %v", arch, branch.Op, far.AssembledSize(), got, want)
			}
		}
		for n := 0; n < 20000; n++ {
			reg := func() backend.Register {
				if r.Intn(4) == 0 {
					return backend.Register(r.Intn(256))
				}
				return backend.Register(r.Intn(40))
			}
			ins := machine.Instruction{
				Op: machine.Opcode(r.Intn(60)), Arithmetic: ir.Opcode(r.Intn(70)), Bits: []uint8{0, 8, 16, 32, 64, 128}[r.Intn(6)],
				A: reg(), B: reg(), C: reg(), Imm: uint64(r.Intn(1 << 21)), Disp: uint64(r.Intn(1 << 16)), Condition: ir.BranchCondition(r.Intn(12)),
			}
			func() {
				defer func() {
					if p := recover(); p != nil {
						t.Fatalf("%s %+v: %v", arch, ins, p)
					}
				}()
				_ = machine.Visit(ins, newMachineAssembler(options, emitText).(machine.Assembler))
			}()
		}
	}
}
