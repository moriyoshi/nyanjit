package arm64

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"golang.org/x/arch/arm64/arm64asm"
)

var divisionOperations = []op.Opcode{op.OpDiv, op.OpDivSigned, op.OpRem, op.OpRemSigned}

// assembleBoth runs ins through both assemblers and checks that the listing
// is the GNU disassembly of the bytes and that both match PhysicalSize.
func assembleBoth(t *testing.T, ins machine.Instruction) []string {
	t.Helper()
	b := NewByteAssembler(abi.StackNoUse, 0)
	text := NewTextAssembler(abi.StackNoUse, 0)
	if err := machine.Visit(ins, b); err != nil || b.Err() != nil {
		t.Fatalf("%+v: %v %v", ins, err, b.Err())
	}
	if err := machine.Visit(ins, text); err != nil || text.Err() != nil {
		t.Fatalf("%+v: text %v %v", ins, err, text.Err())
	}
	raw := b.Bytes()
	if size := PhysicalSize(ins, abi.StackNoUse, 0); size != len(raw) || size == 0 {
		t.Fatalf("%+v: size %d, encoded %d", ins, size, len(raw))
	}
	lines := strings.Split(strings.TrimSpace(text.Text()), "\n")
	if len(lines)*4 != len(raw) {
		t.Fatalf("%+v: %d listing lines for %d bytes", ins, len(lines), len(raw))
	}
	var out []string
	for n, line := range lines {
		decoded, err := arm64asm.Decode(raw[n*4:])
		if err != nil {
			t.Fatalf("%+v word %d: %v", ins, n, err)
		}
		gnu := strings.ReplaceAll(arm64asm.GNUSyntax(decoded), "#0x0", "#0")
		listed := strings.ToLower(strings.TrimSpace(line[strings.Index(line, " "):]))
		if gnu != listed {
			t.Fatalf("%+v word %d: listed %q, encoded %q", ins, n, listed, gnu)
		}
		out = append(out, gnu)
	}
	return out
}

func TestDivisionEncodingSizeAndListing(t *testing.T) {
	for _, w := range []uint8{8, 16, 32, 64} {
		for _, s := range []op.DivisionSemantics{op.DivisionZero, op.DivisionAllOnes, op.DivisionUnchecked} {
			for _, operation := range divisionOperations {
				ins := machine.Instruction{Op: machine.OpDivide, Arithmetic: operation, Bits: w, A: 0, B: 1, C: 2, Imm: uint64(s)}
				words := assembleBoth(t, ins)
				narrowSigned := w < 32 && (operation == op.OpDivSigned || operation == op.OpRemSigned)
				remainder := operation == op.OpRem || operation == op.OpRemSigned
				var want []backend.Register
				if narrowSigned {
					want = []backend.Register{16, 17}
				} else if remainder {
					want = []backend.Register{16}
				}
				if got := ImplicitWrites(ins); fmt.Sprint(got) != fmt.Sprint(want) {
					t.Fatalf("%+v: implicit writes %v, want %v", ins, got, want)
				}
				flags := !remainder && s == op.DivisionAllOnes
				if got := FlagFacts(ins).Modified != 0; got != flags || FlagEffect(ins) != flags || strings.HasPrefix(words[0], "cmp") != flags {
					t.Fatalf("%+v: flag model %v, want %v (%v)", ins, got, flags, words)
				}
			}
		}
	}
	got := assembleBoth(t, machine.Instruction{Op: machine.OpDivide, Arithmetic: op.OpRemSigned, Bits: 8, A: 3, B: 3, C: 4})
	want := []string{"sxtb w16, w3", "sxtb w17, w4", "sdiv w3, w16, w17", "msub w3, w3, w17, w16", "uxtb w3, w3"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("narrow signed remainder %q, want %q", got, want)
	}
	for _, bad := range []machine.Instruction{
		{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 64, Imm: 3},
		{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 64, Imm: 256},
		{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 24},
		{Op: machine.OpDivide, Arithmetic: op.OpMul, Bits: 64},
		{Op: machine.OpDivide, Arithmetic: op.OpDiv, Bits: 64, C: backend.VectorRegister(0)},
	} {
		b := NewByteAssembler(abi.StackNoUse, 0)
		b.Divide(bad)
		if PhysicalSize(bad, abi.StackNoUse, 0) != 0 || b.Err() == nil || len(b.Bytes()) != 0 {
			t.Fatalf("%+v accepted", bad)
		}
	}
}

// divisionMachine executes the GNU disassembly of a division plan on a
// register file, independently of the encoder's own field layout.
type divisionMachine struct {
	x    [32]uint64
	zero bool
}

func (m *divisionMachine) run(t *testing.T, lines []string) {
	t.Helper()
	for _, line := range lines {
		name, rest, _ := strings.Cut(line, " ")
		var regs []int
		wide := false
		var cond string
		for _, arg := range strings.Split(rest, ", ") {
			switch {
			case arg == "wzr" || arg == "xzr":
				regs = append(regs, 31)
				wide = wide || arg == "xzr"
			case arg[0] == 'w' || arg[0] == 'x':
				n, err := strconv.Atoi(arg[1:])
				if err != nil {
					t.Fatalf("%q: %v", line, err)
				}
				regs = append(regs, n)
				wide = wide || arg[0] == 'x'
			case arg == "#0":
			default:
				cond = arg
			}
		}
		get := func(i int) uint64 {
			if regs[i] == 31 {
				return 0
			}
			if wide {
				return m.x[regs[i]]
			}
			return uint64(uint32(m.x[regs[i]]))
		}
		var v uint64
		switch name {
		case "cmp":
			m.zero = get(0) == 0
			continue
		case "sxtb":
			v = uint64(uint32(int32(int8(get(1)))))
		case "sxth":
			v = uint64(uint32(int32(int16(get(1)))))
		case "uxtb":
			v = uint64(uint8(get(1)))
		case "uxth":
			v = uint64(uint16(get(1)))
		case "udiv", "sdiv":
			a, b := get(1), get(2)
			switch {
			case b == 0:
				v = 0
			case name == "udiv":
				v = a / b
			case wide && int64(b) == -1:
				v = -a
			case wide:
				v = uint64(int64(a) / int64(b))
			case int32(b) == -1:
				v = uint64(-uint32(a))
			default:
				v = uint64(uint32(int32(a) / int32(b)))
			}
		case "msub":
			v = get(3) - get(1)*get(2)
		case "csinv":
			if cond != "ne" {
				t.Fatalf("%q: condition %q", line, cond)
			}
			v = get(1)
			if m.zero {
				v = ^get(2)
			}
		default:
			t.Fatalf("unexpected %q", line)
		}
		if !wide {
			v = uint64(uint32(v))
		}
		m.x[regs[0]] = v
	}
}

// divisionReference is the machine contract for OpDivide.
func divisionReference(operation op.Opcode, s op.DivisionSemantics, w uint8, a, b uint64) uint64 {
	mask := ^uint64(0) >> (64 - w)
	signed := operation == op.OpDivSigned || operation == op.OpRemSigned
	remainder := operation == op.OpRem || operation == op.OpRemSigned
	if b == 0 {
		if remainder {
			return a
		}
		if s == op.DivisionAllOnes {
			return mask
		}
		return 0
	}
	if !signed {
		if remainder {
			return a % b
		}
		return a / b
	}
	sa, sb := int64(a<<(64-w))>>(64-w), int64(b<<(64-w))>>(64-w)
	if sb == -1 {
		if remainder {
			return 0
		}
		return uint64(-sa) & mask
	}
	if remainder {
		return uint64(sa%sb) & mask
	}
	return uint64(sa/sb) & mask
}

// Every aliasing of A with B and C must leave the contract's result in A,
// and the expansion must not write registers other than A and its
// documented temporaries.
func TestDivisionPlansUnderAliasing(t *testing.T) {
	values := func(w uint8) []uint64 {
		mask := ^uint64(0) >> (64 - w)
		min := uint64(1) << (w - 1)
		return []uint64{0, 1, 2, 3, 7, mask, mask - 1, min, min - 1, min + 1, 0x5a & mask, mask / 3}
	}
	for _, w := range []uint8{8, 16, 32, 64} {
		for _, s := range []op.DivisionSemantics{op.DivisionZero, op.DivisionAllOnes, op.DivisionUnchecked} {
			for _, operation := range divisionOperations {
				for _, regs := range [][3]backend.Register{{0, 1, 2}, {1, 1, 2}, {2, 1, 2}, {1, 1, 1}, {0, 1, 1}} {
					ins := machine.Instruction{Op: machine.OpDivide, Arithmetic: operation, Bits: w, A: regs[0], B: regs[1], C: regs[2], Imm: uint64(s)}
					lines := assembleBoth(t, ins)
					for _, a := range values(w) {
						for _, b := range values(w) {
							if regs[1] == regs[2] && a != b {
								continue
							}
							if s == op.DivisionUnchecked && (b == 0 || operation != op.OpDiv && operation != op.OpRem && a == uint64(1)<<(w-1) && b == ^uint64(0)>>(64-w)) {
								continue
							}
							var m divisionMachine
							for i := range m.x {
								m.x[i] = 0xdead0000 + uint64(i)
							}
							m.x[regs[1]], m.x[regs[2]] = a, b
							before := m.x
							m.run(t, lines)
							if want := divisionReference(operation, s, w, a, b); m.x[regs[0]] != want {
								t.Fatalf("%+v %#x, %#x: got %#x, want %#x\n%s", ins, a, b, m.x[regs[0]], want, strings.Join(lines, "\n"))
							}
							for i := range m.x {
								if i != int(regs[0]) && i != 16 && i != 17 && m.x[i] != before[i] {
									t.Fatalf("%+v wrote X%d", ins, i)
								}
							}
						}
					}
				}
			}
		}
	}
}

func TestFloatConvertEncodingSizeAndListing(t *testing.T) {
	v := backend.VectorRegister
	for _, bits := range []uint8{32, 64} {
		for _, in := range []uint64{32, 64} {
			for _, operation := range []op.Opcode{op.OpFFromSigned, op.OpFFromUnsigned} {
				ins := machine.Instruction{Op: machine.OpIntToFloat, Arithmetic: operation, Bits: bits, A: v(3), B: 5, Imm: in}
				got := assembleBoth(t, ins)
				fp, gp := map[uint8]string{32: "s", 64: "d"}[bits], map[uint64]string{32: "w", 64: "x"}[in]
				name := map[op.Opcode]string{op.OpFFromSigned: "scvtf", op.OpFFromUnsigned: "ucvtf"}[operation]
				if want := fmt.Sprintf("%s %s3, %s5", name, fp, gp); len(got) != 1 || got[0] != want {
					t.Fatalf("%+v: %q, want %q", ins, got, want)
				}
				if FlagFacts(ins).Modified != 0 || FlagEffect(ins) || ImplicitWrites(ins) != nil {
					t.Fatalf("%+v: conversion has side effects", ins)
				}
			}
			for _, operation := range []op.Opcode{op.OpFToSigned, op.OpFToUnsigned} {
				for _, s := range []op.ConversionSemantics{op.ConversionSaturate, op.ConversionNative} {
					ins := machine.Instruction{Op: machine.OpFloatToInt, Arithmetic: operation, Bits: bits, A: 6, B: v(29), Imm: uint64(s), Disp: in}
					got := assembleBoth(t, ins)
					gp, fp := map[uint8]string{32: "w", 64: "x"}[bits], map[uint64]string{32: "s", 64: "d"}[in]
					name := map[op.Opcode]string{op.OpFToSigned: "fcvtzs", op.OpFToUnsigned: "fcvtzu"}[operation]
					if want := fmt.Sprintf("%s %s6, %s29", name, gp, fp); len(got) != 1 || got[0] != want {
						t.Fatalf("%+v: %q, want %q", ins, got, want)
					}
					if FlagFacts(ins).Modified != 0 || FlagEffect(ins) || ImplicitWrites(ins) != nil {
						t.Fatalf("%+v: conversion has side effects", ins)
					}
				}
			}
		}
	}
	for _, bad := range []machine.Instruction{
		{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromSigned, Bits: 64, A: 0, B: 1, Imm: 64},
		{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromSigned, Bits: 64, A: v(0), B: v(1), Imm: 64},
		{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromSigned, Bits: 16, A: v(0), B: 1, Imm: 64},
		{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromSigned, Bits: 64, A: v(0), B: 1, Imm: 16},
		{Op: machine.OpIntToFloat, Arithmetic: op.OpFToSigned, Bits: 64, A: v(0), B: 1, Imm: 64},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 64, A: 0, B: 1, Disp: 64},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 64, A: v(0), B: v(1), Disp: 64},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 64, A: 0, B: v(1), Disp: 16},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 64, A: 0, B: v(1), Disp: 64, Imm: 2},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 64, A: 0, B: v(1), Disp: 64, Imm: 256},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFFromSigned, Bits: 64, A: 0, B: v(1), Disp: 64},
	} {
		b := NewByteAssembler(abi.StackNoUse, 0)
		b.FloatConvert(bad)
		if PhysicalSize(bad, abi.StackNoUse, 0) != 0 || b.Err() == nil || len(b.Bytes()) != 0 {
			t.Fatalf("%+v accepted", bad)
		}
	}
}

func TestDivisionAndConversionPlansDoNotAllocate(t *testing.T) {
	cases := []machine.Instruction{
		{Op: machine.OpDivide, Arithmetic: op.OpDivSigned, Bits: 8, A: 0, B: 1, C: 2, Imm: uint64(op.DivisionAllOnes)},
		{Op: machine.OpDivide, Arithmetic: op.OpRem, Bits: 64, A: 0, B: 1, C: 2},
		{Op: machine.OpIntToFloat, Arithmetic: op.OpFFromUnsigned, Bits: 64, A: backend.VectorRegister(0), B: 1, Imm: 64},
		{Op: machine.OpFloatToInt, Arithmetic: op.OpFToSigned, Bits: 32, A: 0, B: backend.VectorRegister(1), Disp: 64},
	}
	e := NewByteAssembler(abi.StackNoUse, 0)
	e.words = make([]uint32, 0, 32)
	for _, ins := range cases {
		if n := testing.AllocsPerRun(100, func() { PhysicalSize(ins, abi.StackPlatform, 9) }); n != 0 {
			t.Fatalf("%+v size query allocates %g", ins, n)
		}
		if n := testing.AllocsPerRun(100, func() {
			e.words = e.words[:0]
			if err := machine.Visit(ins, e); err != nil {
				panic(err)
			}
		}); n != 0 {
			t.Fatalf("%+v encoding allocates %g", ins, n)
		}
	}
}
