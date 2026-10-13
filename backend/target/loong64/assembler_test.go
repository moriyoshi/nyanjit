package loong64_test

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
)

func TestEncodings(t *testing.T) {
	// Independently checked against Go 1.26/1.27 cmd/internal/obj/loong64.
	tests := []struct {
		i     machine.Instruction
		words []uint32
	}{
		{machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, A: 4, B: 5, C: 6}, []uint32{0x001098a4}},
		{machine.Instruction{Op: machine.OpScaled, A: 4, B: 5, C: 6, Imm: 8}, []uint32{0x002d14c4}},
		{machine.Instruction{Op: machine.OpNarrow, A: 4, B: 5, Imm: 16}, []uint32{0x00cf00a4}},
		{machine.Instruction{Op: machine.OpMulHigh, Bits: 64, A: 4, B: 5, C: 6}, []uint32{0x001e98a4}},
		{machine.Instruction{Op: machine.OpReturn}, []uint32{0x4c000020}},
		{machine.Instruction{Op: machine.OpJump, Imm: 16}, []uint32{0x50001000}},
		{machine.Instruction{Op: machine.OpBranchZero, A: 4, Imm: 16}, []uint32{0x40001080}},
		{machine.Instruction{Op: machine.OpLoadAddress, A: 4, B: 5, C: 6, Imm: 1}, []uint32{0x380c18a4}},
	}
	for _, tc := range tests {
		b := loong64.NewByteAssembler(abi.StackNoUse, 0)
		txt := loong64.NewTextAssembler(abi.StackNoUse, 0)
		for _, a := range []machine.Assembler{b, txt} {
			if err := machine.Visit(tc.i, a); err != nil {
				t.Fatal(err)
			}
		}
		raw := b.Bytes()
		if len(raw) != 4*len(tc.words) || len(raw) != loong64.PhysicalSize(tc.i, abi.StackNoUse, 0) || txt.AssembledSize() != len(raw) {
			t.Fatalf("size %+v", tc.i)
		}
		for n, w := range tc.words {
			if got := binary.LittleEndian.Uint32(raw[n*4:]); got != w {
				t.Errorf("op%d word%d=%08x want%08x: %s", tc.i.Op, n, got, w, txt.Text())
			}
		}
	}
}
func TestConstantPlans(t *testing.T) {
	for _, v := range []uint64{0, 1, 2047, 2048, 4095, 4096, ^uint64(0), 0x80000000, 0x100000000, 0x8000000000000000, 0x123456789abcdef0, 0xfffff00000000001} {
		a := loong64.NewByteAssembler(abi.StackNoUse, 0)
		a.Constant(4, v)
		raw := a.Bytes()
		if len(raw) > 16 {
			t.Fatal("constant exceeds four instructions")
		}
		var got uint64
		for p := 0; p < len(raw); p += 4 {
			w := binary.LittleEndian.Uint32(raw[p:])
			imm := uint64(w >> 10 & 4095)
			switch {
			case w>>25 == 0xa:
				got = uint64(int64(int32(w>>5&0xfffff) << 12))
			case w>>25 == 0xb:
				got = (got & 0xffffffff) | uint64(int64(int32((w>>5&0xfffff)<<12))>>12)<<32
			case w>>22 == 0xb:
				got = uint64(int64(int32(imm<<20)) >> 20)
			case w>>22 == 0xe:
				got |= imm
			case w>>22 == 0xc:
				got = got&((1<<52)-1) | imm<<52
			default:
				t.Fatalf("unknown constant instruction %x", w)
			}
		}
		if got != v {
			t.Fatalf("constant%#x got%#x:\n%s", v, got, a.Text())
		}
	}
}
func TestBranchReach(t *testing.T) {
	p := loong64.NewPolicy(abi.C, abi.StackNoUse)
	for _, tc := range []struct {
		op   machine.Opcode
		bits int
	}{{machine.OpJumpIf, 18}, {machine.OpBranchZero, 23}, {machine.OpJump, 28}} {
		for _, d := range []int{-(1 << (tc.bits - 1)) - 4, -(1 << (tc.bits - 1)), (1 << (tc.bits - 1)) - 4, 1 << (tc.bits - 1)} {
			want := d >= -(1<<(tc.bits-1)) && d < 1<<(tc.bits-1)
			if p.BranchFits(machine.Instruction{Op: tc.op}, 0, d, 4) != want {
				t.Fatalf("op%d displacement%d", tc.op, d)
			}
		}
	}
}

func TestSymbolRelocation(t *testing.T) {
	a := loong64.NewByteAssembler(abi.StackNoUse, 0)
	a.Symbol(4, "x")
	original := a.Bytes()
	linked, err := machine.LinkRelocations(original, a.Relocations(), map[string]uint64{"x": 0x123456789abcdef0})
	if err != nil {
		t.Fatal(err)
	}
	want := []uint32{0x153579a4, 0x03bbc084, 0x168acf04, 0x03048c84}
	for n, w := range want {
		if got := binary.LittleEndian.Uint32(linked[n*4:]); got != w {
			t.Fatalf("word%d=%#x want%#x", n, got, w)
		}
	}
	if binary.LittleEndian.Uint32(original) != 0x14000004 {
		t.Fatal("link changed template")
	}
	for n := 0; n < 4; n++ {
		bad := append([]byte(nil), original...)
		bad[n*4] ^= 1
		if _, err := machine.LinkRelocations(bad, a.Relocations(), map[string]uint64{"x": 1}); err == nil {
			t.Fatalf("mismatched rd at word%d accepted", n)
		}
	}
}

func TestExpansionReservedWrites(t *testing.T) {
	for _, tc := range []struct {
		i    machine.Instruction
		want []backend.Register
	}{
		{machine.Instruction{Op: machine.OpConst, A: 4, Imm: 0x123456789abcdef0}, nil},
		{machine.Instruction{Op: machine.OpScaled, A: 4, B: 5, C: 6, Imm: 8}, nil},
		{machine.Instruction{Op: machine.OpLoadAddress, A: 4, B: 5, C: 6, Imm: 1}, nil},
		{machine.Instruction{Op: machine.OpSelect, A: 4, B: 5, C: 6, D: 7}, []backend.Register{19, 20}},
		{machine.Instruction{Op: machine.OpStoreOffset, A: 4, B: 5, Disp: 8192}, []backend.Register{19}},
	} {
		got := loong64.ImplicitWrites(tc.i, abi.StackNoUse, 0)
		if len(got) != len(tc.want) {
			t.Fatalf("op%d writes%v want%v", tc.i.Op, got, tc.want)
		}
		for n := range got {
			if got[n] != tc.want[n] {
				t.Fatalf("op%d writes%v want%v", tc.i.Op, got, tc.want)
			}
		}
	}
}

func TestManagedFrameHomesAndFPBoundarySaves(t *testing.T) {
	for _, tc := range []struct {
		i    machine.Instruction
		word uint32
	}{
		{machine.Instruction{Op: machine.OpSpill, A: 4, Slot: 0}, 0x29c24064},
		{machine.Instruction{Op: machine.OpSaveCFloat, A: 24, Slot: 0}, 0x2bc24078},
		{machine.Instruction{Op: machine.OpRestoreCFloat, A: 24, Slot: 0}, 0x2b824078},
	} {
		b := loong64.NewByteAssembler(abi.StackPlatform, 17)
		txt := loong64.NewTextAssembler(abi.StackPlatform, 17)
		for _, a := range []machine.Assembler{b, txt} {
			if err := machine.Visit(tc.i, a); err != nil {
				t.Fatal(err)
			}
		}
		if b.AssembledSize() != 4 || txt.AssembledSize() != 4 || loong64.PhysicalSize(tc.i, abi.StackPlatform, 17) != 4 {
			t.Fatal("frame plan sizes differ")
		}
		if w := binary.LittleEndian.Uint32(b.Bytes()); w != tc.word {
			t.Fatalf("frame op%d got%#x want%#x: %s", tc.i.Op, w, tc.word, txt.Text())
		}
		if len(loong64.ImplicitWrites(tc.i, abi.StackPlatform, 17)) != 0 {
			t.Fatal("FP frame save reported a GP clobber")
		}
	}
}

// A zero scale indexes nothing: the address is B + Disp on both paths.
func TestZeroScaleIndexedMemory(t *testing.T) {
	for _, i := range []machine.Instruction{
		{Op: machine.OpLoadAddress, A: 4, B: 5, C: 6},
		{Op: machine.OpStoreAddress, A: 5, B: 6, C: 4},
	} {
		a := loong64.NewTextAssembler(abi.StackNoUse, 0)
		if err := machine.Visit(i, a); err != nil {
			t.Fatal(err)
		}
		if text := a.Text(); !strings.Contains(text, "addi.d r19, r0, 0\n") || !strings.Contains(text, "mul.d r19, r6, r19\n") {
			t.Errorf("op%d with Imm=0 does not clear the index:\n%s", i.Op, text)
		}
	}
}
