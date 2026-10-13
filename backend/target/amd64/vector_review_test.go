package amd64_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/amd64"
)

func TestSSE2VectorExpansionAndSpillEncodings(t *testing.T) {
	v := backend.VectorRegister
	tests := []struct {
		name  string
		ins   machine.Instruction
		stack abi.StackPractice
		homes int
		hex   string
	}{
		{"splat-byte", machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVSplat, Bits: 8, A: v(3), B: 6}, abi.StackNoUse, 0, "660f6ede660f60dbf20f70db00660f70db00"},
		{"splat-high", machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVSplat, Bits: 32, A: v(11), B: 12}, abi.StackNoUse, 0, "66450f6edc66450f70db00"},
		{"splat-qword", machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVSplat, Bits: 64, A: v(14), B: 13}, abi.StackNoUse, 0, "664d0f6ef566450f6cf6"},
		{"extract-spl", machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVExtract, Bits: 8, A: 4, B: v(11), Imm: 7}, abi.StackNoUse, 0, "66410f6fc3660f73d80766480f7ec4400fb6e4"},
		{"extract-sil", machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVExtract, Bits: 8, A: 6, B: v(11), Imm: 7}, abi.StackNoUse, 0, "66410f6fc3660f73d80766480f7ec6400fb6f6"},
		{"extract-dil", machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVExtract, Bits: 8, A: 7, B: v(11), Imm: 7}, abi.StackNoUse, 0, "66410f6fc3660f73d80766480f7ec7400fb6ff"},
		{"extract-qword", machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVExtract, Bits: 64, A: 12, B: v(14), Imm: 1}, abi.StackNoUse, 0, "66410f6fc6660f73d80866490f7ec4"},
		{"insert", machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVInsert, Bits: 16, A: v(11), B: v(12), C: 13, Imm: 7}, abi.StackNoUse, 0, "66450f6fdc66450fc4dd07"},
		{"insert-zero", machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVInsertZero, Bits: 64, A: v(11), B: v(12), C: 13, Imm: 1}, abi.StackNoUse, 0, "66450f6fdc660f76c0660f73d808660f73f80866410fdfc366440f6fd866490f6ec5660f73f80866440febd8"},
		{"compare", machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVCompare, Bits: 8, A: v(11), B: v(12), C: v(14)}, abi.StackNoUse, 0, "66450f6fdc66450f74de"},
		{"multiply", machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpMul, Bits: 16, A: v(11), B: v(12), C: v(14)}, abi.StackNoUse, 0, "66450f6fdc66450fd5de"},
		{"subtract", machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpSub, Bits: 64, A: v(11), B: v(12), C: v(14)}, abi.StackNoUse, 0, "66450f6fdc66450ffbde"},
		{"copy", machine.Instruction{Op: machine.OpVectorMove, A: v(11), B: v(12)}, abi.StackNoUse, 0, "66450f6fdc"},
		{"r12-base", machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVLoad, A: v(11), B: 12}, abi.StackNoUse, 0, "f3450f6f1c24"},
		{"r13-base", machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVStore, A: 13, B: v(11)}, abi.StackNoUse, 0, "f3450f7f5d00"},
		{"rsp-spill", machine.Instruction{Op: machine.OpVectorSpill, A: v(11), Slot: 9}, abi.StackPlatform, 0, "f3440f7f5c2448"},
		{"rsp-go-reload", machine.Instruction{Op: machine.OpVectorReload, A: v(11), Slot: 9}, abi.StackPlatform, 7, "f3440f6f9c2480000000"},
		{"r10-disp32-spill", machine.Instruction{Op: machine.OpVectorSpill, A: v(11), Slot: 32}, abi.StackNoUse, 0, "f3450f7f9a00010000"},
		{"r10-disp32-reload", machine.Instruction{Op: machine.OpVectorReload, A: v(11), Slot: 32}, abi.StackNoUse, 0, "f3450f6f9a00010000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want, err := hex.DecodeString(tc.hex)
			if err != nil {
				t.Fatal(err)
			}
			b := amd64.NewByteAssembler(tc.stack, tc.homes)
			text := amd64.NewTextAssembler(tc.stack, tc.homes)
			if err := b.Vector(tc.ins); err != nil {
				t.Fatal(err)
			}
			if err := text.Vector(tc.ins); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(b.Bytes(), want) {
				t.Fatalf("got%x want%x", b.Bytes(), want)
			}
			if text.AssembledSize() != len(want) || amd64.PhysicalSize(tc.ins, tc.stack, tc.homes) != len(want) {
				t.Fatal("frame-aware sizing disagrees")
			}
		})
	}
}
