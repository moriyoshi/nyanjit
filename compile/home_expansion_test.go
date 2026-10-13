package compile

import (
	"fmt"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// TestHomeExpansionCoversSelection keeps variable-home planning in step with
// instruction selection and hardening. Each fixed register written by a
// selected sequence, including software fallbacks and blinded immediates, must
// be excluded from variable homes.
func TestHomeExpansionCoversSelection(t *testing.T) {
	type shape struct {
		name string
		ops  []ir.Instruction
	}
	param := func(bits uint8) ir.Instruction { return ir.Instruction{Op: ir.OpParam, Bits: bits} }
	var shapes []shape
	add := func(name string, ops ...ir.Instruction) { shapes = append(shapes, shape{name, ops}) }
	for _, w := range []uint8{8, 16, 32, 64} {
		for _, op := range []ir.Opcode{ir.OpMulHigh, ir.OpMul, ir.OpCarryAdd, ir.OpBorrowSub,
			ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy} {
			add(fmt.Sprintf("%s/%d", op, w), param(w), param(w), ir.Instruction{Op: op, Bits: w, A: 0, B: 1})
		}
		for _, op := range []ir.Opcode{ir.OpLeadingZeros, ir.OpTrailingZeros} {
			add(fmt.Sprintf("%s/%d", op, w), param(w), ir.Instruction{Op: op, Bits: w, A: 0})
		}
		for _, op := range []ir.Opcode{ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight} {
			add(fmt.Sprintf("%s/%d", op, w), param(w), ir.Instruction{Op: op, Bits: w, A: 0, Imm: 3})
		}
		for _, op := range []ir.Opcode{ir.OpAddWithCarry, ir.OpSubWithBorrow} {
			add(fmt.Sprintf("%s/%d", op, w), param(w), param(w), ir.Instruction{Op: ir.OpCarryAdd, Bits: w, A: 0, B: 1},
				ir.Instruction{Op: op, Bits: w, A: 0, B: 1, C: 2})
		}
		add(fmt.Sprintf("OpSelect/condition%d", w), param(w), param(64), param(64), ir.Instruction{Op: ir.OpSelect, Bits: 64, A: 0, B: 1, C: 2})
		add(fmt.Sprintf("OpSelect/value%d", w), param(32), param(w), param(w), ir.Instruction{Op: ir.OpSelect, Bits: w, A: 0, B: 1, C: 2})
		for scale := uint64(1); scale <= 17; scale++ {
			add(fmt.Sprintf("OpAddScaled/%d/%d", w, scale), param(w), param(w), ir.Instruction{Op: ir.OpAddScaled, Bits: w, A: 0, B: 1, Imm: scale})
		}
	}
	for _, a := range []uint8{32, 64} {
		for _, scale := range []uint64{1, 3, 8} {
			add(fmt.Sprintf("load-indexed/%d/%d", a, scale), param(a), param(a),
				ir.Instruction{Op: ir.OpAddScaled, Bits: a, A: 0, B: 1, Imm: scale}, ir.Instruction{Op: ir.OpLoad64, Bits: 64, A: 2})
		}
		add(fmt.Sprintf("load-offset/%d", a), param(a), ir.Instruction{Op: ir.OpConst, Bits: a, Imm: 24},
			ir.Instruction{Op: ir.OpAdd, Bits: a, A: 0, B: 1}, ir.Instruction{Op: ir.OpLoad64, Bits: 64, A: 2})
		add(fmt.Sprintf("store-offset/%d", a), param(a), param(64), ir.Instruction{Op: ir.OpConst, Bits: a, Imm: 24},
			ir.Instruction{Op: ir.OpAdd, Bits: a, A: 0, B: 2}, ir.Instruction{Op: ir.OpStore64, Bits: 64, A: 3, B: 1})
	}
	for _, arch := range []string{"amd64", "386", "arm64", "riscv64", "ppc64le", "loong64"} {
		for _, stack := range []abi.StackPractice{abi.StackPlatform, abi.StackNoUse} {
			for _, harden := range []bool{false, true} {
				target := newMachineTarget(mustTarget(t, TargetSpec{Architecture: arch}).options(Func{Stack: StackPolicy{Practice: stack}, Harden: harden}))
				writes := target.(interface {
					ImplicitWrites(machine.Instruction) []physReg
				})
				var candidate [256]bool
				for _, r := range allocationRegisters(target, nil, stack) {
					candidate[r] = true
				}
				for _, r := range preservedAllocatable(target) {
					candidate[r] = true
				}
				selected := 0
				for _, s := range shapes {
					arity := 0
					for _, i := range s.ops {
						if i.Op == ir.OpParam {
							arity++
						}
					}
					result := len(s.ops) - 1
					if s.ops[result].Op == ir.OpStore64 {
						result = 0
					}
					program, err := testSelectProgram(s.ops, nil, arity, result, nil, abi.MaxArgumentsFor(arch), target)
					if err != nil {
						continue
					}
					if harden {
						if program, err = blindSelected(program, target, zeroReader{}); err != nil {
							t.Fatal(err)
						}
					}
					selected++
					var planned [256]bool
					block := func(physical machine.Instruction) {
						for _, r := range writes.ImplicitWrites(physical) {
							planned[r] = true
						}
					}
					var expansion [2]machine.Instruction
					for n := range s.ops {
						count := homeExpansion(target, &ir.Program{Instructions: s.ops}, &s.ops[n], &expansion)
						for _, physical := range expansion[:count] {
							block(physical)
						}
					}
					if harden {
						for _, physical := range hardenedHomeExpansion(target) {
							block(physical)
						}
					}
					for _, ins := range program.Instructions {
						physical, ok := selectedPhysical(target, program, ins)
						if !ok {
							continue
						}
						for _, r := range writes.ImplicitWrites(physical) {
							if candidate[r] && !planned[r] {
								t.Errorf("%s stack %d harden %v %s: selected %+v writes register %d outside the planned variable-home exclusions", arch, stack, harden, s.name, ins, r)
							}
						}
					}
				}
				if selected < len(shapes)/2 {
					t.Fatalf("%s stack %d harden %v selected only %d of %d shapes", arch, stack, harden, selected, len(shapes))
				}
			}
		}
	}
}

type zeroReader struct{}

func (zeroReader) Read(b []byte) (int, error) {
	clear(b)
	return len(b), nil
}

// selectedPhysical describes a selected instruction the way physical lowering
// presents it to ImplicitWrites.
func selectedPhysical(target machineTarget, program machine.SelectedProgram, ins machine.SelectedInstruction) (machine.Instruction, bool) {
	physical := machine.Instruction{Arithmetic: ins.Arithmetic, Bits: ins.Bits}
	switch ins.Op {
	case machine.SelectedSelect:
		physical.Op, physical.Arithmetic, physical.Bits = machine.OpSelect, ir.OpSelect, uint8(target.RegisterBits())
		if program.Values[ins.Dst].Words == 2 || program.Values[ins.A].Words == 2 {
			physical.Bits = 64
		}
	case machine.SelectedBinary:
		physical.Op = machine.OpBinary
		if target.RegisterBits() == 32 && program.Values[ins.Dst].Words == 2 {
			physical.Bits = 64
		}
	case machine.SelectedMulHigh:
		physical.Op = machine.OpMulHigh
	case machine.SelectedShiftBy:
		physical.Op = machine.OpShiftBy
	case machine.SelectedCarryBit:
		physical.Op = machine.OpCarryBit
	case machine.SelectedWordCarry:
		physical.Op = machine.OpWordCarry
	case machine.SelectedCountZeros:
		physical.Op = machine.OpCountZeros
	default:
		return physical, false
	}
	if physical.Bits == 0 {
		physical.Bits = 64
	}
	return physical, true
}
