package ppc64

import (
	"slices"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func atomicEmitter(t testing.TB, cpu abi.PPC64CPU) *Emitter {
	t.Helper()
	profile, err := abi.NewPPC64Profile("ppc64le", abi.C)
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEmitterForCPU(profile, abi.StackPlatform, false, cpu)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// Words checked with GNU objdump (powerpc:common64).
func TestAtomicReservationEncodings(t *testing.T) {
	for _, test := range []struct {
		ins  Instruction
		word uint32
		text string
	}{
		{Instruction{Op: lbarx, A: 11, C: 4}, 0x7d602068, "lbarx r11, 0, r4"},
		{Instruction{Op: lharx, A: 11, C: 4}, 0x7d6020e8, "lharx r11, 0, r4"},
		{Instruction{Op: lwarx, A: 11, C: 4}, 0x7d602028, "lwarx r11, 0, r4"},
		{Instruction{Op: ldarx, A: 11, C: 4}, 0x7d6020a8, "ldarx r11, 0, r4"},
		{Instruction{Op: stbcx, A: 12, C: 4}, 0x7d80256d, "stbcx. r12, 0, r4"},
		{Instruction{Op: sthcx, A: 12, C: 4}, 0x7d8025ad, "sthcx. r12, 0, r4"},
		{Instruction{Op: stwcx, A: 12, C: 4}, 0x7d80212d, "stwcx. r12, 0, r4"},
		{Instruction{Op: stdcx, A: 12, C: 4}, 0x7d8021ad, "stdcx. r12, 0, r4"},
	} {
		got, err := Encode(test.ins)
		if err != nil || got != test.word || instructionText(test.ins) != test.text {
			t.Errorf("%+v = %#x,%v %q; want %#x %q", test.ins, got, err, instructionText(test.ins), test.word, test.text)
		}
		if test.ins.storeConditional() != (test.ins.writesGeneral() == false) || test.ins.storeConditional() != test.ins.recordsCR0() {
			t.Errorf("%+v: register or CR0 roles", test.ins)
		}
	}
}

func TestAtomicExpansions(t *testing.T) {
	want := map[op.Opcode]int{op.OpAtomicLoad: 5, op.OpAtomicStore: 2, op.OpAtomicAdd: 7, op.OpAtomicAnd: 7, op.OpAtomicOr: 7,
		op.OpAtomicXor: 7, op.OpAtomicSwap: 6, op.OpAtomicCompareSwap: 8}
	for _, cpu := range []abi.PPC64CPU{abi.PPC64Power8, abi.PPC64Power9, abi.PPC64Power10} {
		e := atomicEmitter(t, cpu)
		for o := op.OpAtomicLoad; o <= op.OpAtomicCompareSwap; o++ {
			for _, w := range []uint8{8, 16, 32, 64} {
				ins := machine.Instruction{Op: machine.OpAtomic, Arithmetic: o, Bits: w, A: 3, B: 4, C: 5, D: 6}
				sequence, err := e.Expand(ins, 0x40)
				if err != nil || len(sequence) != want[o] || e.Size(ins) != 4*want[o] {
					t.Fatalf("%v/%d: %d instructions, size %d, %v", o, w, len(sequence), e.Size(ins), err)
				}
				text := NewTextAssembler()
				bytes := NewByteAssembler()
				for _, native := range sequence {
					if err := text.Visit(native); err != nil {
						t.Fatal(err)
					}
					if err := bytes.Visit(native); err != nil {
						t.Fatal(err)
					}
				}
				if strings.Count(text.Text(), "\n") != want[o] || len(bytes.Bytes()) != 4*want[o] {
					t.Fatalf("%v/%d: %q", o, w, text.Text())
				}
				writes := e.ImplicitWrites(ins)
				switch o {
				case op.OpAtomicLoad:
					if !slices.Equal(writes, []backend.Register{3}) {
						t.Errorf("%v/%d: writes %v", o, w, writes)
					}
				case op.OpAtomicStore:
					if len(writes) != 0 {
						t.Errorf("%v/%d: writes %v", o, w, writes)
					}
				default:
					if !slices.Contains(writes, 11) || !slices.Contains(writes, 3) || slices.Contains(writes, 5) {
						t.Errorf("%v/%d: writes %v", o, w, writes)
					}
				}
				if e.FlagEffect(ins) != (o != op.OpAtomicStore) {
					t.Errorf("%v/%d: flag effect %v", o, w, e.FlagEffect(ins))
				}
			}
		}
	}
}

func TestAtomicRejects(t *testing.T) {
	e := atomicEmitter(t, abi.PPC64Power8)
	for _, ins := range []machine.Instruction{
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 12, A: 3, B: 4, C: 5},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 32, A: 3, B: 4, C: 5, Imm: 1},
		{Op: machine.OpAtomic, Arithmetic: op.OpAdd, Bits: 32, A: 3, B: 4, C: 5},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicAdd, Bits: 32, A: 4, B: 4, C: 5},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicCompareSwap, Bits: 32, A: 6, B: 4, C: 5, D: 6},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicLoad, Bits: 64, A: 3, B: 0},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicLoad, Bits: 64, A: 11, B: 4},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicSwap, Bits: 64, A: 3, B: 4, C: 12},
		{Op: machine.OpAtomic, Arithmetic: op.OpAtomicLoad, Bits: 64, A: 3, B: backend.VectorRegister(4)},
	} {
		if _, err := e.Expand(ins, 0); err == nil || e.Size(ins) != 0 {
			t.Errorf("%+v expanded", ins)
		}
	}
	store := machine.Instruction{Op: machine.OpAtomic, Arithmetic: op.OpAtomicStore, Bits: 8, A: 4, B: 4, C: 5}
	if e.Size(store) != 8 {
		t.Error("a store with an ignored A was rejected")
	}
	if !(Policy{}).CanAtomic(op.OpAtomicCompareSwap, 8) || (Policy{}).CanAtomic(op.OpFence, 64) || (Policy{}).CanAtomic(op.OpAtomicLoad, 12) {
		t.Error("CanAtomic")
	}
}
