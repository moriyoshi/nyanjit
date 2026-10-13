package ppc64_test

import (
	"math"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
)

// The words come from Go's ppc64le assembler (FCMPU F4, F5; CROR/CRNOR on
// CR0LT..CR0SO; MOVW CR, R3; RLWNM $s, R3, $1, R3), disassembled with
// golang.org/x/arch/ppc64/ppc64asm in GNU syntax as shown in the comments.
var floatCompareWords = [...][]uint32{
	op.FloatEqual:                 {0xfc042800, 0x7c600026, 0x54631ffe},             // rlwinm r3,r3,3,31,31
	op.FloatNotEqual:              {0xfc042800, 0x4c421042, 0x7c600026, 0x54631ffe}, // crnor eq,eq,eq
	op.FloatLess:                  {0xfc042800, 0x7c600026, 0x54630ffe},             // rlwinm r3,r3,1,31,31
	op.FloatUnorderedGreaterEqual: {0xfc042800, 0x4c000042, 0x7c600026, 0x54630ffe}, // crnor lt,lt,lt
	op.FloatLessEqual:             {0xfc042800, 0x4c001382, 0x7c600026, 0x54630ffe}, // cror lt,lt,eq
	op.FloatUnorderedGreater:      {0xfc042800, 0x4c001042, 0x7c600026, 0x54630ffe}, // crnor lt,lt,eq
	op.FloatGreater:               {0xfc042800, 0x7c600026, 0x546317fe},             // rlwinm r3,r3,2,31,31
	op.FloatUnorderedLessEqual:    {0xfc042800, 0x4c210842, 0x7c600026, 0x546317fe}, // crnor gt,gt,gt
	op.FloatGreaterEqual:          {0xfc042800, 0x4c211382, 0x7c600026, 0x546317fe}, // cror gt,gt,eq
	op.FloatUnorderedLess:         {0xfc042800, 0x4c211042, 0x7c600026, 0x546317fe}, // crnor gt,gt,eq
	op.FloatOrdered:               {0xfc042800, 0x4c631842, 0x7c600026, 0x546327fe}, // crnor so,so,so
	op.FloatUnordered:             {0xfc042800, 0x7c600026, 0x546327fe},             // rlwinm r3,r3,4,31,31
	op.FloatOrderedNotEqual:       {0xfc042800, 0x4c000b82, 0x7c600026, 0x54630ffe}, // cror lt,lt,gt
	op.FloatUnorderedEqual:        {0xfc042800, 0x4c000842, 0x7c600026, 0x54630ffe}, // crnor lt,lt,gt
}

func TestFloatCompareEncodings(t *testing.T) {
	for _, test := range []struct {
		ins  ppc64.Instruction
		word uint32
	}{
		{ppc64.Instruction{Op: ppc64.Cror, A: 0, B: 0, C: 2}, 0x4c001382},  // cror lt,lt,eq
		{ppc64.Instruction{Op: ppc64.Cror, A: 3, B: 1, C: 0}, 0x4c610382},  // cror so,gt,lt
		{ppc64.Instruction{Op: ppc64.Crnor, A: 3, B: 3, C: 3}, 0x4c631842}, // crnor so,so,so
		{ppc64.Instruction{Op: ppc64.Crnor, A: 0, B: 2, C: 0}, 0x4c020042}, // crnor lt,eq,lt
	} {
		got, err := ppc64.Encode(test.ins)
		if err != nil || got != test.word {
			t.Errorf("%+v = %#x,%v; want %#x", test.ins, got, err, test.word)
		}
	}
	if _, err := ppc64.Encode(ppc64.Instruction{Op: ppc64.Cror, A: 32}); err == nil {
		t.Error("cror of CR bit 32 encoded")
	}
}

func TestFloatCompareExpansion(t *testing.T) {
	for _, cpu := range []abi.PPC64CPU{abi.PPC64Power8, abi.PPC64Power9} {
		profile, err := abi.NewPPC64Profile("ppc64le", abi.C)
		if err != nil {
			t.Fatal(err)
		}
		e, err := ppc64.NewEmitterForCPU(profile, abi.StackPlatform, false, cpu)
		if err != nil {
			t.Fatal(err)
		}
		for c, want := range floatCompareWords {
			for _, w := range []uint8{32, 64} {
				ins := machine.Instruction{Op: machine.OpFloatCompare, Bits: w, Imm: uint64(c), A: 3, B: backend.VectorRegister(4), C: backend.VectorRegister(5)}
				seq, err := e.Expand(ins, 0)
				if err != nil {
					t.Fatalf("condition %d, %d bits: %v", c, w, err)
				}
				a := ppc64.NewByteAssembler()
				for _, native := range seq {
					if err := a.Visit(native); err != nil {
						t.Fatal(err)
					}
				}
				got := a.Bytes()
				if len(got) != 4*len(want) || e.Size(ins) != len(got) {
					t.Fatalf("condition %d, %d bits: %d bytes (Size %d), want %d", c, w, len(got), e.Size(ins), 4*len(want))
				}
				for i, word := range want {
					if g := uint32(got[4*i]) | uint32(got[4*i+1])<<8 | uint32(got[4*i+2])<<16 | uint32(got[4*i+3])<<24; g != word {
						t.Errorf("condition %d, %d bits, word %d: %#x, want %#x", c, w, i, g, word)
					}
				}
				if !e.FlagEffect(ins) || e.FlagFacts(ins).Modified != machine.FlagSign|machine.FlagZero {
					t.Errorf("condition %d: CR0 write not reported", c)
				}
				if writes := e.ImplicitWrites(ins); len(writes) != 1 || writes[0] != 3 {
					t.Errorf("condition %d: implicit writes %v", c, writes)
				}
				var cr0, fpscr bool
				for _, r := range e.SpecialWrites(ins) {
					switch r {
					case machine.Resource{Class: machine.ResourceConditionField}:
						cr0 = true
					case machine.Resource{Class: machine.ResourceFloatingStatus}:
						fpscr = true
					default:
						t.Errorf("condition %d: writes %+v", c, r)
					}
				}
				if !cr0 || !fpscr {
					t.Errorf("condition %d: special writes %+v", c, e.SpecialWrites(ins))
				}
				for x, y := range map[float64]float64{1: 2, 2: 1, 3: 3, math.NaN(): 1, 0: math.NaN()} {
					if got, want := runFloatCompare(t, seq, x, y), floatConditionHolds(op.FloatCondition(c), x, y); got != want {
						t.Errorf("condition %d on %v, %v: %d, want %d", c, x, y, got, want)
					}
				}
			}
		}
	}
	e := emitterFor(t, "ppc64le", abi.StackPlatform)
	for _, ins := range []machine.Instruction{
		{Op: machine.OpFloatCompare, Bits: 16, A: 3, B: backend.VectorRegister(4), C: backend.VectorRegister(5)},
		{Op: machine.OpFloatCompare, Bits: 64, Imm: 14, A: 3, B: backend.VectorRegister(4), C: backend.VectorRegister(5)},
		{Op: machine.OpFloatCompare, Bits: 64, A: 3, B: 4, C: backend.VectorRegister(5)},
		{Op: machine.OpFloatCompare, Bits: 64, A: 3, B: backend.VectorRegister(4), C: 5},
		{Op: machine.OpFloatCompare, Bits: 64, A: backend.VectorRegister(3), B: backend.VectorRegister(4), C: backend.VectorRegister(5)},
	} {
		if _, err := e.Expand(ins, 0); err == nil || e.Size(ins) != 0 {
			t.Errorf("%+v accepted", ins)
		}
	}
}

// runFloatCompare interprets an expansion with fB=x and fC=y.
func runFloatCompare(t *testing.T, seq []ppc64.Instruction, x, y float64) uint64 {
	t.Helper()
	var cr uint32 = 0xffffffff // other fields must not leak into the result
	var r [32]uint64
	bit := func(n backend.Register) uint32 { return cr >> (31 - n) & 1 }
	for _, ins := range seq {
		switch ins.Op {
		case ppc64.Fcmpu:
			var field uint32
			switch {
			case x != x || y != y:
				field = 1
			case x < y:
				field = 8
			case x > y:
				field = 4
			default:
				field = 2
			}
			cr = cr&0x0fffffff | field<<28
		case ppc64.Cror, ppc64.Crnor:
			v := bit(ins.B) | bit(ins.C)
			if ins.Op == ppc64.Crnor {
				v ^= 1
			}
			cr = cr&^(1<<(31-ins.A)) | v<<(31-ins.A)
		case ppc64.Mfcr:
			r[ins.A] = 0xdead_beef_0000_0000 | uint64(cr)
		case ppc64.Rlwinm:
			if ins.Mask != 31 || ins.MaskEnd != 31 {
				t.Fatalf("unexpected mask %+v", ins)
			}
			// MASK(63,63) keeps only the rotated word's low bit.
			r[ins.A] = uint64(bitsRotateLeft32(uint32(r[ins.B]), ins.Shift) & 1)
		default:
			t.Fatalf("unexpected %+v", ins)
		}
	}
	return r[3]
}

func bitsRotateLeft32(x uint32, k uint8) uint32 { return x<<(k&31) | x>>((32-k)&31) }

func floatConditionHolds(c op.FloatCondition, x, y float64) uint64 {
	unordered := x != x || y != y
	var holds bool
	switch c &^ 1 {
	case op.FloatEqual:
		holds = x == y
	case op.FloatLess:
		holds = x < y
	case op.FloatLessEqual:
		holds = x <= y
	case op.FloatGreater:
		holds = x > y
	case op.FloatGreaterEqual:
		holds = x >= y
	case op.FloatOrdered:
		holds = !unordered
	case op.FloatOrderedNotEqual:
		holds = x < y || x > y
	}
	if c&1 != 0 {
		holds = !holds
	}
	if holds {
		return 1
	}
	return 0
}
