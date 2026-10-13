//go:build linux && 386

package i386_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/cpuprofile"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
)

func TestVectorRightHomeAliasExecution(t *testing.T) {
	for _, tc := range []struct {
		op   op.Opcode
		bits uint8
		want uintptr
	}{
		{op.OpAdd, 32, 24}, {op.OpSub, 32, 18}, {op.OpMul, 16, 63}, {op.OpAnd, 32, 1}, {op.OpOr, 32, 23}, {op.OpXor, 32, 22}, {op.OpVCarrylessMul, 64, 63},
	} {
		if tc.op == op.OpVCarrylessMul && !cpuprofile.Host().Has("pclmulqdq") {
			continue
		}
		for _, rightAlias := range []bool{false, true} {
			a := i386.NewByteAssembler(abi.StackNoUse)
			f := backend.VectorRegister
			a.Constant(2, 0)
			for n, value := range []uint64{21, 3} {
				a.Constant(0, value)
				if err := a.Vector(machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVSplat, Bits: tc.bits, A: f(uint8(3 + n)), B: 0, HighB: 2}); err != nil {
					t.Fatal(err)
				}
			}
			dst := f(3)
			if rightAlias {
				dst = f(4)
			}
			if err := a.Vector(machine.Instruction{Op: machine.OpVector, Arithmetic: tc.op, Bits: tc.bits, A: dst, B: f(3), C: f(4)}); err != nil {
				t.Fatal(err)
			}
			if err := a.Vector(machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVExtract, Bits: tc.bits, A: 0, HighA: 2, B: dst}); err != nil {
				t.Fatal(err)
			}
			a.Ret()
			code, err := execution.NewCode(a.Bytes(), execution.CodeOptions{ABI: abi.C}, nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := code.Call()
			code.Close()
			if err != nil || got != tc.want {
				t.Fatalf("op%d rightAlias%v got%x,%v want%x", tc.op, rightAlias, got, err, tc.want)
			}
		}
	}
}
