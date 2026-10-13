package riscv64

import (
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func TestPhysicalSizeMatchesAssembler(t *testing.T) {
	for _, test := range []struct {
		name    string
		ins     machine.Instruction
		stack   abi.StackPractice
		goHomes int
	}{
		{"constant", machine.Instruction{Op: machine.OpConst, A: 10, Imm: 0x123456789abcdef0}, abi.StackNoUse, 0},
		{"symbol", machine.Instruction{Op: machine.OpSymbol, A: 10, Symbol: "target"}, abi.StackNoUse, 0},
		{"move", machine.Instruction{Op: machine.OpMove, A: 10, B: 11}, abi.StackNoUse, 0},
		{"same register move", machine.Instruction{Op: machine.OpMove, A: 10, B: 10}, abi.StackNoUse, 0},
		{"binary", machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, A: 10, B: 11, C: 12}, abi.StackNoUse, 0},
		{"add immediate", machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAdd, A: 10, B: 11, Imm: 2047}, abi.StackNoUse, 0},
		{"subtract immediate", machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpSub, A: 10, B: 11, Imm: 2048}, abi.StackNoUse, 0},
		{"and immediate", machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAnd, A: 10, B: 11, Imm: ^uint64(0)}, abi.StackNoUse, 0},
		{"or immediate", machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpOr, A: 10, B: 11, Imm: 7}, abi.StackNoUse, 0},
		{"xor immediate", machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpXor, A: 10, B: 11, Imm: ^uint64(0)}, abi.StackNoUse, 0},
		{"decrement", machine.Instruction{Op: machine.OpDec, A: 10, B: 11}, abi.StackNoUse, 0},
		{"narrow", machine.Instruction{Op: machine.OpNarrow, A: 10, B: 11, Imm: 8}, abi.StackNoUse, 0},
		{"sign extend", machine.Instruction{Op: machine.OpSignExtend, A: 10, B: 11, Imm: 16}, abi.StackNoUse, 0},
		{"sign extend 32", machine.Instruction{Op: machine.OpSignExtend, A: 10, B: 11, Imm: 32}, abi.StackNoUse, 0},
		{"rotate", machine.Instruction{Op: machine.OpShift, Arithmetic: op.OpRotateLeft, Bits: 64, A: 10, B: 11, Imm: 7}, abi.StackNoUse, 0},
		{"zero rotate", machine.Instruction{Op: machine.OpShift, Arithmetic: op.OpRotateLeft, Bits: 64, A: 10, B: 10}, abi.StackNoUse, 0},
		{"scaled", machine.Instruction{Op: machine.OpScaled, A: 10, B: 11, C: 12, Imm: 8}, abi.StackNoUse, 0},
		{"scaled non-power-of-two", machine.Instruction{Op: machine.OpScaled, A: 10, B: 11, C: 12, Imm: 7}, abi.StackNoUse, 0},
		{"scaled zero", machine.Instruction{Op: machine.OpScaled, A: 10, B: 11, C: 12}, abi.StackNoUse, 0},
		{"mul add", machine.Instruction{Op: machine.OpMulAdd, A: 10, B: 11, C: 12, D: 13}, abi.StackNoUse, 0},
		{"select", machine.Instruction{Op: machine.OpSelect, A: 10, B: 11, C: 12, D: 13}, abi.StackNoUse, 0},
		{"load 64", machine.Instruction{Op: machine.OpLoad64, A: 10, B: 11}, abi.StackNoUse, 0},
		{"store 64", machine.Instruction{Op: machine.OpStore64, A: 11, B: 12}, abi.StackNoUse, 0},
		{"load 8", machine.Instruction{Op: machine.OpLoad8, A: 10, B: 11}, abi.StackNoUse, 0},
		{"store 8", machine.Instruction{Op: machine.OpStore8, A: 11, B: 12}, abi.StackNoUse, 0},
		{"load address", machine.Instruction{Op: machine.OpLoadAddress, A: 10, B: 11, C: 12, Imm: 8, Disp: 4096}, abi.StackNoUse, 0},
		{"load address non-power-of-two", machine.Instruction{Op: machine.OpLoadAddress, A: 10, B: 11, C: 12, Imm: 7, Disp: 4096}, abi.StackNoUse, 0},
		{"load address scale one", machine.Instruction{Op: machine.OpLoadAddress, A: 10, B: 11, C: 12, Imm: 1}, abi.StackNoUse, 0},
		{"store address", machine.Instruction{Op: machine.OpStoreAddress, A: 11, B: 12, C: 13, Imm: 8, Disp: 4096}, abi.StackNoUse, 0},
		{"store address non-power-of-two", machine.Instruction{Op: machine.OpStoreAddress, A: 11, B: 12, C: 13, Imm: 7, Disp: 4096}, abi.StackNoUse, 0},
		{"load offset", machine.Instruction{Op: machine.OpLoadOffset, A: 10, B: 11, Disp: 4096}, abi.StackNoUse, 0},
		{"store offset", machine.Instruction{Op: machine.OpStoreOffset, A: 11, B: 12, Disp: 4096}, abi.StackNoUse, 0},
		{"workspace spill", machine.Instruction{Op: machine.OpSpill, A: 10, Slot: 300}, abi.StackNoUse, 0},
		{"stack reload", machine.Instruction{Op: machine.OpReload, A: 10, Slot: 300}, abi.StackPlatform, 9},
		{"save link on workspace", machine.Instruction{Op: machine.OpSaveLink, Slot: 300}, abi.StackNoUse, 0},
		{"restore link on workspace", machine.Instruction{Op: machine.OpRestoreLink, Slot: 300}, abi.StackNoUse, 0},
		{"save link on stack", machine.Instruction{Op: machine.OpSaveLink, Slot: 0}, abi.StackPlatform, 0},
		{"restore link on stack", machine.Instruction{Op: machine.OpRestoreLink, Slot: 0}, abi.StackPlatform, 0},
		{"incoming far word", machine.Instruction{Op: machine.OpLoadIncoming, A: 10, Imm: 4096}, abi.StackPlatform, 0},
		{"save float", machine.Instruction{Op: machine.OpSaveCFloat, A: 8, Slot: 300}, abi.StackPlatform, 9},
		{"restore float", machine.Instruction{Op: machine.OpRestoreCFloat, A: 8, Slot: 300}, abi.StackPlatform, 9},
		{"frame enter", machine.Instruction{Op: machine.OpFrameEnter, Imm: 4096}, abi.StackPlatform, 0},
		{"frame leave", machine.Instruction{Op: machine.OpFrameLeave, Imm: 4096}, abi.StackPlatform, 0},
		{"C call", machine.Instruction{Op: machine.OpCallC}, abi.StackPlatform, 0},
		{"Go call", machine.Instruction{Op: machine.OpCallGo}, abi.StackPlatform, 0},
		{"syscall", machine.Instruction{Op: machine.OpSyscall}, abi.StackNoUse, 0},
		{"return", machine.Instruction{Op: machine.OpReturn}, abi.StackNoUse, 0},
		{"compare zero", machine.Instruction{Op: machine.OpCompareImmediate, A: 10}, abi.StackNoUse, 0},
		{"compare constant", machine.Instruction{Op: machine.OpCompareImmediate, A: 10, Imm: 0x123456789abcdef0}, abi.StackNoUse, 0},
		{"compare register", machine.Instruction{Op: machine.OpCompareRegister, A: 10, B: 11}, abi.StackNoUse, 0},
		{"near jump", machine.Instruction{Op: machine.OpJump, Imm: 64}, abi.StackNoUse, 0},
		{"far jump", machine.Instruction{Op: machine.OpJump, Imm: 1 << 20, Long: true}, abi.StackNoUse, 0},
		{"near branch", machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchEqual, Imm: 64}, abi.StackNoUse, 0},
		{"medium branch", machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchEqual, Imm: 8192, Long: true}, abi.StackNoUse, 0},
		{"far branch", machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchEqual, Imm: 1 << 20, Long: true, Far: true}, abi.StackNoUse, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := NewByteAssembler(test.stack, test.goHomes)
			if err := machine.Visit(test.ins, a); err != nil {
				t.Fatal(err)
			}
			if got, want := a.AssembledSize(), PhysicalSize(test.ins, test.stack, test.goHomes); got != want {
				t.Fatalf("assembled %d bytes, physical size %d", got, want)
			}
			var actual [2]bool
			for _, word := range a.words {
				switch word & 0x7f {
				case 0x03, 0x13, 0x17, 0x1b, 0x33, 0x37, 0x67, 0x6f:
					rd := word >> 7 & 31
					if rd == 30 || rd == 31 {
						actual[rd-30] = true
					}
				}
			}
			var actualWrites []backend.Register
			for index, yes := range actual {
				if yes {
					actualWrites = append(actualWrites, backend.Register(30+index))
				}
			}
			if reported := ImplicitWrites(test.ins, test.stack, test.goHomes); !slices.Equal(reported, actualWrites) {
				t.Fatalf("reported scratch writes %v, encoded writes %v", reported, actualWrites)
			}
		})
	}
}
