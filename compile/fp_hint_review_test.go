package compile

import (
	"math"
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/regalloc"
)

func hintReviewProgram(t *testing.T, arch string) (machineProgram, machineTarget) {
	t.Helper()
	target := newMachineTarget(CompileOptions{Architecture: arch})
	ops := []ir.Instruction{{Op: ir.OpParam, Bits: 64}, {Op: ir.OpConst, Bits: 64, Imm: math.Float64bits(1)},
		{Op: ir.OpFAdd, Bits: 64, A: 0, B: 1}, {Op: ir.OpConst, Bits: 64, Imm: 0x55},
		{Op: ir.OpXor, Bits: 64, A: 0, B: 3}, {Op: ir.OpXor, Bits: 64, A: 4, B: 2}}
	p, err := machine.SelectProgram(ops, nil, 1, 5, []int{2}, abi.MaxArguments(), target)
	if err != nil {
		t.Fatal(err)
	}
	return p, target
}

func TestFPHintRemapsToIntegerCopy(t *testing.T) {
	p, _ := hintReviewProgram(t, "amd64")
	p.PreferredRegisters = map[int]machine.Register{2: 1}
	q := assignFloatResidency(p)
	if _, ok := q.PreferredRegisters[2]; ok {
		t.Fatal("integer hint stayed on FP producer")
	}
	copy := q.Exports[0]
	if q.Values[copy].Class != regalloc.General || q.PreferredRegisters[copy] != 1 {
		t.Fatal("integer copy did not inherit branch hint")
	}
	if p.PreferredRegisters[2] != 1 {
		t.Fatal("rewriter changed input hints")
	}
}

func TestAllocatorRejectsWrongBankHints(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "riscv64", "ppc64", "ppc64le"} {
		p, target := hintReviewProgram(t, arch)
		p.PreferredRegisters = map[int]machine.Register{4: machine.VectorRegister(4)}
		physical, _, _, err := allocateAndEmit(p, target, newMachineEmitter(CompileOptions{Architecture: arch}))
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range physical.insns {
			if (i.ins.Op == machine.OpBinary || i.ins.Op == machine.OpBinaryImmediate) && machine.IsVectorRegister(i.ins.A) {
				t.Fatalf("%s integer hint bypassed bank check: %+v", arch, i.ins)
			}
		}
	}
}
