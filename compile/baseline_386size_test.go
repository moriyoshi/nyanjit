package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/ir"
)

// TestI386BaselineSizes checks the 386 baseline's computed sizes against the
// bytes its encoding produces, around the boundaries of the short
// immediate forms.
func TestI386BaselineSizes(t *testing.T) {
	x := i386BaselineArch{}
	imms := []int64{-129, -128, -1, 0, 1, 127, 128, 0x7fffffff, -0x80000000, 0xffffffff, 0x12345678, -0x12345678}
	for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
		for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
			e := &baselineEngine{options: compileOptions{Stack: stack, ABI: flavor}, arch: x, symbols: []string{"s"}}
			e.m = x.model(e.options)
			enc := newI386Enc(e)
			check := func(p bPhys) {
				t.Helper()
				want := x.size(e, &p) // may mark p, as finish's layout does
				if e.emit.sizeErr {
					t.Fatalf("%+v: does not encode", p)
				}
				enc.reset(flavor)
				if err := enc.one(e, &p, 0, nil); err != nil || enc.Err() != nil {
					t.Fatalf("%+v: %v %v", p, err, enc.Err())
				}
				if got := enc.AssembledSize(); got != want {
					t.Fatalf("%v %v %+v: size %d, encoded %d", stack, flavor, p, want, got)
				}
			}
			// EAX is never allocated, but the encoder has its short form.
			gps := []int8{x86EAX, x86EBX, x86ESI, x86EDI}
			fps := []int8{fpBase + 3, fpBase + 7}
			for _, v := range imms {
				imm := uint64(v)
				for _, w := range []uint8{8, 16, 32} {
					for _, o := range []ir.Opcode{ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor} {
						for _, a := range gps {
							check(bPhys{k: pkOpImm, sub: uint8(o), w: w, a: a, b: a, imm: imm})
							check(bPhys{k: pkOpImm, sub: uint8(o), w: w, a: a, b: x86EDI ^ a&1, imm: imm})
						}
					}
				}
				for _, w := range []uint8{8, 16, 32, 64} {
					for c := op.BranchCondition(0); c <= op.BranchAbove; c++ {
						b := gps[int(c)%len(gps)]
						if w == 64 {
							b = fps[int(c)%len(fps)]
						}
						check(bPhys{k: pkCmpImm, sub: uint8(c), w: w, b: b, imm: imm})
					}
				}
				for _, a := range append(gps, fps...) {
					check(bPhys{k: pkConst, a: a, imm: imm})
				}
			}
			// Register operations and shifts: an 8- or 16-bit result is
			// masked by MOVZX where its register has the width, after LEA
			// for an addition into another register, and by AND otherwise.
			regs := []int8{x86EAX, x86ECX, x86EBX, x86ESP, x86EBP, x86ESI, x86EDI}
			for _, w := range []uint8{8, 16, 32} {
				for _, o := range []ir.Opcode{ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpMul} {
					for _, a := range regs {
						for _, b := range regs {
							for _, c := range regs {
								if a != x86ESP {
									check(bPhys{k: pkOp, sub: uint8(o), w: w, a: a, b: b, c: c})
								}
							}
						}
					}
				}
				for _, o := range []ir.Opcode{ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic} {
					for _, a := range regs {
						for _, b := range regs {
							for _, count := range []uint64{0, 1, 3, uint64(w) - 1} {
								if a != x86ESP {
									check(bPhys{k: pkOp, sub: uint8(o), w: w, a: a, b: b, imm: count})
								}
							}
						}
					}
				}
			}
			if stack == abi.StackPlatform {
				for _, frame := range []uint64{16, 32, 128, 143, 144, 160, 4096, 1 << 20} {
					check(bPhys{k: pkFrameEnter, imm: frame})
					check(bPhys{k: pkFrameLeave, imm: frame})
					for words := range uint8(3) {
						check(bPhys{k: pkTailCall, imm: frame, w: words})
					}
				}
			}
			check(bPhys{k: pkTailCall})
			check(bPhys{k: pkRet})
		}
	}
}
