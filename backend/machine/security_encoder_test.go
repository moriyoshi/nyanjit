package machine_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/amd64"
	"github.com/moriyoshi/nyanjit/backend/target/arm64"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
	"github.com/moriyoshi/nyanjit/backend/target/riscv64"
)

type securityAssembler interface {
	machine.Assembler
	Bytes() []byte
}

func TestSecurityEncodingRanges(t *testing.T) {
	constructors := map[string]func() securityAssembler{
		"arm64":   func() securityAssembler { return arm64.NewByteAssembler(abi.StackNoUse, 0) },
		"amd64":   func() securityAssembler { return amd64.NewByteAssembler(abi.StackNoUse, 0) },
		"riscv64": func() securityAssembler { return riscv64.NewByteAssembler(abi.StackNoUse, 0) },
		"loong64": func() securityAssembler { return loong64.NewByteAssembler(abi.StackNoUse, 0) },
	}
	tests := []struct {
		arch, name string
		ins        machine.Instruction
	}{
		{"arm64", "compare", machine.Instruction{Op: machine.OpCompareImmediate, A: 0, Imm: 4096}},
		{"arm64", "offset", machine.Instruction{Op: machine.OpLoadOffset, A: 0, B: 1, Disp: 257}},
		{"arm64", "jump", machine.Instruction{Op: machine.OpJump, Imm: 2}},
		{"arm64", "logical-immediate", machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAnd, Bits: 64, A: 0, B: 1, Imm: 0}},
		{"amd64", "compare", machine.Instruction{Op: machine.OpCompareImmediate, A: 0, Imm: 1 << 32}},
		{"amd64", "immediate", machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAdd, Bits: 64, A: 0, B: 1, Imm: 1 << 32}},
		{"amd64", "index", machine.Instruction{Op: machine.OpLoadAddress, A: 0, B: 1, C: 4, Imm: 1}},
		{"riscv64", "immediate", machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAdd, Bits: 64, A: 10, B: 11, Imm: 2048}},
		{"riscv64", "jump", machine.Instruction{Op: machine.OpJump, Imm: 1}},
		{"loong64", "immediate", machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAdd, Bits: 64, A: 4, B: 5, Imm: 2048}},
		{"loong64", "jump", machine.Instruction{Op: machine.OpJump, Imm: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.arch+"/"+tt.name, func(t *testing.T) {
			a := constructors[tt.arch]()
			if err := machine.Visit(tt.ins, a); err == nil {
				t.Fatal("invalid encoding accepted")
			}
			if len(a.Bytes()) != 0 {
				t.Fatalf("invalid bytes exposed: %x", a.Bytes())
			}
		})
	}
}

func TestSecurityZeroSymbolBinding(t *testing.T) {
	a := amd64.NewByteAssembler(abi.StackNoUse, 0)
	a.Symbol(0, "entry")
	if _, err := machine.LinkRelocations(a.Bytes(), a.Relocations(), map[string]uint64{"entry": 0}); err == nil {
		t.Fatal("zero entry binding accepted")
	}
	if _, err := machine.LinkRelocations(a.Bytes(), a.Relocations(), map[string]uint64{"entry": 1}); err != nil {
		t.Fatal(err)
	}
}
