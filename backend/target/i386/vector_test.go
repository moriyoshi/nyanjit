package i386_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
)

func TestSSE2VectorPlans(t *testing.T) {
	v := backend.VectorRegister
	for _, tc := range []struct {
		i         machine.Instruction
		hex, text string
	}{
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVLoad, A: v(3), B: 4}, "f30f6f9c2400000000", "movdqu xmm3, [ESP]"},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVStore, A: 5, B: v(3)}, "f30f7f9d00000000", "movdqu [EBP], xmm3"},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVZero, A: v(3)}, "660fefdb", "pxor xmm3, xmm3"},
		{machine.Instruction{Op: machine.OpVectorMove, A: v(3), B: v(4)}, "660f6fdc", "movdqa xmm3, xmm4"},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVSplat, Bits: 64, A: v(3), B: 0, HighB: 2}, "660f6ed8660f6ed2660f62da660f6cdb", "punpcklqdq xmm3, xmm3"},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVSplat, Bits: 8, A: v(3), B: 0}, "660f6ed8660f60dbf20f70db00660f70db00", "pshufd xmm3, xmm3, 0"},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVSplat, Bits: 16, A: v(3), B: 0}, "660f6ed8f20f70db00660f70db00", "pshuflw xmm3, xmm3, 0"},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVSplat, Bits: 32, A: v(3), B: 0}, "660f6ed8660f70db00", "movd xmm3, EAX"},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVExtract, Bits: 64, A: 0, HighA: 2, B: v(4), Imm: 1}, "660f6fc4660f73d808660f7ec0660f70d055660f7ed2", "movd EDX, xmm2"},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVExtract, Bits: 16, A: 6, B: v(4), Imm: 0}, "660f6fc4660fc5f000", "pextrw ESI, xmm0, 0"},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVExtract, Bits: 8, A: 7, B: v(4), Imm: 0}, "660f6fc4660f72f018660f72d018660f7ec7", "movd EDI, xmm0"},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVExtract, Bits: 32, A: 0, B: v(4), Imm: 1}, "660f6fc4660f73d804660f7ec0", "psrldq xmm0, 4"},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVInsert, Bits: 16, A: v(3), B: v(4), C: 0, Imm: 7}, "660f6fdc660fc4d807", "pinsrw xmm3, EAX, 7"},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVInsertZero, Bits: 64, A: v(3), B: v(4), C: 0, HighC: 2, Imm: 1}, "660f6fdc660f76c0660f73d808660f73f808660fdfc3660f6fd8660f6ec0660f6ed2660f62c2660f73f808660febd8", "por xmm3, xmm0"},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVCompare, Bits: 8, A: v(3), B: v(3), C: v(4), Imm: uint64(op.BranchEqual)}, "660f74dc", "pcmpeqb xmm3, xmm4"},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVMaskBits, A: 0, B: v(4)}, "660fd7c4", "pmovmskb EAX, xmm4"},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpSub, Bits: 32, A: v(4), B: v(3), C: v(4)}, "660f6fcc660f6fe3660ffae1", "psubd xmm4, xmm1"},
	} {
		check386Vector(t, tc.i, tc.hex, tc.text)
	}
	for _, tc := range []struct {
		op               op.Opcode
		bits             uint8
		opcode, mnemonic string
	}{
		{op.OpAdd, 8, "fc", "paddb"}, {op.OpAdd, 16, "fd", "paddw"}, {op.OpAdd, 32, "fe", "paddd"}, {op.OpAdd, 64, "d4", "paddq"},
		{op.OpSub, 8, "f8", "psubb"}, {op.OpSub, 16, "f9", "psubw"}, {op.OpSub, 32, "fa", "psubd"}, {op.OpSub, 64, "fb", "psubq"},
		{op.OpMul, 16, "d5", "pmullw"}, {op.OpAnd, 64, "db", "pand"}, {op.OpOr, 64, "eb", "por"}, {op.OpXor, 64, "ef", "pxor"},
	} {
		check386Vector(t, machine.Instruction{Op: machine.OpVector, Arithmetic: tc.op, Bits: tc.bits, A: v(3), B: v(3), C: v(4)}, "660f"+tc.opcode+"dc", tc.mnemonic)
	}
	for selector, imm := range []string{"00", "01", "10", "11"} {
		check386Vector(t, machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVCarrylessMul, Bits: 64, A: v(3), B: v(3), C: v(4), Imm: uint64(selector)}, "660f3a44dc"+imm, "pclmulqdq")
	}
}
func check386Vector(t *testing.T, i machine.Instruction, wantHex, text string) {
	t.Helper()
	a, x := i386.NewByteAssembler(abi.StackNoUse), i386.NewTextAssembler(abi.StackNoUse)
	for _, v := range []machine.Assembler{a, x} {
		if err := machine.Visit(i, v); err != nil {
			t.Fatal(err)
		}
	}
	want, err := hex.DecodeString(wantHex)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), want) || x.AssembledSize() != len(want) || i386.VectorSize(i, abi.StackNoUse, 0) != len(want) || i386.PhysicalSize(i, abi.StackNoUse, 0) != len(want) || !strings.Contains(x.Text(), text) {
		t.Fatalf("%+v got%x want%x text%s", i, a.Bytes(), want, x.Text())
	}
	if i386.FlagEffect(i) {
		t.Fatal("SIMD unexpectedly modifies GP flags")
	}
}
func TestSSE2VectorShiftCounts(t *testing.T) {
	for _, operation := range []op.Opcode{op.OpShiftLeft, op.OpShiftRight, op.OpRotateLeft, op.OpRotateRight} {
		for _, alias := range []bool{false, true} {
			src := backend.VectorRegister(4)
			if alias {
				src = backend.VectorRegister(3)
			}
			for _, n := range []uint64{0, 1, 31, 32, 33, 64, 255} {
				i := machine.Instruction{Op: machine.OpVector, Arithmetic: operation, Bits: 32, A: backend.VectorRegister(3), B: src, Imm: n}
				a, x := i386.NewByteAssembler(abi.StackNoUse), i386.NewTextAssembler(abi.StackNoUse)
				if err := a.Vector(i); err != nil {
					t.Fatal(err)
				}
				if err := x.Vector(i); err != nil {
					t.Fatal(err)
				}
				if len(a.Bytes()) != x.AssembledSize() || len(a.Bytes()) != i386.VectorSize(i, abi.StackNoUse, 0) {
					t.Fatal("PC mismatch")
				}
				rotate := operation == op.OpRotateLeft || operation == op.OpRotateRight
				if !rotate && n >= 32 && hex.EncodeToString(a.Bytes()) != "660fefdb" {
					t.Fatal("shift must zero")
				}
				if rotate && n%32 == 0 || !rotate && n == 0 {
					want := 0
					if !alias {
						want = 4
					}
					if len(a.Bytes()) != want {
						t.Fatal("identity should only copy")
					}
				}
			}
		}
	}
}
func TestSSE2VectorSpillAndPool(t *testing.T) {
	for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
		for _, reload := range []bool{false, true} {
			a, x := i386.NewByteAssembler(stack), i386.NewTextAssembler(stack)
			a.FrameEnterArea(256, 128)
			x.FrameEnterArea(256, 128)
			before := len(a.Bytes())
			op := machine.OpVectorSpill
			if reload {
				op = machine.OpVectorReload
			}
			i := machine.Instruction{Op: op, A: backend.VectorRegister(7), Slot: 3}
			if err := a.Vector(i); err != nil {
				t.Fatal(err)
			}
			if err := x.Vector(i); err != nil {
				t.Fatal(err)
			}
			if len(a.Bytes())-before != i386.VectorSize(i, stack, 128) || len(a.Bytes()) != x.AssembledSize() {
				t.Fatal("spill call-area PC mismatch")
			}
			if !strings.Contains(x.Text(), "movdqu") || !strings.Contains(x.Text(), "0x8c") && stack == abi.StackPlatform {
				t.Fatal(x.Text())
			}
		}
	}
	a, x := i386.NewByteAssembler(abi.StackNoUse), i386.NewTextAssembler(abi.StackNoUse)
	i := machine.Instruction{Op: machine.OpLiteralLoad, A: backend.VectorRegister(7), Bits: 128, Disp: 32}
	for _, v := range []machine.Assembler{a, x} {
		if err := machine.Visit(i, v); err != nil {
			t.Fatal(err)
		}
	}
	if hex.EncodeToString(a.Bytes()) != "f30f6f3d00000000" || len(a.Relocations()) != 1 || a.Relocations()[0].Encoding != machine.Reloc386Literal || a.Relocations()[0].TargetOffset != 32 || a.AssembledSize() != x.AssembledSize() || !strings.Contains(x.Text(), "movdqu xmm7, [literal + 0x20]") {
		t.Fatal("bad pool relocation")
	}
	a = i386.NewByteAssembler(abi.StackNoUse)
	if err := a.Vector(machine.Instruction{Op: machine.OpVectorMove, A: backend.VectorRegister(8), B: backend.VectorRegister(3)}); !errors.Is(err, machine.ErrUnsupportedOperation) || len(a.Bytes()) != 0 {
		t.Fatal("accepted unavailable XMM register")
	}
}

func TestSSE2RejectsWrongRegisterBanks(t *testing.T) {
	f := backend.VectorRegister
	for _, i := range []machine.Instruction{
		{Op: machine.OpVectorMove, A: 0, B: f(3)},
		{Op: machine.OpVector, Arithmetic: op.OpVLoad, A: f(3), B: f(4)},
		{Op: machine.OpVector, Arithmetic: op.OpVStore, A: f(3), B: f(4)},
		{Op: machine.OpVector, Arithmetic: op.OpVSplat, Bits: 64, A: f(3), B: 0, HighB: f(4)},
		{Op: machine.OpVector, Arithmetic: op.OpVExtract, Bits: 64, A: 0, HighA: 0, B: f(3)},
		{Op: machine.OpVector, Arithmetic: op.OpAdd, Bits: 32, A: f(3), B: f(4), C: 0},
	} {
		a, x := i386.NewByteAssembler(abi.StackNoUse), i386.NewTextAssembler(abi.StackNoUse)
		if !errors.Is(a.Vector(i), machine.ErrUnsupportedOperation) || !errors.Is(x.Vector(i), machine.ErrUnsupportedOperation) || a.AssembledSize() != 0 || x.AssembledSize() != 0 {
			t.Fatalf("invalid operand banks accepted %+v", i)
		}
	}
}
