package i386_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
)

func TestAbsoluteLiteralEncodings(t *testing.T) {
	for _, w := range []uint8{32, 64, 128} {
		for _, r := range []uint8{0, 3, 7} {
			i := machine.Instruction{Op: machine.OpLiteralLoad, A: backend.VectorRegister(r), Bits: w, Disp: 32}
			prefix, op, mnemonic := byte(0xf2), byte(0x10), "movsd"
			if w == 32 {
				prefix, mnemonic = 0xf3, "movss"
			}
			if w == 128 {
				prefix, op, mnemonic = 0xf3, 0x6f, "movdqu"
			}
			want := []byte{prefix, 0x0f, op, 0x05 | r<<3, 0, 0, 0, 0}
			a := i386.NewByteAssembler(abi.StackNoUse)
			x := i386.NewTextAssembler(abi.StackNoUse)
			if e := machine.Visit(i, a); e != nil {
				t.Fatal(e)
			}
			if e := machine.Visit(i, x); e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(a.Bytes(), want) || !bytes.Equal(x.Bytes(), want) || i386.PhysicalSize(i, abi.StackNoUse, 0) != 8 || x.AssembledSize() != 8 {
				t.Fatalf("width%d xmm%d bytes=%x/%x want%x", w, r, a.Bytes(), x.Bytes(), want)
			}
			if !strings.Contains(x.Text(), mnemonic) || !strings.Contains(x.Text(), "[literal + 0x20]") {
				t.Fatal(x.Text())
			}
			for _, rs := range [][]machine.Relocation{a.Relocations(), x.Relocations()} {
				if len(rs) != 1 || !rs[0].NeedsCodeBase() || rs[0].Offset != 0 || rs[0].TargetOffset != 32 || rs[0].Symbol != "" {
					t.Fatalf("relocations=%+v", rs)
				}
			}
			if i386.FlagEffect(i) {
				t.Fatal("pool load changes integer flags")
			}
		}
	}
	for _, i := range []machine.Instruction{{Op: machine.OpLiteralLoad, A: backend.VectorRegister(8), Bits: 64}, {Op: machine.OpLiteralLoad, A: 0, Bits: 64}, {Op: machine.OpLiteralLoad, A: backend.VectorRegister(3), Bits: 16}} {
		if e := machine.Visit(i, i386.NewByteAssembler(abi.StackNoUse)); !errors.Is(e, machine.ErrUnsupportedOperation) {
			t.Fatalf("invalid literal: %v", e)
		}
	}
}
