package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestScaledArithmeticNarrowsForWiderTarget(t *testing.T) {
	for _, cpu := range []abi.PPC64CPU{abi.PPC64Power8, abi.PPC64Power9, abi.PPC64Power10} {
		profile, err := abi.NewPPC64Profile("ppc64le", abi.C)
		if err != nil {
			t.Fatal(err)
		}
		policy, err := ppc64.NewPolicyForCPU(profile, abi.StackNoUse, cpu)
		if err != nil {
			t.Fatal(err)
		}
		for _, width := range []uint8{8, 16, 32} {
			for _, scale := range []uint64{0, 1, 2, 7, ^uint64(0)} {
				for _, displacement := range []uint64{0, 9, ^uint64(0)} {
					ops := []ir.Instruction{
						{Op: op.OpParam, Bits: width},
						{Op: op.OpParam, Bits: width},
						{Op: op.OpAddScaled, A: 0, B: 1, Imm: scale, Bits: width},
					}
					result := 2
					if displacement != 0 {
						ops = append(ops, ir.Instruction{Op: op.OpConst, Imm: displacement & (^uint64(0) >> (64 - width)), Bits: width},
							ir.Instruction{Op: op.OpAdd, A: 2, B: 3, Bits: width})
						result = 4
					}
					selected, err := testSelectProgram(ops, nil, 2, result, nil, 8, policy)
					if err != nil {
						t.Fatalf("CPU %s width %d scale %#x displacement %#x: %v", cpu, width, scale, displacement, err)
					}
					last := selected.Instructions[len(selected.Instructions)-1]
					if last.Op != machine.SelectedNarrow || last.Dst != result || last.Imm != uint64(width) {
						t.Fatalf("scaled result lost truncation: %+v", selected.Instructions)
					}
				}
			}
		}
	}
}

func TestNarrowAddressesAreMaterializedBeforeMemoryAccess(t *testing.T) {
	profile, err := abi.NewPPC64Profile("ppc64le", abi.C)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := ppc64.NewPolicyForCPU(profile, abi.StackNoUse, abi.PPC64Power9)
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []uint8{8, 16, 32} {
		for _, shape := range []string{"add", "scaled", "offset", "subtract", "scaled-offset"} {
			ops := []ir.Instruction{{Op: op.OpParam, Bits: width}, {Op: op.OpParam, Bits: width}}
			switch shape {
			case "add":
				ops = append(ops, ir.Instruction{Op: op.OpAdd, A: 0, B: 1, Bits: width})
			case "scaled", "scaled-offset":
				ops = append(ops, ir.Instruction{Op: op.OpAddScaled, A: 0, B: 1, Imm: 1, Bits: width})
				if shape == "scaled-offset" {
					ops = append(ops, ir.Instruction{Op: op.OpConst, Imm: 4, Bits: width},
						ir.Instruction{Op: op.OpAdd, A: 2, B: 3, Bits: width})
				}
			case "offset", "subtract":
				operation := op.OpAdd
				if shape == "subtract" {
					operation = op.OpSub
				}
				ops = append(ops, ir.Instruction{Op: op.OpConst, Imm: 4, Bits: width},
					ir.Instruction{Op: operation, A: 0, B: 2, Bits: width})
			}
			address := len(ops) - 1
			ops = append(ops, ir.Instruction{Op: op.OpLoad64, A: address, Bits: 64})
			selected, err := testSelectProgram(ops, nil, 2, len(ops)-1, nil, 8, policy)
			if err != nil {
				t.Fatalf("width %d %s: %v", width, shape, err)
			}
			last := selected.Instructions[len(selected.Instructions)-1]
			if last.Op != machine.SelectedLoad64 || last.A != address {
				t.Fatalf("width %d %s bypassed pointer truncation: %+v", width, shape, last)
			}
		}
	}
}
