package i386_test

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
)

// The expected bytes were checked with the x86asm disassembler.
func TestAtomicEncodings(t *testing.T) {
	narrow := func(o op.Opcode, w uint8, a, b, c, d backend.Register) machine.Instruction {
		return machine.Instruction{Op: machine.OpAtomic, Arithmetic: o, Bits: w, A: a, B: b, C: c, D: d}
	}
	wide := func(o op.Opcode) machine.Instruction {
		return machine.Instruction{Op: machine.OpAtomic, Arithmetic: o, Bits: 64, A: 0, HighA: 2, Slot: 2}
	}
	for _, tt := range []struct {
		ins  machine.Instruction
		want string
	}{
		{narrow(op.OpAtomicLoad, 16, 3, 6, 0, 0), "0fb71e"},                         // movzx ebx, word ptr [esi]
		{narrow(op.OpAtomicStore, 8, 0, 6, 7, 0), "89fa8616"},                       // mov edx, edi; xchg byte ptr [esi], dl
		{narrow(op.OpAtomicAdd, 8, 3, 6, 7, 0), "89faf00fc0160fb6da"},               // mov edx, edi; lock xadd [esi], dl; movzx ebx, dl
		{narrow(op.OpAtomicOr, 32, 3, 6, 7, 0), "8b0689c209faf00fb11675f689c3"},     // mov eax, [esi]; L: mov edx, eax; or edx, edi; lock cmpxchg [esi], edx; jnz L; mov ebx, eax
		{narrow(op.OpAtomicCompareSwap, 8, 3, 6, 7, 1), "89f889caf00fb0160fb6d8"},   // mov eax, edi; mov edx, ecx; lock cmpxchg [esi], dl; movzx ebx, al
		{wide(op.OpAtomicLoad), "8bb42408000000f30f7e06660f7ec0660f73d020660f7ec2"}, // mov esi, [esp+8]; movq xmm0, [esi]; movd eax, xmm0; psrlq xmm0, 32; movd edx, xmm0
		{wide(op.OpAtomicStore), "8bb42408000000f30f7e84240c000000660fd6060faef0"},  // mov esi, [esp+8]; movq xmm0, [esp+12]; movq [esi], xmm0; mfence
		{wide(op.OpAtomicSwap), "8bb424080000008b068b56048b9c240c0000008b8c2410000000f00fc70e75fa"},
	} {
		b := i386.NewByteAssembler(abi.StackPlatform)
		if err := machine.Visit(tt.ins, b); err != nil || b.Err() != nil {
			t.Fatalf("%+v: %v %v", tt.ins, err, b.Err())
		}
		if got := hex.EncodeToString(b.Bytes()); got != tt.want {
			t.Errorf("%v/%d: %s, want %s", tt.ins.Arithmetic, tt.ins.Bits, got, tt.want)
		}
		if size := i386.PhysicalSize(tt.ins, abi.StackPlatform, 0); size != len(b.Bytes()) {
			t.Errorf("%v/%d: size %d, bytes %d", tt.ins.Arithmetic, tt.ins.Bits, size, len(b.Bytes()))
		}
		x := i386.NewTextAssembler(abi.StackPlatform)
		if err := machine.Visit(tt.ins, x); err != nil || x.Err() != nil {
			t.Fatalf("%+v: text %v %v", tt.ins, err, x.Err())
		}
	}
	x := i386.NewTextAssembler(abi.StackPlatform)
	if err := machine.Visit(wide(op.OpAtomicAdd), x); err != nil {
		t.Fatal(err)
	}
	if text := x.Text(); !strings.Contains(text, "adc ECX, dword ptr [ESP + 16]") || !strings.Contains(text, "lock cmpxchg8b qword ptr [ESI]") {
		t.Fatalf("listing:\n%s", text)
	}
}

func TestAtomicRejections(t *testing.T) {
	for _, ins := range []machine.Instruction{
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 12, A: 3, B: 6, C: 7},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 32, A: 3, B: 6, C: 7, Imm: 1},
		{Op: machine.OpAtomic, Arithmetic: op.OpAdd, Bits: 32, A: 3, B: 6, C: 7},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 32, A: 3, B: 8, C: 7},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 32, A: 3, B: 6, C: 2},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicStore, Bits: 16, B: 2, C: 7},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicXor, Bits: 8, A: 0, B: 6, C: 7},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicCompareSwap, Bits: 32, A: 3, B: 6, C: 7, D: 2},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 64, A: 0, HighA: 1, Slot: 2},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicLoad, Bits: 64, A: 3, HighA: 2, Slot: 2},
	} {
		b := i386.NewByteAssembler(abi.StackPlatform)
		if err := machine.Visit(ins, b); err == nil && b.Err() == nil {
			t.Errorf("%+v assembled", ins)
		}
		if i386.PhysicalSize(ins, abi.StackPlatform, 0) != 0 {
			t.Errorf("%+v has a size", ins)
		}
	}
	// A 64-bit store returns nothing, so A is not checked.
	if i386.PhysicalSize(machine.Instruction{Op: machine.OpAtomic, Arithmetic: op.OpAtomicStore, Bits: 64, A: 5, Slot: 1}, abi.StackPlatform, 0) == 0 {
		t.Error("64-bit store with a free A rejected")
	}
	for o := op.OpAtomicLoad; o <= op.OpAtomicCompareSwap; o++ {
		if !(i386.Policy{}).CanAtomic(o, 64) || (i386.Policy{}).CanAtomic(o, 128) {
			t.Errorf("%v: CanAtomic", o)
		}
		ins := machine.Instruction{Op: machine.OpAtomic, Arithmetic: o, Bits: 32}
		if writes := o != op.OpAtomicLoad && o != op.OpAtomicStore && o != op.OpAtomicSwap; i386.FlagEffect(ins) != writes {
			t.Errorf("%v: FlagEffect %v", o, !writes)
		}
	}
}
