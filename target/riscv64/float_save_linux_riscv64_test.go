//go:build linux && riscv64

package riscv64

import (
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/execution"
	"github.com/moriyoshi/nyanjit/machine"
)

func TestCFloatSaveRestore(t *testing.T) {
	for _, slot := range []int{0, 256} {
		for _, reg := range []uint32{8, 9, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27} {
			a := NewByteAssembler(abi.StackPlatform, 0)
			frame := 32
			if slot != 0 {
				frame = 4096
			}
			a.FrameEnter(frame)
			a.SaveCFloat(machine.Register(reg), 1)                      // Preserve the caller's Freg.
			a.words = append(a.words, rWord(0x79, 0, 10, 0, reg, 0x53)) // FMV.D.X Freg, X10.
			a.SaveCFloat(machine.Register(reg), slot)
			a.words = append(a.words, rWord(0x79, 0, 0, 0, reg, 0x53)) // Clear Freg.
			a.RestoreCFloat(machine.Register(reg), slot)
			a.words = append(a.words, rWord(0x71, 0, reg, 0, 10, 0x53)) // FMV.X.D X10, Freg.
			a.RestoreCFloat(machine.Register(reg), 1)
			a.FrameLeave(frame)
			a.Ret()
			code, err := execution.NewCode(a.Bytes(), execution.CodeOptions{ABI: abi.C, Arity: 1}, nil)
			if err != nil {
				t.Fatal(err)
			}
			const want = uint64(0x7ff8123456789abc)
			got, callErr := code.Call(uintptr(want))
			closeErr := code.Close()
			if callErr != nil || closeErr != nil || uint64(got) != want {
				t.Fatalf("F%d slot %d: got %#x, call %v, close %v; want %#x", reg, slot, got, callErr, closeErr, want)
			}
		}
	}
}
