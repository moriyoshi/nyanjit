package loong64_test

import (
	"encoding/binary"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
)

func TestFloatEncodings(t *testing.T) {
	f := backend.VectorRegister
	// Opcode fields independently match Go's cmd/internal/obj/loong64 table.
	for _, tc := range []struct {
		i     machine.Instruction
		words []uint32
		text  string
	}{
		{machine.Instruction{Op: machine.OpFloatBinary, Arithmetic: op.OpFAdd, Bits: 32, A: f(8), B: f(9), C: f(10)}, []uint32{0x0100a928}, "fadd.s f8, f9, f10"},
		{machine.Instruction{Op: machine.OpFloatBinary, Arithmetic: op.OpFSub, Bits: 64, A: f(10), B: f(9), C: f(10)}, []uint32{0x0103292a}, "fsub.d f10, f9, f10"},
		{machine.Instruction{Op: machine.OpFloatBinary, Arithmetic: op.OpFDiv, Bits: 64, A: f(10), B: f(9), C: f(10)}, []uint32{0x0107292a}, "fdiv.d f10, f9, f10"},
		{machine.Instruction{Op: machine.OpFloatFMA, Bits: 64, A: f(8), B: f(9), C: f(10), D: f(11)}, []uint32{0x0825a928}, "fmadd.d f8, f9, f10, f11"},
		{machine.Instruction{Op: machine.OpFloatUnary, Arithmetic: op.OpFSqrt, Bits: 32, A: f(8), B: f(9)}, []uint32{0x01144528}, "fsqrt.s f8, f9"},
		{machine.Instruction{Op: machine.OpFloatUnary, Arithmetic: op.OpFConvert, Bits: 32, A: f(8), B: f(9)}, []uint32{0x01191928}, "fcvt.s.d f8, f9"},
		{machine.Instruction{Op: machine.OpFloatUnary, Arithmetic: op.OpFConvert, Bits: 64, A: f(8), B: f(9)}, []uint32{0x01192528}, "fcvt.d.s f8, f9"},
		{machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: f(8), B: 4}, []uint32{0x0114a888}, "movgr2fr.d f8, r4"},
		{machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: 4, B: f(8)}, []uint32{0x0114b904}, "movfr2gr.d r4, f8"},
		{machine.Instruction{Op: machine.OpFloatSpill, Bits: 32, A: f(8), Slot: 0}, []uint32{0x2b4002e8}, "fst.s f8, 0(r23)"},
		{machine.Instruction{Op: machine.OpFloatReload, Bits: 64, A: f(8), Slot: 0}, []uint32{0x2b8002e8}, "fld.d f8, 0(r23)"},
	} {
		b := loong64.NewByteAssembler(abi.StackNoUse, 0)
		txt := loong64.NewTextAssembler(abi.StackNoUse, 0)
		for _, a := range []machine.Assembler{b, txt} {
			if err := machine.Visit(tc.i, a); err != nil {
				t.Fatal(err)
			}
		}
		var words []uint32
		for n := 0; n < len(b.Bytes()); n += 4 {
			words = append(words, binary.LittleEndian.Uint32(b.Bytes()[n:]))
		}
		if !reflect.DeepEqual(words, tc.words) || !strings.Contains(txt.Text(), tc.text) || len(b.Bytes()) != loong64.PhysicalSize(tc.i, abi.StackNoUse, 0) || len(b.Bytes()) != txt.AssembledSize() {
			t.Errorf("%s got %x want %x", txt.Text(), words, tc.words)
		}
	}
}
func TestFloatImplicitGeneralWrites(t *testing.T) {
	f := backend.VectorRegister
	for _, i := range []machine.Instruction{
		{Op: machine.OpFloatBinary, Arithmetic: op.OpFAdd, Bits: 64, A: f(19), B: f(20), C: f(12)},
		{Op: machine.OpFloatFMA, Bits: 64, A: f(20), B: f(19), C: f(12), D: f(13)},
		{Op: machine.OpLiteralData, Data: []byte{19, 0, 0, 0}},
	} {
		if got := loong64.ImplicitWrites(i, abi.StackNoUse, 0); len(got) != 0 {
			t.Fatalf("false GP writes %v for %+v", got, i)
		}
	}
	for _, i := range []machine.Instruction{
		{Op: machine.OpFloatMove, Bits: 32, A: 19, B: f(8)},
		{Op: machine.OpLiteralLoad, Bits: 64, A: f(8), Disp: 4096},
		{Op: machine.OpFloatReload, Bits: 32, A: f(8), Slot: 1024},
	} {
		if got := loong64.ImplicitWrites(i, abi.StackNoUse, 0); !reflect.DeepEqual(got, []backend.Register{19}) {
			t.Fatalf("missing GP scratch %v for %+v", got, i)
		}
	}
}
func TestLiteralReachAndPlan(t *testing.T) {
	if strconv.IntSize == 32 {
		t.Skip("full signed-32-bit reach requires a PC above 2 GiB")
	}
	pc := int64(1) << 32
	for _, bits := range []uint8{32, 64} {
		for _, delta := range []int64{-(1 << 31), -2049, -2048, -1, 0, 2047, 2048, (1 << 31) - 2049} {
			e := loong64.NewByteAssembler(abi.StackNoUse, 0)
			e.SetPC(int(pc))
			i := machine.Instruction{Op: machine.OpLiteralLoad, Bits: bits, A: backend.VectorRegister(19), Disp: uint64(pc + delta)}
			if err := e.Literal(i); err != nil {
				t.Fatalf("delta%d: %v", delta, err)
			}
			b := e.Bytes()
			hi := int64(int32(binary.LittleEndian.Uint32(b[:4])<<7) >> 12)
			lo := int64(int32(binary.LittleEndian.Uint32(b[4:])<<10) >> 20)
			if len(b) != 8 || hi*4096+lo != delta {
				t.Fatalf("delta%d encoded %x hi%d lo%d", delta, b, hi, lo)
			}
		}
	}
	for _, delta := range []int64{-(1 << 31) - 1, (1 << 31) - 2048} {
		e := loong64.NewByteAssembler(abi.StackNoUse, 0)
		e.SetPC(int(pc))
		if err := e.Literal(machine.Instruction{Op: machine.OpLiteralLoad, Bits: 64, A: backend.VectorRegister(8), Disp: uint64(pc + delta)}); !errors.Is(err, backend.ErrBackendLimit) {
			t.Fatalf("delta%d err%v", delta, err)
		}
	}
}

func TestFloatingStatusEffects(t *testing.T) {
	p := loong64.NewPolicy(abi.C, abi.StackNoUse)
	for _, op := range []machine.Opcode{machine.OpFloatUnary, machine.OpFloatBinary, machine.OpFloatFMA, machine.OpCallC, machine.OpCallGo} {
		i := machine.Instruction{Op: op}
		writes := p.SpecialWrites(i)
		if len(writes) != 1 || p.ResourceName(writes[0]) != "FCSR0" || loong64.FlagEffect(i) {
			t.Fatalf("op%d resources%v", op, writes)
		}
	}
	for _, op := range []machine.Opcode{machine.OpFloatMove, machine.OpFloatBits, machine.OpFloatReload, machine.OpLiteralLoad, machine.OpLiteralData, machine.OpFloatSpill} {
		if writes := p.SpecialWrites(machine.Instruction{Op: op}); len(writes) != 0 {
			t.Fatalf("op%d spuriously changes FP status: %v", op, writes)
		}
	}
}
