//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"strconv"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// A 64-bit OpVMaskBits result, including that of a value compared with
// itself, compiles on every target; 386 used to reject it with a residency
// mismatch (security audit 2026-10-25, L20).
func TestVectorMaskBitsWideResult(t *testing.T) {
	m, address := simdMemory(t)
	for i := 0; i < 32; i++ {
		m[i] = byte(i)
	}
	m[16+3], m[16+9] = 3, 9 // lanes 3 and 9 of the two vectors are equal
	native := uint8(strconv.IntSize)
	for _, bits := range []uint8{16, 32, 64} {
		for _, self := range []bool{false, true} {
			for _, condition := range []ir.BranchCondition{ir.BranchEqual, ir.BranchBelow} {
				right, want := 3, uint64(1<<3|1<<9)
				if condition == ir.BranchBelow {
					want = 0xffff &^ want // the other lanes of the first vector are smaller
				}
				if self {
					right, want = 2, 0xffff
					if condition == ir.BranchBelow {
						want = 0
					}
				}
				p := &ir.Program{Instructions: []ir.Instruction{
					{Op: ir.OpParam, Bits: native},
					{Op: ir.OpParam, Bits: native},
					{Op: ir.OpVLoad, Type: ir.Vec(16, 8), Bits: 8, A: 0},
					{Op: ir.OpVLoad, Type: ir.Vec(16, 8), Bits: 8, A: 1},
					{Op: ir.OpVCompare, Type: ir.Mask(16), Bits: 8, A: 2, B: right, Imm: uint64(condition)},
					{Op: ir.OpVMaskBits, Bits: bits, A: 4},
				}, Result: 5, Arity: 2}
				for _, arch := range []string{"amd64", "386", "arm64", "riscv64", "loong64", "ppc64le"} {
					target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch})
					if err != nil {
						t.Fatal(err)
					}
					f := compile.Func{ABI: abi.C, Context: compile.ContextExternal, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 20}}
					unit, err := compile.Lower(target, p, f)
					if err == nil {
						_, err = unit.Listing()
					}
					if err != nil {
						t.Errorf("%s bits=%d self=%v %v: %v", arch, bits, self, condition, err)
					}
				}
				code, err := nyanjit.Compile(p, compile.TargetSpec{}, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
				if err != nil {
					t.Fatalf("bits=%d self=%v %v: %v", bits, self, condition, err)
				}
				lo, hi, err := code.CallPair(address, address+16)
				code.Close()
				if got := execution.Wide(lo, hi); err != nil || got != want {
					t.Errorf("bits=%d self=%v %v: got %#x, %v; want %#x", bits, self, condition, got, err, want)
				}
			}
		}
	}
}
