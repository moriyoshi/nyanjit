package loong64_test

import (
	"bytes"
	"encoding/binary"
	"slices"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
	"golang.org/x/arch/loong64/loong64asm"
)

func checkLSXEncoding(t *testing.T, i machine.Instruction, want ...uint32) {
	t.Helper()
	b, text := loong64.NewByteAssembler(abi.StackNoUse, 0), loong64.NewTextAssembler(abi.StackNoUse, 0)
	for _, a := range []machine.Assembler{b, text} {
		if err := machine.Visit(i, a); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.Bytes()) != 4*len(want) || text.AssembledSize() != len(b.Bytes()) || loong64.PhysicalSize(i, abi.StackNoUse, 0) != len(b.Bytes()) {
		t.Fatalf("inconsistent vector sizes: %s", text.Text())
	}
	for k, w := range want {
		if got := binary.LittleEndian.Uint32(b.Bytes()[4*k:]); got != w {
			t.Fatalf("word%d=%08x want%08x: %s", k, got, w, text.Text())
		}
	}
	if !bytes.Equal(b.Bytes(), text.Bytes()) {
		t.Fatal("byte/text plans differ")
	}
}

func TestLSXEncodings(t *testing.T) {
	f := backend.VectorRegister
	// Fixed bases independently agree with Go 1.26/1.27's instOp.go and
	// cmd/asm/internal/asm/testdata/loong64enc{1,2}.s. Register and lane
	// fields exercise both ends of the register file and every lane width.
	for _, tc := range []struct {
		op    op.Opcode
		words []uint32
	}{
		{op.OpAdd, []uint32{0x700a0000, 0x700a8000, 0x700b0000, 0x700b8000}},
		{op.OpSub, []uint32{0x700c0000, 0x700c8000, 0x700d0000, 0x700d8000}},
		{op.OpMul, []uint32{0x70840000, 0x70848000, 0x70850000, 0x70858000}},
		{op.OpAnd, []uint32{0x71260000, 0x71260000, 0x71260000, 0x71260000}},
		{op.OpOr, []uint32{0x71268000, 0x71268000, 0x71268000, 0x71268000}},
		{op.OpXor, []uint32{0x71270000, 0x71270000, 0x71270000, 0x71270000}},
	} {
		for k, base := range tc.words {
			for _, regs := range [][3]uint8{{0, 31, 15}, {31, 0, 31}, {8, 8, 9}, {9, 8, 9}} {
				i := machine.Instruction{Op: machine.OpVector, Arithmetic: tc.op, Bits: uint8(8 << k), A: f(regs[0]), B: f(regs[1]), C: f(regs[2])}
				checkLSXEncoding(t, i, base|uint32(regs[2])<<10|uint32(regs[1])<<5|uint32(regs[0]))
			}
		}
	}
	for k, bits := range []uint8{8, 16, 32, 64} {
		for _, r := range []uint8{0, 31} {
			i := machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVSplat, Bits: bits, A: f(r), B: 31}
			checkLSXEncoding(t, i, []uint32{0x729f0000, 0x729f0400, 0x729f0800, 0x729f0c00}[k]|31<<5|uint32(r))
			i.Arithmetic, i.A, i.B, i.Imm = op.OpVExtract, 31, f(r), uint64(128/bits-1)
			checkLSXEncoding(t, i, []uint32{0x72f38000, 0x72f3c000, 0x72f3e000, 0x72f3f000}[k]|uint32(i.Imm)<<10|uint32(r)<<5|31)
			for _, op := range []op.Opcode{op.OpVInsert, op.OpVInsertZero} {
				i.Arithmetic, i.A, i.B, i.C = op, f(r), f(r), 31
				checkLSXEncoding(t, i, []uint32{0x72eb8000, 0x72ebc000, 0x72ebe000, 0x72ebf000}[k]|uint32(i.Imm)<<10|31<<5|uint32(r))
			}
			i.Arithmetic, i.Imm = op.OpVZero, 0
			checkLSXEncoding(t, i, 0x71270000|uint32(r)<<10|uint32(r)<<5|uint32(r))
		}
	}
	checkLSXEncoding(t, machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVCompare, Bits: 8, A: f(31), B: f(0), C: f(31), Imm: uint64(op.BranchEqual)}, 0x70007c1f)
	// VMSKLTZ is absent from Go's assembler. This base also agrees with
	// QEMU v10.2.3 target/loongarch/insns.decode (vmskltz_b).
	checkLSXEncoding(t, machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVMaskBits, Bits: 64, A: 31, B: f(31)}, 0x729c43e0, 0x72f3e01f)
	checkLSXEncoding(t, machine.Instruction{Op: machine.OpVectorMove, A: f(31), B: f(0)}, 0x7126801f)
}

func TestLSXImmediateShiftEncodings(t *testing.T) {
	f := backend.VectorRegister
	for _, tc := range []struct {
		op   op.Opcode
		base [4]uint32
	}{
		{op.OpShiftLeft, [4]uint32{0x732c2000, 0x732c4000, 0x732c8000, 0x732d0000}},
		{op.OpShiftRight, [4]uint32{0x73302000, 0x73304000, 0x73308000, 0x73310000}},
		{op.OpShiftRightArithmetic, [4]uint32{0x73342000, 0x73344000, 0x73348000, 0x73350000}},
		{op.OpRotateRight, [4]uint32{0x72a02000, 0x72a04000, 0x72a08000, 0x72a10000}},
		{op.OpRotateLeft, [4]uint32{0x72a02000, 0x72a04000, 0x72a08000, 0x72a10000}},
	} {
		for k, bits := range []uint8{8, 16, 32, 64} {
			for _, count := range []uint64{0, 1, uint64(bits - 1), uint64(bits), uint64(bits + 1), 255} {
				i := machine.Instruction{Op: machine.OpVector, Arithmetic: tc.op, Bits: bits, A: f(31), B: f(0), Imm: count}
				n := count
				if tc.op == op.OpRotateRight || tc.op == op.OpRotateLeft {
					n %= uint64(bits)
					if tc.op == op.OpRotateLeft {
						n = (uint64(bits) - n) % uint64(bits)
					}
				}
				want := tc.base[k] | uint32(n)<<10 | 31
				if count >= uint64(bits) && tc.op != op.OpRotateLeft && tc.op != op.OpRotateRight {
					if tc.op == op.OpShiftRightArithmetic {
						want = tc.base[k] | uint32(bits-1)<<10 | 31
					} else {
						want = 0x71277fff
					}
				} else if n == 0 {
					want = 0x7126801f
				}
				checkLSXEncoding(t, i, want)
			}
		}
	}
}

func TestLSXMemoryEncodings(t *testing.T) {
	f := backend.VectorRegister
	for _, offset := range []int64{-2048, -1, 0, 1, 2047} {
		for _, r := range []uint8{0, 31} {
			checkLSXEncoding(t, machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVLoad, Bits: 8, A: f(r), B: 31, Disp: uint64(offset)}, 0x2c000000|(uint32(offset)&4095)<<10|31<<5|uint32(r))
			checkLSXEncoding(t, machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVStore, Bits: 8, A: 31, B: f(r), Disp: uint64(offset)}, 0x2c400000|(uint32(offset)&4095)<<10|31<<5|uint32(r))
		}
	}
	for _, index := range []backend.Register{0, 31} {
		checkLSXEncoding(t, machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVLoad, Bits: 32, A: f(31), B: 0, C: index, Indexed: true}, 0x3840001f|uint32(index)<<10)
		checkLSXEncoding(t, machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVStore, Bits: 32, A: 0, B: f(31), C: index, Indexed: true}, 0x3844001f|uint32(index)<<10)
	}
	checkLSXEncoding(t, machine.Instruction{Op: machine.OpVectorSpill, A: f(31), Slot: 255}, 0x2c5fe2ff)
	checkLSXEncoding(t, machine.Instruction{Op: machine.OpVectorReload, A: f(0), Slot: 255}, 0x2c1fe2e0)
	checkLSXEncoding(t, machine.Instruction{Op: machine.OpLiteralLoad, A: f(31), Bits: 128, Disp: 2047}, 0x1c000013, 0x2c1ffe7f)
}

func TestLSXVariableShiftPlansAndWrites(t *testing.T) {
	f := backend.VectorRegister
	for _, bits := range []uint8{8, 16, 32, 64} {
		for _, operation := range []op.Opcode{op.OpShiftLeftBy, op.OpShiftRightBy, op.OpShiftRightArithmeticBy, op.OpRotateLeftBy, op.OpRotateRightBy} {
			for _, scalar := range []bool{false, true} {
				for _, dst := range []uint8{0, 8, 9, 10, 31} {
					i := machine.Instruction{Op: machine.OpVector, Arithmetic: operation, Bits: bits, A: f(dst), B: f(9), C: f(10)}
					if scalar {
						i.C = 4
					}
					b, x := loong64.NewByteAssembler(abi.StackNoUse, 0), loong64.NewTextAssembler(abi.StackNoUse, 0)
					for _, a := range []machine.Assembler{b, x} {
						if err := machine.Visit(i, a); err != nil {
							t.Fatal(err)
						}
					}
					if !bytes.Equal(b.Bytes(), x.Bytes()) || len(b.Bytes()) != loong64.PhysicalSize(i, abi.StackNoUse, 0) {
						t.Fatal("variable shift plan diverged")
					}
					if !slices.Contains(loong64.VectorWrites(i), f(dst)) {
						t.Fatal("destination missing from writes")
					}
					if scalar && !slices.Contains(loong64.VectorWrites(i), f(0)) {
						t.Fatal("count splat missing from writes")
					}
					if operation == op.OpShiftRightArithmeticBy && !strings.Contains(x.Text(), "vandn.v vr2, vr1, vr2") {
						t.Fatal("incorrect AND-NOT operand order")
					}
				}
			}
		}
	}
}

func TestLSXReferenceDecoderCoverage(t *testing.T) {
	// x/arch v0.32 supports scalar LA64, but has no LSX opcodes. Pin that
	// limitation explicitly instead of pretending unknown words were decoded.
	raw := make([]byte, 4)
	binary.LittleEndian.PutUint32(raw, 0x700a0000)
	if i, err := loong64asm.Decode(raw); err == nil {
		t.Logf("upstream now decodes LSX: %s", loong64asm.GNUSyntax(i))
	} else {
		t.Log("x/arch v0.32 lacks LSX; vector words checked against Go assembler goldens and QEMU")
	}
	for _, w := range []uint32{0x1c000013, 0x02402093, 0x0011cc13} {
		binary.LittleEndian.PutUint32(raw, w)
		if _, err := loong64asm.Decode(raw); err != nil {
			t.Fatalf("scalar expansion %08x: %v", w, err)
		}
	}
	for _, r := range []backend.Register{12, 19, 20} {
		i := machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVLoad, Bits: 32, A: backend.VectorRegister(uint8(r)), B: 4}
		if got := loong64.ImplicitWrites(i, abi.StackNoUse, 0); len(got) != 0 {
			t.Fatalf("vector load falsely reports GP writes %v", got)
		}
		i = machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVExtract, Bits: 32, A: r, B: backend.VectorRegister(31)}
		if got := loong64.ImplicitWrites(i, abi.StackNoUse, 0); !slices.Equal(got, []backend.Register{r}) {
			t.Fatalf("vector extract GP write: %v want%d", got, r)
		}
	}
}
