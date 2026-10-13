package compile

import (
	"math/rand/v2"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/op"
	targetloong64 "github.com/moriyoshi/nyanjit/backend/target/loong64"
	"github.com/moriyoshi/nyanjit/ir"
)

// TestLoong64BaselineSizes checks the loong64 baseline's computed sizes
// against the bytes its encoding produces.
func TestLoong64BaselineSizes(t *testing.T) {
	x := loong64BaselineArch{}
	rng := rand.New(rand.NewPCG(1, 2))
	for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
		for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
			e := &baselineEngine{options: compileOptions{Stack: stack, ABI: flavor}, arch: x, symbols: []string{"s"}}
			e.m = x.model(e.options)
			asm := targetloong64.NewByteAssembler(stack, e.m.goHomes)
			check := func(p bPhys) {
				t.Helper()
				want := x.size(e, &p) // may mark p, as finish's layout does
				if e.emit.sizeErr {
					t.Fatalf("%+v: does not encode", p)
				}
				asm.Reset()
				if err := loong64EncodeOne(e, asm, &p, 0, nil); err != nil || asm.Err() != nil {
					t.Fatalf("%+v: %v %v", p, err, asm.Err())
				}
				if got := asm.AssembledSize(); got != want {
					t.Fatalf("%+v: size %d, encoded %d", p, want, got)
				}
			}
			gp := func() int8 { return []int8{4, 5, 11, 12, 13, 17, 18, 24, 31}[rng.IntN(9)] }
			fp := func() int8 { return fpBase + int8(3+rng.IntN(29)) }
			values := []uint64{0, 1, 2047, 2048, 4095, 4096, 1 << 16, 0xffffffff, 0x80000000, 1 << 52, 1<<63 | 5, ^uint64(0), 0x123456789abcdef0, 0xfffff000, 0x7ff0000000000000}
			for range 200 {
				values = append(values, rng.Uint64()>>rng.IntN(64))
			}
			for _, v := range values {
				check(bPhys{k: pkConst, a: gp(), imm: v})
				check(bPhys{k: pkConst, a: fp(), imm: v})
				check(bPhys{k: pkCmpImm, b: gp(), imm: uint64(int64(v) % 4096)})
			}
			for _, slot := range []uint64{0, 1, 200, 230, 240, 255, 256, 300, 4000} {
				check(bPhys{k: pkSpill, a: gp(), imm: slot})
				check(bPhys{k: pkReload, a: gp(), imm: slot})
				check(bPhys{k: pkSpill, a: fp(), imm: slot})
				check(bPhys{k: pkReload, a: fp(), imm: slot})
			}
			for _, call := range []bool{false, true} {
				e.alloc.hasCall = call
				for _, n := range []int{16, 2032, 2048, 4096, 10000} {
					check(bPhys{k: pkFrameEnter, imm: uint64(n)})
					check(bPhys{k: pkFrameLeave, imm: uint64(n)})
				}
			}
			check(bPhys{k: pkSymbol, a: gp()})
			check(bPhys{k: pkSymbol, a: fp()})
			for _, m := range [][2]int8{{4, 5}, {5, 5}, {fp(), 4}, {4, fp()}, {fp(), fp()}} {
				check(bPhys{k: pkMove, a: m[0], b: m[1]})
			}
			intOps := []ir.Opcode{ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpShiftLeftBy, ir.OpShiftRightBy,
				ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy, ir.OpMulHigh, ir.OpMulHighSigned, ir.OpCarryAdd,
				ir.OpBorrowSub, ir.OpDiv, ir.OpDivSigned, ir.OpRem, ir.OpRemSigned, ir.OpSelect, ir.OpAddWithCarry, ir.OpSubWithBorrow,
				ir.OpLeadingZeros, ir.OpTrailingZeros, ir.OpLoad8, ir.OpLoad64, ir.OpStore8, ir.OpStore64}
			for _, w := range []uint8{8, 16, 32, 64} {
				for _, o := range intOps {
					for sem := range uint64(3) {
						imm := uint64(0)
						if o == ir.OpDiv || o == ir.OpDivSigned || o == ir.OpRem || o == ir.OpRemSigned {
							imm = sem
						}
						check(bPhys{k: pkOp, sub: uint8(o), w: w, a: gp(), b: gp(), c: gp(), d: gp(), imm: imm})
					}
				}
				for _, o := range []ir.Opcode{ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor} {
					check(bPhys{k: pkOpImm, sub: uint8(o), w: w, a: gp(), b: gp(), imm: uint64(rng.IntN(2048))})
				}
				for c := range uint64(w) {
					for _, o := range []ir.Opcode{ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight} {
						r := gp()
						check(bPhys{k: pkOp, sub: uint8(o), w: w, a: r, b: r, imm: c})
						check(bPhys{k: pkOp, sub: uint8(o), w: w, a: gp(), b: gp(), imm: c})
					}
				}
				for _, from := range []uint8{8, 16, 32, 64} {
					check(bPhys{k: pkOp, sub: uint8(ir.OpCastUnsigned), w: w, a: gp(), b: gp(), d: int8(from)})
					if from <= w && from < 64 {
						check(bPhys{k: pkOp, sub: uint8(ir.OpSignExtend), w: w, a: gp(), b: gp(), imm: uint64(from)})
					}
				}
				for _, s := range []uint64{0, 1, 2, 3, 4, 8, 16, 32, 1 << 40, 12345, 0xffffffffff} {
					check(bPhys{k: pkOp, sub: uint8(ir.OpAddScaled), w: w, a: gp(), b: gp(), c: gp(), imm: s})
				}
				for _, o := range []ir.Opcode{ir.OpLoad16, ir.OpLoad32, ir.OpStore16, ir.OpStore32} {
					for _, ord := range []op.ByteOrder{op.ByteOrderNative, op.ByteOrderLittleEndian, op.ByteOrderBigEndian} {
						check(bPhys{k: pkOp, sub: uint8(o), w: w, a: gp(), b: gp(), c: gp(), imm: uint64(ord)})
					}
				}
			}
			for _, w := range []uint8{32, 64} {
				for _, o := range []ir.Opcode{ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv, ir.OpFMA, ir.OpFSqrt, ir.OpFConvert} {
					check(bPhys{k: pkOp, sub: uint8(o), w: w, a: fp(), b: fp(), c: fp(), d: fp()})
				}
				for _, from := range []int8{32, 64} {
					for _, o := range []ir.Opcode{ir.OpFFromSigned, ir.OpFFromUnsigned} {
						check(bPhys{k: pkOp, sub: uint8(o), w: w, a: fp(), b: gp(), d: from})
					}
					for _, o := range []ir.Opcode{ir.OpFToSigned, ir.OpFToUnsigned} {
						for sem := range uint64(2) {
							check(bPhys{k: pkOp, sub: uint8(o), w: w, a: gp(), b: fp(), d: from, imm: sem})
						}
					}
				}
				for c := range uint64(14) {
					check(bPhys{k: pkOp, sub: uint8(ir.OpFCompare), w: 64, a: gp(), b: fp(), c: fp(), d: int8(w), imm: c})
				}
			}
			check(bPhys{k: pkOp, sub: uint8(ir.OpFProductError), w: 64, a: fp(), b: fp(), c: fp(), d: fp()})
		}
	}
}
