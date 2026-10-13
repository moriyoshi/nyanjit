package riscv64

import (
	"encoding/binary"
	"slices"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

var atomicOperations = []op.Opcode{op.OpAtomicLoad, op.OpAtomicStore, op.OpAtomicAdd, op.OpAtomicAnd, op.OpAtomicOr, op.OpAtomicXor, op.OpAtomicSwap, op.OpAtomicCompareSwap}

// atomicForm uses A=17, B=14, C=15, D=16: argument registers outside the
// temporaries.
func atomicForm(operation op.Opcode, w uint8) machine.Instruction {
	return machine.Instruction{Op: machine.OpAtomic, Arithmetic: operation, Bits: w, A: 17, B: 14, C: 15, D: 16}
}

// referenceAtomic applies operation to the w-bit field old and returns the
// new field value.
func referenceAtomic(operation op.Opcode, w uint8, old, c, d uint64) uint64 {
	m := ^uint64(0) >> (64 - w)
	switch operation {
	case op.OpAtomicStore, op.OpAtomicSwap:
		return c & m
	case op.OpAtomicAdd:
		return (old + c) & m
	case op.OpAtomicAnd:
		return old & c
	case op.OpAtomicOr:
		return old | c
	case op.OpAtomicXor:
		return old ^ c
	case op.OpAtomicCompareSwap:
		if old == c {
			return d
		}
	}
	return old
}

func TestAtomicFormsAssemble(t *testing.T) {
	for _, operation := range atomicOperations {
		for _, w := range []uint8{8, 16, 32, 64} {
			ins := atomicForm(operation, w)
			a := NewTextAssembler(abi.StackPlatform, 0)
			if err := machine.Visit(ins, a); err != nil {
				t.Fatalf("%v/%d: %v", operation, w, err)
			}
			if size := PhysicalSize(ins, abi.StackPlatform, 0); size != len(a.Bytes()) || size == 0 {
				t.Fatalf("%v/%d: size %d, bytes %d", operation, w, size, len(a.Bytes()))
			}
			if strings.Contains(a.Text(), ".word") {
				t.Fatalf("%v/%d: undecoded word:\n%s", operation, w, a.Text())
			}
			checkConstrainedLoop(t, operation, w, a.Bytes())
		}
	}
}

// checkConstrainedLoop finds each LR/SC loop and checks the RVWMO
// constrained-loop rules: at most 16 instructions from lr to the retry
// branch, all base-I ALU or forward branches besides lr and sc.
func checkConstrainedLoop(t *testing.T, operation op.Opcode, w uint8, code []byte) {
	t.Helper()
	words := make([]uint32, len(code)/4)
	for i := range words {
		words[i] = binary.LittleEndian.Uint32(code[i*4:])
	}
	for i, word := range words {
		if word&0x7f != 0x2f || word>>27 != amoLR {
			continue
		}
		end := -1
		for j := i + 1; j < len(words); j++ {
			if words[j]&0x7f == 0x63 && signBits(words[j]>>31<<12|words[j]>>7&1<<11|words[j]>>25&63<<5|words[j]>>8&15<<1, 13) < 0 {
				end = j
				break
			}
		}
		if end < 0 || end-i+1 > 16 {
			t.Fatalf("%v/%d: LR at %d has no short retry (end %d)", operation, w, i, end)
		}
		for _, inner := range words[i+1 : end] {
			switch inner & 0x7f {
			case 0x13, 0x1b, 0x37:
			case 0x33:
				if inner>>25 == 1 {
					t.Fatalf("%v/%d: M-extension word %08x inside the loop", operation, w, inner)
				}
			case 0x63: // forward exits only, checked above
			case 0x2f:
				if inner>>27 != amoSC {
					t.Fatalf("%v/%d: AMO %08x inside the loop", operation, w, inner)
				}
			default:
				t.Fatalf("%v/%d: word %08x is not allowed inside an LR/SC loop", operation, w, inner)
			}
		}
	}
}

func TestAtomicRejectsInvalidForms(t *testing.T) {
	cases := []machine.Instruction{
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 12, A: 17, B: 14, C: 15},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 32, Imm: 1, A: 17, B: 14, C: 15},
		{Op: machine.OpAtomic, Arithmetic: op.OpAdd, Bits: 32, A: 17, B: 14, C: 15},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 32, A: 17, B: backend.VectorRegister(1), C: 15},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 32, A: 14, B: 14, C: 15},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 32, A: 15, B: 14, C: 15},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicCompareSwap, Bits: 32, A: 16, B: 14, C: 15, D: 16},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 32, A: 0, B: 14, C: 15},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 32, A: 17, B: 30, C: 15},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 8, A: 17, B: 5, C: 15},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicCompareSwap, Bits: 16, A: 17, B: 14, C: 15, D: 28},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicSwap, Bits: 8, A: 6, B: 14, C: 15},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicLoad, Bits: 64, A: 31, B: 14},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicStore, Bits: 64, B: 14, C: 31},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicLoad, Bits: 64, A: 17, B: 14, ByteOrder: op.ByteOrderBigEndian},
	}
	for _, ins := range cases {
		if err := machine.Visit(ins, NewByteAssembler(abi.StackPlatform, 0)); err == nil {
			t.Errorf("%+v assembled", ins)
		}
		if PhysicalSize(ins, abi.StackPlatform, 0) != 0 {
			t.Errorf("%+v has a size", ins)
		}
	}
	// A store reads no destination, and a load reads no operand.
	for _, ins := range []machine.Instruction{
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicStore, Bits: 8, A: 15, B: 14, C: 15},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicLoad, Bits: 8, A: 17, B: 14, C: 17},
	} {
		if PhysicalSize(ins, abi.StackPlatform, 0) == 0 {
			t.Errorf("%+v rejected", ins)
		}
	}
}

// TestAtomicImplicitWritesTemplate checks that the temporaries follow from
// Arithmetic and Bits alone, as the Var-home planner queries them.
func TestAtomicImplicitWritesTemplate(t *testing.T) {
	want := map[op.Opcode]map[uint8][]backend.Register{
		op.OpAtomicLoad:        {8: nil, 16: nil, 32: nil, 64: nil},
		op.OpAtomicStore:       {8: nil, 16: nil, 32: nil, 64: nil},
		op.OpAtomicAdd:         {8: {5, 6, 7, 30, 31}, 16: {5, 6, 7, 30, 31}, 32: nil, 64: nil},
		op.OpAtomicSwap:        {8: {5, 6, 7, 30, 31}, 16: {5, 6, 7, 30, 31}, 32: nil, 64: nil},
		op.OpAtomicAnd:         {8: {30, 31}, 16: {30, 31}, 32: nil, 64: nil},
		op.OpAtomicOr:          {8: {30, 31}, 16: {30, 31}, 32: nil, 64: nil},
		op.OpAtomicXor:         {8: {30, 31}, 16: {30, 31}, 32: nil, 64: nil},
		op.OpAtomicCompareSwap: {8: {5, 6, 7, 28, 30, 31}, 16: {5, 6, 7, 28, 30, 31}, 32: {30, 31}, 64: {30, 31}},
	}
	for operation, widths := range want {
		for w, regs := range widths {
			template := machine.Instruction{Op: machine.OpAtomic, Arithmetic: operation, Bits: w}
			got := ImplicitWrites(template, abi.StackPlatform, 0)
			if !slices.Equal(got, regs) {
				t.Errorf("%v/%d template: %v, want %v", operation, w, got, regs)
			}
			// The expansion writes nothing else besides A.
			a := NewByteAssembler(abi.StackPlatform, 0)
			ins := atomicForm(operation, w)
			if err := machine.Visit(ins, a); err != nil {
				t.Fatal(err)
			}
			code := a.Bytes()
			for i := 0; i < len(code); i += 4 {
				word := binary.LittleEndian.Uint32(code[i:])
				rd := backend.Register(word >> 7 & 31)
				switch word & 0x7f {
				case 0x23, 0x63, 0x0f:
					continue
				}
				if rd != 0 && rd != ins.A && !slices.Contains(regs, rd) {
					t.Errorf("%v/%d writes x%d (%08x), not reported", operation, w, rd, word)
				}
			}
		}
	}
}
