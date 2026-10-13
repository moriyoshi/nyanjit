package ppc64_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
)

// Words checked with golang.org/x/arch/ppc64/ppc64asm (GNU syntax).
func TestDivideAndConversionEncodings(t *testing.T) {
	for _, test := range []struct {
		ins  ppc64.Instruction
		word uint32
	}{
		{ppc64.Instruction{Op: ppc64.Divd, A: 3, B: 4, C: 5}, 0x7c642bd2},
		{ppc64.Instruction{Op: ppc64.Divdu, A: 3, B: 4, C: 5}, 0x7c642b92},
		{ppc64.Instruction{Op: ppc64.Divw, A: 3, B: 4, C: 5}, 0x7c642bd6},
		{ppc64.Instruction{Op: ppc64.Divwu, A: 3, B: 4, C: 5}, 0x7c642b96},
		{ppc64.Instruction{Op: ppc64.Modsd, A: 3, B: 4, C: 5}, 0x7c642e12},
		{ppc64.Instruction{Op: ppc64.Modud, A: 3, B: 4, C: 5}, 0x7c642a12},
		{ppc64.Instruction{Op: ppc64.Modsw, A: 3, B: 4, C: 5}, 0x7c642e16},
		{ppc64.Instruction{Op: ppc64.Moduw, A: 3, B: 4, C: 5}, 0x7c642a16},
		{ppc64.Instruction{Op: ppc64.Mtvsrwa, A: 3, B: 4}, 0x7c6401a6},
		{ppc64.Instruction{Op: ppc64.Mtvsrwz, A: 3, B: 4}, 0x7c6401e6},
		{ppc64.Instruction{Op: ppc64.Fcfid, A: 3, B: 3, Bits: 64}, 0xfc601e9c},
		{ppc64.Instruction{Op: ppc64.Fcfid, A: 3, B: 3, Bits: 32}, 0xec601e9c},
		{ppc64.Instruction{Op: ppc64.Fcfidu, A: 3, B: 3, Bits: 64}, 0xfc601f9c},
		{ppc64.Instruction{Op: ppc64.Fcfidu, A: 3, B: 3, Bits: 32}, 0xec601f9c},
		{ppc64.Instruction{Op: ppc64.Fctidz, A: 4, B: 4}, 0xfc80265e},
		{ppc64.Instruction{Op: ppc64.Fctiduz, A: 4, B: 4}, 0xfc80275e},
		{ppc64.Instruction{Op: ppc64.Fctiwz, A: 4, B: 4}, 0xfc80201e},
		{ppc64.Instruction{Op: ppc64.Fctiwuz, A: 4, B: 4}, 0xfc80211e},
		{ppc64.Instruction{Op: ppc64.Fcmpu, B: 4, C: 4}, 0xfc042000},
	} {
		got, err := ppc64.Encode(test.ins)
		if err != nil || got != test.word {
			t.Errorf("%+v = %#x,%v; want %#x", test.ins, got, err, test.word)
		}
	}
	if _, err := ppc64.Encode(ppc64.Instruction{Op: ppc64.Fcfid, A: 3, B: 3, Bits: 16}); !errors.Is(err, machine.ErrUnsupportedOperation) {
		t.Errorf("fcfid of 16 bits: %v", err)
	}
}

// machineState runs the integer instructions of a division expansion. An
// undefined POWER result is a fixed junk value, so only a guarded
// expansion can match the semantics.
type machineState struct {
	r      [32]uint64
	lt, eq bool
}

const junk = 0x5a5a_a5a5_5a5a_a5a5

func (s *machineState) run(t *testing.T, ins ppc64.Instruction) {
	a, b := s.r[ins.B], s.r[ins.C]
	low := func(v uint64) uint64 { return junk&^0xffffffff | v&0xffffffff }
	divided := func(signed, word, mod bool) uint64 {
		x, y := a, b
		if word {
			x, y = uint64(int64(int32(x))), uint64(int64(int32(y)))
			if !signed {
				x, y = a&0xffffffff, b&0xffffffff
			}
		}
		min := uint64(1) << 63
		if word {
			min = 0xffff_ffff_8000_0000 // math.MinInt32 sign-extended
		}
		if y == 0 || signed && x == min && y == ^uint64(0) {
			return junk
		}
		var v uint64
		switch {
		case signed && mod:
			v = uint64(int64(x) % int64(y))
		case signed:
			v = uint64(int64(x) / int64(y))
		case mod:
			v = x % y
		default:
			v = x / y
		}
		if word {
			return low(v)
		}
		return v
	}
	switch ins.Op {
	case ppc64.Cmpi:
		if ins.Bits != 64 {
			t.Fatalf("compare width %d", ins.Bits)
		}
		s.lt, s.eq = int64(a) < ins.Imm, int64(a) == ins.Imm
	case ppc64.Isel:
		if ins.BI != 2 {
			t.Fatalf("isel bit %d", ins.BI)
		}
		v := b
		if s.eq {
			v = a
			if ins.B == 0 {
				v = 0
			}
		}
		s.r[ins.A] = v
	case ppc64.Addi:
		if ins.B != 0 {
			t.Fatal("addi with a base")
		}
		s.r[ins.A] = uint64(ins.Imm)
	case ppc64.Divd, ppc64.Divdu, ppc64.Divw, ppc64.Divwu, ppc64.Modsd, ppc64.Modud, ppc64.Modsw, ppc64.Moduw:
		signed := ins.Op == ppc64.Divd || ins.Op == ppc64.Divw || ins.Op == ppc64.Modsd || ins.Op == ppc64.Modsw
		word := ins.Op == ppc64.Divw || ins.Op == ppc64.Divwu || ins.Op == ppc64.Modsw || ins.Op == ppc64.Moduw
		mod := ins.Op >= ppc64.Modsd && ins.Op <= ppc64.Moduw
		s.r[ins.A] = divided(signed, word, mod)
	case ppc64.Mulld:
		s.r[ins.A] = a * b
	case ppc64.Subf:
		s.r[ins.A] = a - b // B - C in nyanjit's operand order
	case ppc64.Neg:
		s.r[ins.A] = -a
	case ppc64.Extsb:
		s.r[ins.A] = uint64(int64(int8(a)))
	case ppc64.Extsh:
		s.r[ins.A] = uint64(int64(int16(a)))
	case ppc64.Extsw:
		s.r[ins.A] = uint64(int64(int32(a)))
	case ppc64.Rldicl:
		v := a<<ins.Shift | a>>(64-ins.Shift)%64
		s.r[ins.A] = v & (^uint64(0) >> ins.Mask)
	default:
		t.Fatalf("unexpected instruction %+v", ins)
	}
}

func referenceDivide(operation op.Opcode, semantics op.DivisionSemantics, width uint8, x, y uint64) uint64 {
	mask := ^uint64(0) >> (64 - width)
	sign := func(v uint64) int64 { return int64(v<<(64-width)) >> (64 - width) }
	switch {
	case y == 0 && (operation == op.OpRem || operation == op.OpRemSigned):
		return x
	case y == 0 && semantics == op.DivisionAllOnes:
		return mask
	case y == 0:
		return 0
	case operation == op.OpDiv:
		return x / y
	case operation == op.OpRem:
		return x % y
	case sign(y) == -1 && operation == op.OpDivSigned:
		return -x & mask
	case sign(y) == -1:
		return 0
	case operation == op.OpDivSigned:
		return uint64(sign(x)/sign(y)) & mask
	default:
		return uint64(sign(x)%sign(y)) & mask
	}
}

// Every width, semantics, CPU level and operand aliasing that CanReuse
// permits computes the documented result, including zero divisors and
// signed MIN / -1, without leaving the junk of an undefined division.
func TestDivideExpansionSemantics(t *testing.T) {
	profile, err := abi.NewPPC64Profile("ppc64le", abi.C)
	if err != nil {
		t.Fatal(err)
	}
	aliases := [][3]backend.Register{{3, 4, 5}, {3, 3, 4}, {3, 4, 3}, {3, 4, 4}, {3, 3, 3}, {0, 4, 5}, {3, 0, 5}, {3, 4, 0}}
	for _, cpu := range []abi.PPC64CPU{abi.PPC64Power8, abi.PPC64Power9} {
		emitter, err := ppc64.NewEmitterForCPU(profile, abi.StackPlatform, false, cpu)
		if err != nil {
			t.Fatal(err)
		}
		for _, width := range []uint8{8, 16, 32, 64} {
			mask := ^uint64(0) >> (64 - width)
			min := uint64(1) << (width - 1)
			values := []uint64{0, 1, 2, 3, 7, mask, mask - 1, min, min - 1, min + 1, 0x1234_5678_9abc_def1 & mask, 0xfedc_ba98_7654_3211 & mask}
			for _, operation := range []op.Opcode{op.OpDiv, op.OpDivSigned, op.OpRem, op.OpRemSigned} {
				for _, semantics := range []op.DivisionSemantics{op.DivisionZero, op.DivisionAllOnes, op.DivisionUnchecked} {
					for _, alias := range aliases {
						ins := machine.Instruction{Op: machine.OpDivide, Arithmetic: operation, Bits: width, Imm: uint64(semantics), A: alias[0], B: alias[1], C: alias[2]}
						sequence, err := emitter.Expand(ins, 0)
						if err != nil {
							t.Fatalf("%s %+v: %v", cpu, ins, err)
						}
						if emitter.Size(ins) != len(sequence)*4 {
							t.Fatalf("%s %+v: size %d for %d instructions", cpu, ins, emitter.Size(ins), len(sequence))
						}
						for _, native := range sequence {
							if _, err := ppc64.Encode(native); err != nil {
								t.Fatalf("%s %+v: %v", cpu, ins, err)
							}
						}
						for _, x := range values {
							for _, y := range values {
								if alias[1] == alias[2] && x != y {
									continue
								}
								if semantics == op.DivisionUnchecked && (y == 0 || (operation == op.OpDivSigned || operation == op.OpRemSigned) && x == min && y == mask) {
									continue
								}
								var s machineState
								s.r[alias[1]], s.r[alias[2]] = x, y
								for _, native := range sequence {
									s.run(t, native)
								}
								if got, want := s.r[alias[0]], referenceDivide(operation, semantics, width, x, y); got != want {
									t.Fatalf("%s %+v: %#x, %#x = %#x; want %#x\n%+v", cpu, ins, x, y, got, want, sequence)
								}
							}
						}
					}
				}
			}
		}
	}
}

func TestDivideEffectsAndRejections(t *testing.T) {
	emitter := emitterFor(t, "ppc64le", abi.StackPlatform)
	checked := machine.Instruction{Op: machine.OpDivide, Arithmetic: op.OpDivSigned, Bits: 64, A: 3, B: 4, C: 5}
	if !slices.Equal(emitter.ImplicitWrites(checked), []backend.Register{11, 12, 3}) {
		t.Errorf("checked division writes %v", emitter.ImplicitWrites(checked))
	}
	if facts := emitter.FlagFacts(checked); facts.Modified != machine.FlagSign|machine.FlagZero {
		t.Errorf("guard compare flags: %+v", facts)
	}
	if !slices.Equal(emitter.SpecialWrites(checked), []machine.Resource{{Class: machine.ResourceConditionField}}) {
		t.Errorf("checked division resources: %v", emitter.SpecialWrites(checked))
	}
	unchecked := checked
	unchecked.Imm = uint64(op.DivisionUnchecked)
	if emitter.FlagEffect(unchecked) || len(emitter.ImplicitWrites(unchecked)) != 1 {
		t.Errorf("unchecked division needs no guard: %v", emitter.ImplicitWrites(unchecked))
	}
	for _, ins := range []machine.Instruction{
		{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 12, A: 3, B: 4, C: 5},
		{Op: machine.OpDivide, Arithmetic: op.OpMul, Bits: 64, A: 3, B: 4, C: 5},
		{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 64, Imm: 3, A: 3, B: 4, C: 5},
		{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 64, A: 3, B: backend.VectorRegister(4), C: 5},
	} {
		if _, err := emitter.Expand(ins, 0); !errors.Is(err, machine.ErrUnsupportedOperation) || emitter.Size(ins) != 0 {
			t.Errorf("accepted %+v: %v", ins, err)
		}
	}
	reserved := checked
	reserved.C = 12
	if _, err := emitter.Expand(reserved, 0); !errors.Is(err, machine.ErrInvalidInstruction) {
		t.Errorf("expansion temporary as an operand: %v", err)
	}
}

func TestConversionExpansions(t *testing.T) {
	emitter := emitterFor(t, "ppc64le", abi.StackPlatform)
	fpscr := machine.Resource{Class: machine.ResourceFloatingStatus}
	for _, test := range []struct {
		arithmetic op.Opcode
		bits       uint8
		source     uint64
		want       []ppc64.Opcode
	}{
		{op.OpFFromSigned, 64, 64, []ppc64.Opcode{ppc64.Mtvsrd, ppc64.Fcfid}},
		{op.OpFFromSigned, 32, 32, []ppc64.Opcode{ppc64.Mtvsrwa, ppc64.Fcfid}},
		{op.OpFFromUnsigned, 32, 64, []ppc64.Opcode{ppc64.Mtvsrd, ppc64.Fcfidu}},
		{op.OpFFromUnsigned, 64, 32, []ppc64.Opcode{ppc64.Mtvsrwz, ppc64.Fcfid}},
	} {
		ins := machine.Instruction{Op: machine.OpIntToFloat, Arithmetic: test.arithmetic, Bits: test.bits, Imm: test.source, A: backend.VectorRegister(3), B: 4}
		sequence, err := emitter.Expand(ins, 0)
		if err != nil {
			t.Fatal(err)
		}
		var ops []ppc64.Opcode
		for _, native := range sequence {
			ops = append(ops, native.Op)
			if native.Op == ppc64.Fcfid || native.Op == ppc64.Fcfidu {
				if native.A != 3 || native.B != 3 || native.Bits != test.bits {
					t.Errorf("%+v: conversion %+v", ins, native)
				}
			} else if native.A != 3 || native.B != 4 {
				t.Errorf("%+v: move %+v", ins, native)
			}
		}
		if !slices.Equal(ops, test.want) || emitter.ImplicitWrites(ins) != nil || !slices.Equal(emitter.SpecialWrites(ins), []machine.Resource{fpscr}) {
			t.Errorf("%+v: %v writes %v %v", ins, ops, emitter.ImplicitWrites(ins), emitter.SpecialWrites(ins))
		}
	}
	for _, signed := range []bool{false, true} {
		for _, bits := range []uint8{32, 64} {
			for _, semantics := range []op.ConversionSemantics{op.ConversionSaturate, op.ConversionNative} {
				arithmetic := op.OpFToUnsigned
				if signed {
					arithmetic = op.OpFToSigned
				}
				ins := machine.Instruction{Op: machine.OpFloatToInt, Arithmetic: arithmetic, Bits: bits, Imm: uint64(semantics), Disp: 32, A: 3, B: backend.VectorRegister(4)}
				sequence, err := emitter.Expand(ins, 0)
				if err != nil {
					t.Fatal(err)
				}
				// The result goes through f0, so B is never written. The
				// NaN compare reads B before f0 changes, which lets B be f0.
				convert := map[[2]bool]ppc64.Opcode{{true, true}: ppc64.Fctidz, {false, true}: ppc64.Fctiduz, {true, false}: ppc64.Fctiwz, {false, false}: ppc64.Fctiwuz}[[2]bool{signed, bits == 64}]
				move := ppc64.Mfvsrd
				if bits == 32 {
					move = ppc64.Mfvsrwz
				}
				want := []ppc64.Instruction{{Op: convert, A: 0, B: 4}, {Op: move, A: 3, B: 0, PC: 4}}
				resources := []machine.Resource{fpscr}
				if signed && semantics == op.ConversionSaturate {
					want = []ppc64.Instruction{{Op: ppc64.Fcmpu, B: 4, C: 4}, {Op: convert, A: 0, B: 4, PC: 4}, {Op: move, A: 3, B: 0, PC: 8}, {Op: ppc64.Isel, A: 3, C: 3, BI: 3, PC: 12}}
					resources = []machine.Resource{{Class: machine.ResourceConditionField}, fpscr}
				}
				if !slices.Equal(sequence, want) {
					t.Fatalf("%+v: %+v; want %+v", ins, sequence, want)
				}
				if !slices.Equal(emitter.ImplicitWrites(ins), []backend.Register{3}) || !slices.Equal(emitter.SpecialWrites(ins), resources) {
					t.Errorf("%+v: writes %v %v", ins, emitter.ImplicitWrites(ins), emitter.SpecialWrites(ins))
				}
			}
		}
	}
	for _, ins := range []machine.Instruction{
		{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromSigned, Bits: 64, Imm: 64, A: 3, B: 4},
		{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromSigned, Bits: 64, Imm: 16, A: backend.VectorRegister(3), B: 4},
		{Op: machine.OpIntToFloat, Arithmetic: op.OpFToSigned, Bits: 64, Imm: 64, A: backend.VectorRegister(3), B: 4},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 64, Disp: 64, A: 3, B: 4},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 16, Disp: 64, A: 3, B: backend.VectorRegister(4)},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 64, Disp: 64, Imm: 2, A: 3, B: backend.VectorRegister(4)},
	} {
		if _, err := emitter.Expand(ins, 0); !errors.Is(err, machine.ErrUnsupportedOperation) {
			t.Errorf("accepted %+v: %v", ins, err)
		}
	}
}
