//go:build linux && 386

package i386_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
)

// TestLEAExecution runs each flag-preserving form beside its flag-writing
// form, on every register assignment: ESP and EBP bases, the operand swap
// that keeps ESP out of the index, negative and wrapping displacements and
// the unmasked 8- and 16-bit immediate forms, and the masked 8- and 16-bit
// additions, which zero-extend the LEA with MOVZX. The function returns the
// LEA result in EAX and the flag-writing result in EDX; with neither operand
// ESP, both must also equal the arithmetic.
func TestLEAExecution(t *testing.T) {
	inputs := []uint32{0, 1, 0x7fffffff, 0x80000000, 0xffffffff, 0x12345678}
	immediates := []uint64{0, 1, 0x7f, 0x80, 0xffffff80, 0xffffff7f, 0x7fffffff, 0x80000000, 0xffffffff, 0x1000}
	var forms []machine.Instruction
	for a := backend.Register(0); a < 8; a++ {
		if a == 4 {
			continue
		}
		for b := backend.Register(0); b < 8; b++ {
			if a == b {
				continue
			}
			for c := backend.Register(0); c < 8; c++ {
				if a != c && !(b == 4 && c == 4) {
					forms = append(forms, machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 32, A: a, B: b, C: c})
				}
			}
			for _, o := range []op.Opcode{op.OpAdd, op.OpSub} {
				for _, imm := range immediates {
					forms = append(forms, machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: o, Bits: 32, A: a, B: b, Imm: imm})
				}
			}
			forms = append(forms, machine.Instruction{Op: machine.OpInc, A: a, B: b}, machine.Instruction{Op: machine.OpDec, A: a, B: b})
		}
	}
	for _, w := range []uint8{0, 8, 16} {
		forms = append(forms,
			machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: w, A: 0, B: 1, C: 2},
			machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAdd, Bits: w, A: 0, B: 1, Imm: 0xff},
			machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpSub, Bits: w, A: 0, B: 1, Imm: 0x8001})
	}
	for _, ins := range forms {
		lea := ins
		lea.PreserveFlags = true
		masked := ins.Op == machine.OpBinary && (ins.Bits == 8 || ins.Bits == 16)
		e := i386.NewByteAssembler(abi.StackPlatform)
		e.FrameEnter(expansionFrame)
		if ins.B != 4 {
			e.IncomingWord(ins.B, 4)
		}
		if ins.Op == machine.OpBinary && ins.C != 4 && ins.C != ins.B {
			e.IncomingWord(ins.C, 8)
		}
		for _, i := range []machine.Instruction{lea, {Op: machine.OpSpill, A: ins.A, Slot: 0}, ins, {Op: machine.OpSpill, A: ins.A, Slot: 1}, {Op: machine.OpReload, A: 0, Slot: 0}, {Op: machine.OpReload, A: 2, Slot: 1}} {
			if err := machine.Visit(i, e); err != nil {
				t.Fatalf("%+v: %v", i, err)
			}
		}
		e.FrameLeave(expansionFrame)
		e.Ret()
		if e.Err() != nil {
			t.Fatalf("%+v: %v", ins, e.Err())
		}
		code, err := execution.NewCode(e.Bytes(), execution.CodeOptions{ABI: abi.C, Arity: 2, ArgumentWords: 2}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range inputs {
			for _, y := range inputs {
				lo, hi, err := code.CallPair(uintptr(x), uintptr(y))
				if err != nil {
					t.Fatal(err)
				}
				if lo != hi {
					t.Fatalf("%+v: f(%#x, %#x) LEA %#x, flag-writing form %#x", ins, x, y, lo, hi)
				}
				if ins.B == 4 || ins.Op == machine.OpBinary && ins.C == 4 {
					continue
				}
				c := y
				if ins.C == ins.B {
					c = x
				}
				want := map[machine.Opcode]uint32{
					machine.OpBinary:          x + c,
					machine.OpBinaryImmediate: x + uint32(ins.Imm),
					machine.OpInc:             x + 1,
					machine.OpDec:             x - 1,
				}[ins.Op]
				if ins.Op == machine.OpBinaryImmediate && ins.Arithmetic == op.OpSub {
					want = x - uint32(ins.Imm)
				}
				if masked {
					want &= uint32(1)<<ins.Bits - 1 // LEA, then MOVZX
				}
				if uint32(lo) != want {
					t.Fatalf("%+v: f(%#x, %#x) = %#x, want %#x", ins, x, y, lo, want)
				}
			}
		}
		code.Close()
	}
}
