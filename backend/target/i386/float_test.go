package i386_test

import (
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
)

func TestSSE2FloatEncodings(t *testing.T) {
	// Pair-transfer bytes independently assembled with GOARCH=386 go tool asm.
	pack0 := "660f6ec0660f6ed2660f62c2"
	pack1 := "660f6ecb660f6ed1660f62ca"
	unpack := "660f7ec0660f70d055660f7ed2"
	for _, w := range []uint8{32, 64} {
		prefix := byte(0xf3)
		before, after := "660f6ec0660f6ecb", "660f7ec0"
		if w == 64 {
			prefix = 0xf2
			before, after = pack0+pack1, unpack
		}
		for _, op := range []struct {
			ir       op.Opcode
			mnemonic string
			opcode   byte
		}{{op.OpFAdd, "add", 0x58}, {op.OpFSub, "sub", 0x5c}, {op.OpFMul, "mul", 0x59}, {op.OpFDiv, "div", 0x5e}} {
			t.Run(fmt.Sprintf("%s/%d", op.mnemonic, w), func(t *testing.T) {
				ins := machine.Instruction{Op: machine.OpFloatBinary, Arithmetic: op.ir, Bits: w, A: 0, HighA: 2, B: 0, HighB: 2, C: 3, HighC: 1}
				suffix := "ss"
				if w == 64 {
					suffix = "sd"
				}
				checkFloat(t, ins, before+fmt.Sprintf("%02x0f%02xc1", prefix, op.opcode)+after, op.mnemonic+suffix)
			})
		}
	}
	for _, test := range []struct {
		name     string
		ins      machine.Instruction
		hex      string
		mnemonic string
	}{
		{"sqrt32", machine.Instruction{Op: machine.OpFloatUnary, Arithmetic: op.OpFSqrt, Bits: 32, A: 0, B: 0}, "660f6ec0f30f51c0660f7ec0", "sqrtss"},
		{"sqrt64", machine.Instruction{Op: machine.OpFloatUnary, Arithmetic: op.OpFSqrt, Bits: 64, A: 0, HighA: 2, B: 0, HighB: 2}, pack0 + "f20f51c0" + unpack, "sqrtsd"},
		{"convert-to64", machine.Instruction{Op: machine.OpFloatUnary, Arithmetic: op.OpFConvert, Bits: 64, A: 0, HighA: 2, B: 0}, "660f6ec0f30f5ac0" + unpack, "cvtss2sd"},
		{"convert-to32", machine.Instruction{Op: machine.OpFloatUnary, Arithmetic: op.OpFConvert, Bits: 32, A: 0, B: 0, HighB: 2}, pack0 + "f20f5ac0660f7ec0", "cvtsd2ss"},
		{"home-add", machine.Instruction{Op: machine.OpFloatBinary, Arithmetic: op.OpFAdd, Bits: 64, A: backend.VectorRegister(7), B: backend.VectorRegister(3), C: backend.VectorRegister(4)}, "660f28fbf20f58fc", "addsd"},
		{"home-sub-in-place", machine.Instruction{Op: machine.OpFloatBinary, Arithmetic: op.OpFSub, Bits: 32, A: backend.VectorRegister(3), B: backend.VectorRegister(3), C: backend.VectorRegister(7)}, "f30f5cdf", "subss"},
		{"home-convert", machine.Instruction{Op: machine.OpFloatUnary, Arithmetic: op.OpFConvert, Bits: 64, A: backend.VectorRegister(7), B: backend.VectorRegister(3)}, "f30f5afb", "cvtss2sd"},
		{"home-copy", machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: backend.VectorRegister(7), B: backend.VectorRegister(3)}, "660f28fb", "movapd"},
		{"copy-pair-in", machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: backend.VectorRegister(0), B: 0, HighB: 2}, pack0, "punpckldq"},
		{"copy-pair-out", machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: 0, HighA: 2, B: backend.VectorRegister(0)}, unpack, "pshufd"},
		{"FP-bits-and", machine.Instruction{Op: machine.OpFloatBits, Arithmetic: op.OpAnd, Bits: 64, A: backend.VectorRegister(3), B: backend.VectorRegister(4)}, "660f28dc660f54d8", "andpd"},
		{"FP-bits-xor", machine.Instruction{Op: machine.OpFloatBits, Arithmetic: op.OpXor, Bits: 32, A: backend.VectorRegister(3), B: backend.VectorRegister(4)}, "660f28dc660f57d8", "xorpd"},
	} {
		t.Run(test.name, func(t *testing.T) { checkFloat(t, test.ins, test.hex, test.mnemonic) })
	}
}

func checkFloat(t *testing.T, ins machine.Instruction, wantHex, mnemonic string) {
	t.Helper()
	b, text := i386.NewByteAssembler(abi.StackNoUse), i386.NewTextAssembler(abi.StackNoUse)
	if err := machine.Visit(ins, b); err != nil {
		t.Fatal(err)
	}
	if err := machine.Visit(ins, text); err != nil {
		t.Fatal(err)
	}
	want, err := hex.DecodeString(wantHex)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(b.Bytes(), want) || !slices.Equal(text.Bytes(), want) {
		t.Fatalf("got %x want %x", b.Bytes(), want)
	}
	if b.AssembledSize() != text.AssembledSize() || i386.PhysicalSize(ins, abi.StackNoUse, 0) != len(want) {
		t.Fatal("byte/text/size PC mismatch")
	}
	if !strings.Contains(text.Text(), mnemonic) {
		t.Fatalf("missing %s in %s", mnemonic, text.Text())
	}
	if i386.FlagEffect(ins) {
		t.Fatal("SSE2 operation changes integer flags")
	}
}

func TestSSE2FloatPairListing(t *testing.T) {
	a := i386.NewTextAssembler(abi.StackNoUse)
	ins := machine.Instruction{Op: machine.OpFloatBinary, Arithmetic: op.OpFAdd, Bits: 64,
		A: 0, HighA: 2, B: 0, HighB: 2, C: 3, HighC: 1}
	if err := machine.Visit(ins, a); err != nil {
		t.Fatal(err)
	}
	want := `movd xmm0, EAX
movd xmm2, EDX
punpckldq xmm0, xmm2
movd xmm1, EBX
movd xmm2, ECX
punpckldq xmm1, xmm2
addsd xmm0, xmm1
movd EAX, xmm0
pshufd xmm2, xmm0, 0x55
movd EDX, xmm2
`
	if a.Text() != want {
		t.Fatalf("listing:\n%s\nwant:\n%s", a.Text(), want)
	}
}

// Pin the noncommutative dst==right shape independently of allocator choices.
func TestFloatBinaryRightHomeAlias(t *testing.T) {
	for _, width := range []uint8{32, 64} {
		prefix, suffix := "f3", "ss"
		if width == 64 {
			prefix, suffix = "f2", "sd"
		}
		for _, op := range []struct {
			op         op.Opcode
			name, code string
		}{
			{op.OpFSub, "sub", "5c"}, {op.OpFDiv, "div", "5e"},
		} {
			t.Run(fmt.Sprintf("%s/%d", op.name, width), func(t *testing.T) {
				ins := machine.Instruction{Op: machine.OpFloatBinary, Arithmetic: op.op, Bits: width,
					A: backend.VectorRegister(7), B: backend.VectorRegister(3), C: backend.VectorRegister(7)}
				// movapd xmm1,xmm7; movapd xmm7,xmm3; sub/div xmm7,xmm1
				checkFloat(t, ins, "660f28cf660f28fb"+prefix+"0f"+op.code+"f9", op.name+suffix)
			})
		}
	}
}
