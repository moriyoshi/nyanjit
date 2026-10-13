package machine_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
)

func TestI386InvalidOperands(t *testing.T) {
	cases := []machine.Instruction{{Op: machine.OpJumpIf, Condition: 200}}
	for _, operation := range []machine.Opcode{machine.OpConst, machine.OpBinaryCarry, machine.OpMulWide, machine.OpFloatBinary, machine.OpMulHigh, machine.OpCarryBit, machine.OpWordCarry, machine.OpCompareRegister} {
		cases = append(cases, machine.Instruction{Op: operation, Arithmetic: op.OpAdd, Bits: 32, A: 255, B: 255, C: 255, D: 255, E: 255})
	}
	for _, ins := range cases {
		t.Run(fmt.Sprintf("op%d", ins.Op), func(t *testing.T) {
			for _, text := range []bool{false, true} {
				t.Run(fmt.Sprint(text), func(t *testing.T) {
					defer func() {
						if p := recover(); p != nil {
							t.Errorf("encoder panicked: %v", p)
						}
					}()
					var a machine.Assembler = i386.NewByteAssembler(abi.StackPlatform)
					if text {
						a = i386.NewTextAssembler(abi.StackPlatform)
					}
					if err := machine.Visit(ins, a); !errors.Is(err, machine.ErrInvalidInstruction) {
						t.Fatalf("invalid operand: %v", err)
					}
					if err := machine.Visit(machine.Instruction{Op: machine.OpReturn}, a); !errors.Is(err, machine.ErrInvalidInstruction) {
						t.Fatalf("error not sticky: %v", err)
					}
				})
			}
			t.Run("size", func(t *testing.T) {
				defer func() {
					if p := recover(); p != nil {
						t.Errorf("sizing panicked: %v", p)
					}
				}()
				if n := i386.PhysicalSize(ins, abi.StackPlatform, 0); n != 0 {
					t.Fatalf("invalid size: %d", n)
				}
				if ins.Op == machine.OpFloatBinary && i386.FloatSize(ins) != 0 {
					t.Fatal("invalid FloatSize accepted")
				}
			})
		})
	}
}
