//go:build linux && (arm64 || amd64 || riscv64)

package compile

import "testing"

func machineOpCount(program machineProgram, operation machineOp) int {
	count := 0
	for _, ins := range program.Instructions {
		if ins.Op == operation {
			count++
		}
	}
	return count
}

func TestSharedScaledAddressSelection(t *testing.T) {
	ops := []instruction{
		{Op: opParam}, {Op: opParam}, {Op: opParam},
		{Op: opAddScaled, A: 0, B: 1, Imm: 8},
		{Op: opStore64, A: 3, B: 2},
		{Op: opLoad64, A: 3},
	}
	program, err := selectProgram(ops, nil, 3, 5, newMachineTarget(CompileOptions{ABI: ABIC}))
	if err != nil {
		t.Fatal(err)
	}
	if machineOpCount(program, mScaled) != 1 || machineOpCount(program, mStore64) != 1 || machineOpCount(program, mLoad64) != 1 {
		t.Fatalf("shared address was not materialized once: %+v", program.Instructions)
	}
}

func TestFoldedScaledAddressSelection(t *testing.T) {
	ops := []instruction{
		{Op: opParam}, {Op: opParam}, {Op: opParam},
		{Op: opAddScaled, A: 0, B: 1, Imm: 8},
		{Op: opStore64, A: 3, B: 2},
		{Op: opAddScaled, A: 0, B: 1, Imm: 8},
		{Op: opLoad64, A: 5},
	}
	program, err := selectProgram(ops, nil, 3, 6, newMachineTarget(CompileOptions{ABI: ABIC}))
	if err != nil {
		t.Fatal(err)
	}
	if machineOpCount(program, mStoreAddress) != 1 || machineOpCount(program, mLoadAddress) != 1 || machineOpCount(program, mScaled) != 0 {
		t.Fatalf("single-use scaled addresses were not folded: %+v", program.Instructions)
	}
}
