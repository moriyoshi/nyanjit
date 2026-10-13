package loong64

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

// atomicForm uses A=8, B=9, C=10, D=11: argument registers outside the
// temporaries.
func atomicForm(operation op.Opcode, w uint8) machine.Instruction {
	return machine.Instruction{Op: machine.OpAtomic, Arithmetic: operation, Bits: w, A: 8, B: 9, C: 10, D: 11}
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
			if strings.Contains(a.Text(), "%!") {
				t.Fatalf("%v/%d: bad listing:\n%s", operation, w, a.Text())
			}
			checkLoop(t, operation, w, a.Bytes())
		}
	}
}

// checkLoop checks that each ll/sc loop holds only register arithmetic and
// a forward exit between ll and the retry, with no other memory access.
func checkLoop(t *testing.T, operation op.Opcode, w uint8, code []byte) {
	t.Helper()
	words := make([]uint32, len(code)/4)
	for i := range words {
		words[i] = binary.LittleEndian.Uint32(code[i*4:])
	}
	for i, word := range words {
		if word&0xfd000000 != 0x20000000 { // ll.w, ll.d
			continue
		}
		end := -1
		for j := i + 1; j < len(words); j++ {
			if words[j]&0xfc000000 == 0x40000000 { // beqz back
				end = j
				break
			}
		}
		if end < 0 || end-i+1 > 16 {
			t.Fatalf("%v/%d: ll at %d has no short retry", operation, w, i)
		}
		for _, inner := range words[i+1 : end] {
			switch {
			case inner&0xfd000000 == 0x21000000: // sc
			case inner&0xfc000000 == 0x5c000000: // bne forward
				if int32(inner<<6)>>16 <= 0 {
					t.Fatalf("%v/%d: backward bne inside the loop", operation, w)
				}
			case inner>>22 == 0 || inner&0xffc00000 == 0x02800000 || inner&0xffc00000 == 0x03400000:
			default:
				t.Fatalf("%v/%d: word %08x is not allowed inside an ll/sc loop", operation, w, inner)
			}
		}
	}
}

func TestAtomicRejectsInvalidForms(t *testing.T) {
	cases := []machine.Instruction{
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 12, A: 8, B: 9, C: 10},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 32, Imm: 1, A: 8, B: 9, C: 10},
		{Op: machine.OpAtomic, Arithmetic: op.OpAdd, Bits: 32, A: 8, B: 9, C: 10},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 32, A: 8, B: backend.VectorRegister(1), C: 10},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 32, A: 9, B: 9, C: 10},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 64, A: 10, B: 9, C: 10},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicCompareSwap, Bits: 32, A: 11, B: 9, C: 10, D: 11},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 32, A: 0, B: 9, C: 10},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 32, A: 8, B: 12, C: 10},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 8, A: 8, B: 13, C: 10},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicCompareSwap, Bits: 16, A: 8, B: 9, C: 10, D: 14},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicSwap, Bits: 8, A: 13, B: 9, C: 10},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicLoad, Bits: 64, A: 20, B: 9},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicStore, Bits: 64, B: 9, C: 19},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicLoad, Bits: 64, A: 8, B: 9, ByteOrder: op.ByteOrderBigEndian},
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
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicStore, Bits: 8, A: 10, B: 9, C: 10},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicLoad, Bits: 8, A: 8, B: 9, C: 8},
	} {
		if PhysicalSize(ins, abi.StackPlatform, 0) == 0 {
			t.Errorf("%+v rejected", ins)
		}
	}
}

// TestAtomicImplicitWritesTemplate checks that the temporaries follow from
// Arithmetic and Bits alone, as the Var-home planner queries them.
func TestAtomicImplicitWritesTemplate(t *testing.T) {
	bitwise := []backend.Register{12, 19, 20}
	want := map[op.Opcode]map[uint8][]backend.Register{
		op.OpAtomicLoad:        {8: nil, 16: nil, 32: nil, 64: nil},
		op.OpAtomicStore:       {8: nil, 16: nil, 32: nil, 64: nil},
		op.OpAtomicAdd:         {8: {12, 19, 20, 13}, 16: {12, 19, 20, 13}, 32: nil, 64: nil},
		op.OpAtomicSwap:        {8: {12, 19, 20, 13}, 16: {12, 19, 20, 13}, 32: nil, 64: nil},
		op.OpAtomicAnd:         {8: bitwise, 16: bitwise, 32: nil, 64: nil},
		op.OpAtomicOr:          {8: bitwise, 16: bitwise, 32: nil, 64: nil},
		op.OpAtomicXor:         {8: bitwise, 16: bitwise, 32: nil, 64: nil},
		op.OpAtomicCompareSwap: {8: {12, 19, 20, 13, 14}, 16: {12, 19, 20, 13, 14}, 32: {12, 19, 20}, 64: {12, 19}},
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
				rd := backend.Register(word & 31)
				if word&0xff000000 == 0x29000000 || word>>26 >= 0x10 || word == 0x38720000 {
					continue // stores, branches, dbar
				}
				if rd != 0 && rd != ins.A && !slices.Contains(regs, rd) {
					t.Errorf("%v/%d writes r%d (%08x), not reported", operation, w, rd, word)
				}
			}
		}
	}
}
