package amd64

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// The expected bytes were checked with the x86asm disassembler.
func TestAtomicEncodings(t *testing.T) {
	atomic := func(o op.Opcode, w uint8, a, b, c, d uint8) machine.Instruction {
		return machine.Instruction{Op: machine.OpAtomic, Arithmetic: o, Bits: w, A: backend.Register(a), B: backend.Register(b), C: backend.Register(c), D: backend.Register(d)}
	}
	for _, tt := range []struct {
		ins  machine.Instruction
		want string
	}{
		{atomic(op.OpAtomicLoad, 8, 15, 12, 0, 0), "450fb63c24"},                        // movzx r15d, byte ptr [r12]
		{atomic(op.OpAtomicLoad, 64, 6, 13, 0, 0), "498b7500"},                          // mov rsi, [r13]
		{atomic(op.OpAtomicStore, 8, 0, 13, 12, 0), "4d89e345865d00"},                   // mov r11, r12; xchg byte ptr [r13], r11b
		{atomic(op.OpAtomicAdd, 8, 6, 13, 12, 0), "4d89e3f0450fc05d00410fb6f3"},         // mov r11, r12; lock xadd byte ptr [r13], r11b; movzx esi, r11b
		{atomic(op.OpAtomicSwap, 16, 3, 6, 7, 0), "4989fb6644871e410fb7db"},             // mov r11, rdi; xchg word ptr [rsi], r11w; movzx ebx, r11w
		{atomic(op.OpAtomicAnd, 32, 3, 6, 7, 0), "8b064989c34121fbf0440fb11e75f389c3"},  // mov eax, [rsi]; L: mov r11, rax; and r11d, edi; lock cmpxchg [rsi], r11d; jnz L; mov ebx, eax
		{atomic(op.OpAtomicCompareSwap, 8, 15, 12, 5, 6), "4889e8f0410fb03424440fb6f8"}, // mov rax, rbp; lock cmpxchg byte ptr [r12], sil; movzx r15d, al
		{atomic(op.OpAtomicCompareSwap, 64, 3, 6, 7, 8), "4889f8f04c0fb1064889c3"},      // mov rax, rdi; lock cmpxchg [rsi], r8; mov rbx, rax
	} {
		got := checkExpansion(t, tt.ins)
		if hex.EncodeToString(got) != tt.want {
			t.Errorf("%v/%d: % x, want %s", tt.ins.Arithmetic, tt.ins.Bits, got, tt.want)
		}
	}
}

func TestAtomicRejections(t *testing.T) {
	good := machine.Instruction{Op: machine.OpAtomic, Arithmetic: op.OpAtomicCompareSwap, Bits: 32, A: 3, B: 6, C: 7, D: 8}
	checkExpansion(t, good)
	// A is written last, so it may equal any input.
	for _, alias := range []func(*machine.Instruction){
		func(i *machine.Instruction) { i.A = 6 },
		func(i *machine.Instruction) { i.A = 7 },
		func(i *machine.Instruction) { i.A = 8 },
	} {
		ins := good
		alias(&ins)
		checkExpansion(t, ins)
	}
	for _, bad := range []func(*machine.Instruction){
		func(i *machine.Instruction) { i.Bits = 12 },
		func(i *machine.Instruction) { i.Imm = 1 },
		func(i *machine.Instruction) { i.Arithmetic = op.OpAdd },
		func(i *machine.Instruction) { i.B = 16 },
		func(i *machine.Instruction) { i.C = 0 },
		func(i *machine.Instruction) { i.A = 0 },
		func(i *machine.Instruction) { i.ByteOrder = op.ByteOrderLittleEndian },
	} {
		ins := good
		bad(&ins)
		b := NewByteAssembler(abi.StackPlatform, 0)
		if err := machine.Visit(ins, b); err == nil && b.Err() == nil {
			t.Errorf("%+v assembled", ins)
		}
		if PhysicalSize(ins, abi.StackPlatform, 0) != 0 {
			t.Errorf("%+v has a size", ins)
		}
	}
	// Each form's temporaries are rejected for every operand.
	for o := op.OpAtomicLoad; o <= op.OpAtomicCompareSwap; o++ {
		for _, r := range atomicTemporaries(o) {
			for _, field := range []string{"A", "B", "C", "D"} {
				ins := machine.Instruction{Op: machine.OpAtomic, Arithmetic: o, Bits: 64, A: 3, B: 6, C: 7, D: 8}
				switch field {
				case "A":
					ins.A = backend.Register(r)
				case "B":
					ins.B = backend.Register(r)
				case "C":
					ins.C = backend.Register(r)
				case "D":
					ins.D = backend.Register(r)
				}
				read := field == "B" || field == "A" && o != op.OpAtomicStore || field == "C" && o != op.OpAtomicLoad || field == "D" && o == op.OpAtomicCompareSwap
				if size := PhysicalSize(ins, abi.StackPlatform, 0); (size == 0) != read {
					t.Errorf("%v with %s=%d: size %d", o, field, r, size)
				}
			}
		}
	}
}

func TestAtomicListingAndFlags(t *testing.T) {
	ins := machine.Instruction{Op: machine.OpAtomic, Arithmetic: op.OpAtomicXor, Bits: 8, A: 3, B: 6, C: 7}
	x := NewTextAssembler(abi.StackPlatform, 0)
	if err := machine.Visit(ins, x); err != nil {
		t.Fatal(err)
	}
	if text := x.Text(); !strings.Contains(text, "lock cmpxchg byte ptr [RSI], R11B") || !strings.Contains(text, "jnz 0x3") {
		t.Fatalf("listing:\n%s", text)
	}
	for o := op.OpAtomicLoad; o <= op.OpAtomicCompareSwap; o++ {
		ins.Arithmetic = o
		writes := o != op.OpAtomicLoad && o != op.OpAtomicStore && o != op.OpAtomicSwap
		if FlagEffect(ins) != writes || (FlagFacts(ins).Modified != 0) != writes {
			t.Errorf("%v: flag effect %v, want %v", o, FlagEffect(ins), writes)
		}
		if !(Policy{}).CanAtomic(o, 8) || (Policy{}).CanAtomic(o, 12) {
			t.Errorf("%v: CanAtomic", o)
		}
	}
	if (Policy{}).CanAtomic(op.OpAdd, 64) || (Policy{}).CanAtomic(op.OpFence, 64) {
		t.Error("CanAtomic accepts a non-atomic operation")
	}
}
