package machine_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/amd64"
	"github.com/moriyoshi/nyanjit/backend/target/arm64"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
	"github.com/moriyoshi/nyanjit/backend/target/riscv64"
)

func TestEncoderFieldsRejectBeforeEncoding(t *testing.T) {
	constructors := map[string]func() securityAssembler{
		"amd64":   func() securityAssembler { return amd64.NewByteAssembler(abi.StackPlatform, 0) },
		"arm64":   func() securityAssembler { return arm64.NewByteAssembler(abi.StackPlatform, 0) },
		"riscv64": func() securityAssembler { return riscv64.NewByteAssembler(abi.StackPlatform, 0) },
		"loong64": func() securityAssembler { return loong64.NewByteAssembler(abi.StackPlatform, 0) },
	}
	v := backend.VectorRegister
	cases := map[string]machine.Instruction{
		"gp-range":                  {Op: machine.OpConst, A: 32, Imm: 1},
		"gp-bank":                   {Op: machine.OpMove, A: 1, B: v(1)},
		"binary-op":                 {Op: machine.OpBinary, Arithmetic: op.OpParam, Bits: 64, A: 1, B: 2, C: 3},
		"binary-immediate-multiply": {Op: machine.OpBinaryImmediate, Arithmetic: op.OpMul, Bits: 64, A: 1, B: 2, Imm: 1},
		"binary-width":              {Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 7, A: 1, B: 2, C: 3},
		"shift-op":                  {Op: machine.OpShift, Arithmetic: op.OpAdd, Bits: 64, A: 1, B: 2, Imm: 3},
		"shift-overflow":            {Op: machine.OpShift, Arithmetic: op.OpShiftLeft, Bits: 64, A: 1, B: 2, Imm: 256},
		"narrow-overflow":           {Op: machine.OpNarrow, A: 1, B: 2, Imm: 264},
		"narrow-zero":               {Op: machine.OpNarrow, A: 1, B: 2},
		"float-range":               {Op: machine.OpFloatBinary, Arithmetic: op.OpFAdd, Bits: 64, A: v(1), B: 32, C: v(2)},
		"float-move-bank":           {Op: machine.OpFloatMove, Bits: 64, A: 1, B: 2},
		"float-memory-bank":         {Op: machine.OpFloatReload, Bits: 64, A: 1},
		"float-memory-width":        {Op: machine.OpFloatReload, Bits: 7, A: v(1)},
		"literal-bank":              {Op: machine.OpLiteralLoad, Bits: 64, A: 1},
		"narrow-memory-range":       {Op: machine.OpLoad16, A: 32, B: 1},
		"negative-spill":            {Op: machine.OpSpill, A: 1, Slot: -1},
		"overflow-spill":            {Op: machine.OpReload, A: 1, Slot: int(^uint(0) >> 1)},
		"carry-mode":                {Op: machine.OpWordCarry, Arithmetic: op.OpAdd, Bits: 64, A: 1, B: 2, C: 3, CarryMode: 255},
		"carry-op":                  {Op: machine.OpWordCarry, Arithmetic: op.OpAnd, Bits: 64, A: 1, B: 2, C: 3, CarryMode: machine.CarryFlags},
		"branch-condition":          {Op: machine.OpJumpIf, Condition: 255, Imm: 16},
		"vector-load-bank":          {Op: machine.OpVector, Arithmetic: op.OpVLoad, A: 1, B: 2},
		"vector-extract-lane":       {Op: machine.OpVector, Arithmetic: op.OpVExtract, Bits: 16, A: 1, B: v(1), Imm: 8},
		"vector-insert-lane":        {Op: machine.OpVector, Arithmetic: op.OpVInsert, Bits: 32, A: v(1), B: v(2), C: 1, Imm: 4},
		"vector-width":              {Op: machine.OpVector, Arithmetic: op.OpAdd, Bits: 7, A: v(1), B: v(2), C: v(3)},
	}
	for arch, mk := range constructors {
		t.Run(arch, func(t *testing.T) {
			for name, ins := range cases {
				t.Run(name, func(t *testing.T) {
					a := mk()
					if err := machine.Visit(ins, a); err == nil {
						t.Fatalf("invalid fields accepted: %+v", ins)
					}
					if len(a.Bytes()) != 0 {
						t.Fatal("invalid encoding exposed bytes")
					}
				})
			}
		})
	}
}

func TestEncoderDirectFieldsAndState(t *testing.T) {
	for name, mk := range map[string]func(int) securityAssembler{
		"amd64":   func(h int) securityAssembler { return amd64.NewByteAssembler(abi.StackPlatform, h) },
		"arm64":   func(h int) securityAssembler { return arm64.NewByteAssembler(abi.StackPlatform, h) },
		"riscv64": func(h int) securityAssembler { return riscv64.NewByteAssembler(abi.StackPlatform, h) },
		"loong64": func(h int) securityAssembler { return loong64.NewByteAssembler(abi.StackPlatform, h) },
	} {
		t.Run(name, func(t *testing.T) {
			for _, homes := range []int{-1, int(^uint(0) >> 1)} {
				if err := machine.Visit(machine.Instruction{Op: machine.OpConst}, mk(homes)); err == nil {
					t.Fatal("invalid homes accepted")
				}
			}
			a := mk(0)
			a.SetPC(-4)
			a.Constant(0, 1)
			if len(a.Bytes()) != 0 {
				t.Fatal("negative PC exposed bytes")
			}
			for _, method := range []string{"Literal", "FloatMemory"} {
				a := mk(0)
				var err error
				switch method {
				case "Literal":
					err = a.(interface {
						Literal(machine.Instruction) error
					}).Literal(machine.Instruction{Op: machine.OpConst})
				case "FloatMemory":
					err = a.(interface {
						FloatMemory(machine.Instruction) error
					}).FloatMemory(machine.Instruction{Op: machine.OpConst})
				}
				if err == nil || len(a.Bytes()) != 0 {
					t.Fatalf("%s accepted wrong opcode", method)
				}
			}
		})
	}
}

func TestInvalidEncoderPlansHaveZeroSize(t *testing.T) {
	bad := machine.Instruction{Op: machine.OpFloatBinary, Arithmetic: op.OpFAdd, Bits: 64, A: backend.VectorRegister(1), B: 32, C: backend.VectorRegister(2)}
	for name, size := range map[string]func(machine.Instruction) int{
		"amd64": amd64.FloatSize, "arm64": arm64.FloatSize, "riscv64": riscv64.FloatSize,
	} {
		if got := size(bad); got != 0 {
			t.Errorf("%s invalid float plan size=%d", name, got)
		}
	}
	if amd64.ConstantSize(16, 1) != 0 || amd64.MemorySize(16) != 0 {
		t.Fatal("invalid x86 register size")
	}
	if got := riscv64.ConstantWords(32, 1); len(got) != 0 {
		t.Fatal("invalid RV constant plan")
	}
	for _, size := range []func(int) int{arm64.FrameEnterSize, arm64.FrameLeaveSize, riscv64.FrameEnterSize, riscv64.FrameLeaveSize} {
		if size(-1) != 0 || size(int(^uint(0)>>1)) != 0 {
			t.Fatal("invalid frame plan size")
		}
	}
}

func TestCPUSizePlansRejectInvalidFields(t *testing.T) {
	for _, i := range []machine.Instruction{
		{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 7, A: 1, B: 2, C: 3},
		{Op: machine.OpBinaryImmediate, Arithmetic: op.OpMul, Bits: 64, A: 1, B: 2, Imm: 1},
	} {
		if amd64.PhysicalSizeWithCPU(i, abi.StackNoUse, abi.ResolvedCPU{}, 0) != 0 || riscv64.PhysicalSizeWithCPU(i, abi.StackNoUse, abi.ResolvedCPU{}, 0) != 0 {
			t.Fatalf("invalid CPU-specific plan accepted: %+v", i)
		}
	}
}
