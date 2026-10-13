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

func TestPairedImmediateShiftEncodings(t *testing.T) {
	for _, tt := range []struct {
		name       string
		op         op.Opcode
		n          uint64
		code, text string
	}{
		{"left7", op.OpShiftLeft, 7, "0fa4c207c1e007", "shld EDX, EAX, 7\nshl EAX, 7\n"},
		{"right7", op.OpShiftRight, 7, "0facd007c1ea07", "shrd EAX, EDX, 7\nshr EDX, 7\n"},
		{"signed7", op.OpShiftRightArithmetic, 7, "0facd007c1fa07", "shrd EAX, EDX, 7\nsar EDX, 7\n"},
		{"rotate-left7", op.OpRotateLeft, 7, "89c10fa4d0070fa4ca07", "mov ECX, EAX\nshld EAX, EDX, 7\nshld EDX, ECX, 7\n"},
		{"rotate-right7", op.OpRotateRight, 7, "89c10facd0070facca07", "mov ECX, EAX\nshrd EAX, EDX, 7\nshrd EDX, ECX, 7\n"},
		{"left32", op.OpShiftLeft, 32, "89c2b800000000", "mov EDX, EAX\nmov EAX, 0\n"},
		{"right32", op.OpShiftRight, 32, "89d0ba00000000", "mov EAX, EDX\nmov EDX, 0\n"},
		{"signed32", op.OpShiftRightArithmetic, 32, "89d0c1fa1f", "mov EAX, EDX\nsar EDX, 31\n"},
		{"rotate32", op.OpRotateLeft, 32, "89c189d089ca", "mov ECX, EAX\nmov EAX, EDX\nmov EDX, ECX\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ins := machine.Instruction{Op: machine.OpShift, Arithmetic: tt.op, Bits: 64, A: 0, HighA: 2, B: 0, HighB: 2, C: 1, Imm: tt.n}
			b, a := i386.NewByteAssembler(abi.StackNoUse), i386.NewTextAssembler(abi.StackNoUse)
			if err := machine.Visit(ins, b); err != nil {
				t.Fatal(err)
			}
			if err := machine.Visit(ins, a); err != nil {
				t.Fatal(err)
			}
			want, err := hex.DecodeString(tt.code)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(b.Bytes(), want) || !bytes.Equal(a.Bytes(), want) || a.Text() != tt.text {
				t.Fatalf("bytes=%x listing=%s; want %x %s", b.Bytes(), a.Text(), want, tt.text)
			}
			if i386.PhysicalSize(ins, abi.StackNoUse, 0) != len(want) || !i386.FlagEffect(ins) {
				t.Fatal("wrong size/flag effect")
			}
		})
	}
}

func TestWideSymbolRelocation(t *testing.T) {
	for _, value := range []uint64{0xffffffff, 0x100000000, 0x123456789abcdef0, ^uint64(0)} {
		ins := machine.Instruction{Op: machine.OpSymbol, Bits: 64, A: 0, HighA: 2, Symbol: "wide"}
		b, a := i386.NewByteAssembler(abi.StackNoUse), i386.NewTextAssembler(abi.StackNoUse)
		if err := machine.Visit(ins, b); err != nil {
			t.Fatal(err)
		}
		if err := machine.Visit(ins, a); err != nil {
			t.Fatal(err)
		}
		if a.AssembledSize() != 10 || i386.PhysicalSize(ins, abi.StackNoUse, 0) != 10 || !strings.Contains(a.Text(), "high32(wide)") {
			t.Fatal("wrong size/listing")
		}
		raw := append([]byte(nil), b.Bytes()...)
		linked, err := machine.LinkRelocations(raw, b.Relocations(), map[string]uint64{"wide": value})
		want := i386.NewByteAssembler(abi.StackNoUse)
		want.Constant(0, uint64(uint32(value)))
		want.Constant(2, value>>32)
		if err != nil || !bytes.Equal(linked, want.Bytes()) || !bytes.Equal(raw, b.Bytes()) {
			t.Fatalf("value=%x linked=%x err=%v", value, linked, err)
		}
	}
	for _, offset := range []int{-1, 0, 1, 6, 100} {
		_, err := machine.LinkRelocations(make([]byte, 10), []machine.Relocation{{Symbol: "x", Offset: offset, Encoding: machine.Reloc386WideImmediate}}, map[string]uint64{"x": 0})
		if !errors.Is(err, backend.ErrInvalidFunction) {
			t.Fatalf("offset %d: %v", offset, err)
		}
	}
	b := i386.NewByteAssembler(abi.StackNoUse)
	b.SymbolInstruction(machine.Instruction{Bits: 64, A: 0, HighA: 2, Symbol: "x"})
	if _, err := machine.LinkRelocations(b.Bytes(), b.Relocations(), nil); !errors.Is(err, machine.ErrUnboundSymbol) {
		t.Fatal(err)
	}
}

func TestPairedShiftParallelCopy(t *testing.T) {
	a := i386.NewByteAssembler(abi.StackNoUse)
	i := machine.Instruction{Op: machine.OpShift, Arithmetic: op.OpShiftRight, Bits: 64, A: 0, HighA: 2, B: 2, HighB: 0, C: 1}
	if err := machine.Visit(i, a); err != nil {
		t.Fatal(err)
	}
	want, _ := hex.DecodeString("89d189c289c8")
	if !bytes.Equal(a.Bytes(), want) {
		t.Fatalf("swap=%x want%x", a.Bytes(), want)
	}
}
