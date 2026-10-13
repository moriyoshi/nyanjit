package machine_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/target/ppc64"
)

func TestScaledArithmeticNarrowsForWiderTarget(t *testing.T) {
	for _, cpu := range []abi.PPC64CPU{abi.PPC64Power8, abi.PPC64Power9, abi.PPC64Power10} {
		profile, err := abi.NewPPC64Profile("ppc64", abi.C, abi.PPC64ELFv2)
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
						{Op: ir.OpParam, Bits: width},
						{Op: ir.OpParam, Bits: width},
						{Op: ir.OpAddScaled, A: 0, B: 1, Imm: scale, Bits: width},
					}
					result := 2
					if displacement != 0 {
						ops = append(ops, ir.Instruction{Op: ir.OpConst, Imm: displacement & (^uint64(0) >> (64 - width)), Bits: width},
							ir.Instruction{Op: ir.OpAdd, A: 2, B: 3, Bits: width})
						result = 4
					}
					selected, err := machine.SelectProgram(ops, nil, 2, result, nil, 8, policy)
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
	profile, err := abi.NewPPC64Profile("ppc64", abi.C, abi.PPC64ELFv2)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := ppc64.NewPolicyForCPU(profile, abi.StackNoUse, abi.PPC64Power9)
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []uint8{8, 16, 32} {
		for _, shape := range []string{"add", "scaled", "offset", "subtract", "scaled-offset"} {
			ops := []ir.Instruction{{Op: ir.OpParam, Bits: width}, {Op: ir.OpParam, Bits: width}}
			switch shape {
			case "add":
				ops = append(ops, ir.Instruction{Op: ir.OpAdd, A: 0, B: 1, Bits: width})
			case "scaled", "scaled-offset":
				ops = append(ops, ir.Instruction{Op: ir.OpAddScaled, A: 0, B: 1, Imm: 1, Bits: width})
				if shape == "scaled-offset" {
					ops = append(ops, ir.Instruction{Op: ir.OpConst, Imm: 4, Bits: width},
						ir.Instruction{Op: ir.OpAdd, A: 2, B: 3, Bits: width})
				}
			case "offset", "subtract":
				op := ir.OpAdd
				if shape == "subtract" {
					op = ir.OpSub
				}
				ops = append(ops, ir.Instruction{Op: ir.OpConst, Imm: 4, Bits: width},
					ir.Instruction{Op: op, A: 0, B: 2, Bits: width})
			}
			address := len(ops) - 1
			ops = append(ops, ir.Instruction{Op: ir.OpLoad64, A: address, Bits: 64})
			selected, err := machine.SelectProgram(ops, nil, 2, len(ops)-1, nil, 8, policy)
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
