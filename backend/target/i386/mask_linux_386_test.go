//go:build linux && 386

package i386_test

import (
	"math/bits"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
)

// maskSeed is the immediate the test functions compare their first input
// with before the form runs, so that flags the form keeps are known.
const maskSeed = 0x80

// TestMaskExecution runs every masked 8- and 16-bit form, with its AND and
// with its PreserveFlags MOVZX, on byte and other registers, then reads the
// flags with CMOVcc: the result is Go's, a flag FlagFacts leaves unmodified
// is the seed compare's, a flag fact holds, and an operation before MOVZX
// leaves the zero of its unmasked 32-bit result.
func TestMaskExecution(t *testing.T) {
	inputs := []uint32{0, 1, 0x7f, 0x80, 0xff, 0x100, 0x7fff, 0x8000, 0xffff, 0x10000, 0x12345678, 0x7fffffff, 0x80000000, 0xffffffff}
	regs := []backend.Register{0, 1, 2, 3, 5, 6, 7}
	var forms []machine.Instruction
	for _, w := range []uint8{8, 16} {
		for _, a := range regs {
			for _, b := range []backend.Register{a, regs[(int(a)+3)%len(regs)]} {
				for _, c := range []backend.Register{b, regs[(int(a)+5)%len(regs)]} {
					if a == c && a != b {
						continue // the move would overwrite C
					}
					for _, o := range []op.Opcode{op.OpAdd, op.OpSub, op.OpAnd, op.OpOr, op.OpXor, op.OpMul} {
						forms = append(forms, machine.Instruction{Op: machine.OpBinary, Arithmetic: o, Bits: w, A: a, B: b, C: c})
					}
				}
				for _, o := range []op.Opcode{op.OpShiftLeft, op.OpShiftRight, op.OpRotateLeft, op.OpRotateRight} {
					for _, n := range []uint64{0, 1, 3, uint64(w) - 1, 32, 33} {
						forms = append(forms, machine.Instruction{Op: machine.OpShift, Arithmetic: o, Bits: w, A: a, B: b, Imm: n})
					}
				}
				forms = append(forms,
					machine.Instruction{Op: machine.OpNarrow, A: a, B: b, Imm: uint64(w)},
					machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAnd, Bits: 32, A: a, B: b, Imm: uint64(1)<<w - 1})
			}
		}
	}
	ran := 0
	for _, form := range forms {
		preserved := form
		preserved.PreserveFlags = true
		for _, ins := range []machine.Instruction{form, preserved} {
			if ins.PreserveFlags && i386.PhysicalSize(ins, abi.StackPlatform, 0) == 0 {
				continue // no byte register, or a narrowing (the elider rewrites it as its AND)
			}
			code := maskFunction(t, ins)
			ran++
			facts := i386.FlagFacts(ins)
			for _, x := range inputs {
				for _, y := range inputs[:6] {
					lo, hi, err := code.CallPair(uintptr(x), uintptr(y))
					if err != nil {
						t.Fatal(err)
					}
					if ins.C == ins.B {
						y = x
					}
					want, full, zero := maskReference(ins, x, y)
					if uint32(lo) != want {
						t.Fatalf("%+v: f(%#x, %#x) = %#x, want %#x", ins, x, y, lo, want)
					}
					z, c, s := hi&1 != 0, hi&2 != 0, hi&4 != 0
					seed := x - maskSeed
					for _, f := range []struct {
						flag machine.FlagMask
						got  bool
						seed bool
					}{{machine.FlagZero, z, seed == 0}, {machine.FlagCarry, c, x < maskSeed}, {machine.FlagSign, s, int32(seed) < 0}} {
						if facts.Modified&f.flag == 0 && f.got != f.seed {
							t.Fatalf("%+v: f(%#x, %#x) changes flag %v that FlagFacts keeps", ins, x, y, f.flag)
						}
					}
					if facts.Zero.Valid && z != (want == 0) || facts.Sign.Valid && s != (int32(want) < 0) {
						t.Fatalf("%+v: f(%#x, %#x) = %#x: flags %03b break facts %+v", ins, x, y, want, hi, facts)
					}
					if ins.PreserveFlags && zero && z != (full == 0) {
						t.Fatalf("%+v: f(%#x, %#x): ZF %v, 32-bit result %#x", ins, x, y, z, full)
					}
				}
			}
			code.Close()
		}
	}
	if ran < 1000 {
		t.Fatalf("ran only %d forms", ran)
	}
}

// maskFunction is f(x, y): x in B, y in C, the flags of CMP B, maskSeed,
// then ins; it returns A and the ZF, CF and SF bits read after ins.
func maskFunction(t *testing.T, ins machine.Instruction) *execution.Code {
	t.Helper()
	e := i386.NewByteAssembler(abi.StackPlatform)
	e.FrameEnter(expansionFrame)
	e.IncomingWord(ins.B, 4)
	if ins.Op == machine.OpBinary && ins.C != ins.B {
		e.IncomingWord(ins.C, 8)
	}
	e.CompareImmediate(ins.B, maskSeed)
	steps := []machine.Instruction{ins, {Op: machine.OpSpill, A: ins.A, Slot: 0}, {Op: machine.OpConst, A: 1, Imm: 1}, {Op: machine.OpConst, A: 2}}
	for slot, cond := range []op.BranchCondition{op.BranchEqual, op.BranchBelow, machine.BranchSign} {
		steps = append(steps, machine.Instruction{Op: machine.OpSelectFlags, Condition: cond, A: 0, C: 1, D: 2}, machine.Instruction{Op: machine.OpSpill, A: 0, Slot: slot + 1})
	}
	steps = append(steps,
		machine.Instruction{Op: machine.OpReload, A: 0, Slot: 0},
		machine.Instruction{Op: machine.OpReload, A: 2, Slot: 1},
		machine.Instruction{Op: machine.OpReload, A: 1, Slot: 2},
		machine.Instruction{Op: machine.OpShift, Arithmetic: op.OpShiftLeft, Bits: 32, A: 1, B: 1, Imm: 1},
		machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpOr, Bits: 32, A: 2, B: 2, C: 1},
		machine.Instruction{Op: machine.OpReload, A: 1, Slot: 3},
		machine.Instruction{Op: machine.OpShift, Arithmetic: op.OpShiftLeft, Bits: 32, A: 1, B: 1, Imm: 2},
		machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpOr, Bits: 32, A: 2, B: 2, C: 1})
	for _, i := range steps {
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
	return code
}

// maskReference returns ins's masked result, the 32-bit result of the
// operation before the mask, and whether that operation sets ZF from it.
func maskReference(ins machine.Instruction, x, y uint32) (uint32, uint32, bool) {
	w := ins.Bits
	if ins.Op == machine.OpNarrow {
		w = uint8(ins.Imm)
	}
	if ins.Op == machine.OpBinaryImmediate {
		w = 8
		if ins.Imm == 0xffff {
			w = 16
		}
	}
	mask := uint32(1)<<w - 1
	n := uint(ins.Imm & 31)
	var full uint32
	zero := true
	switch ins.Op {
	case machine.OpNarrow, machine.OpBinaryImmediate:
		full, zero = x, false
	case machine.OpShift:
		zero = n != 0 && ins.Arithmetic != op.OpRotateLeft && ins.Arithmetic != op.OpRotateRight
		switch ins.Arithmetic {
		case op.OpShiftLeft:
			full = x << n
		case op.OpShiftRight:
			full = x >> n
		case op.OpRotateLeft:
			full = bits.RotateLeft32(x, int(n))
		case op.OpRotateRight:
			full = bits.RotateLeft32(x, -int(n))
		}
	default:
		zero = ins.Arithmetic != op.OpMul // IMUL leaves ZF undefined
		switch ins.Arithmetic {
		case op.OpAdd:
			full = x + y
		case op.OpSub:
			full = x - y
		case op.OpAnd:
			full = x & y
		case op.OpOr:
			full = x | y
		case op.OpXor:
			full = x ^ y
		case op.OpMul:
			full = x * y
		}
		if ins.PreserveFlags && ins.Arithmetic == op.OpAdd && ins.A != ins.B {
			zero = false // LEA
		}
	}
	return full & mask, full, zero
}
