package ir

import "testing"

func TestInputsOwnTheirStorage(t *testing.T) {
	for _, op := range []Opcode{OpScopeEnter, OpCallC, OpCallGo, OpSyscall, OpAdd, OpSelect} {
		instruction := Instruction{Op: op, A: 1, B: 2, C: 3, Args: []int{1, 2, 3}}
		inputs := instruction.Inputs()
		inputs[0] = 99
		if instruction.A != 1 || instruction.Args[0] != 1 {
			t.Fatalf("opcode %d Inputs aliases instruction", op)
		}
	}
}

func TestEmptyInputsOwnSpareStorage(t *testing.T) {
	for _, op := range []Opcode{OpScopeEnter, OpScopeLeave, OpCallC, OpCallGo, OpSyscall} {
		backing := []int{7, 8, 9}
		i := Instruction{Op: op, Args: backing[:0]}
		got := append(i.Inputs(), 99)
		if got[0] != 99 || backing[0] != 7 {
			t.Fatalf("%s empty Inputs aliases spare Args capacity", op)
		}
	}
}
