package target_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/target/amd64"
	"github.com/moriyoshi/nyanjit/backend/target/arm64"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
	"github.com/moriyoshi/nyanjit/backend/target/riscv64"
)

type goFrameTarget struct {
	name    string
	goFrame func([]machine.LocatedInstruction, int, int) (goregister.Frame, error)
	size    func(machine.Instruction) int
	frames  []int
}

func goFrameTargets(t *testing.T) []goFrameTarget {
	profile, err := abi.NewPPC64Profile("ppc64le", abi.Go)
	if err != nil {
		t.Fatal(err)
	}
	emitter, err := ppc64.NewEmitter(profile, abi.StackPlatform)
	if err != nil {
		t.Fatal(err)
	}
	return []goFrameTarget{
		{"amd64", amd64.GoFrame, func(i machine.Instruction) int { return amd64.PhysicalSize(i, abi.StackPlatform, 0) }, []int{16, 512}},
		{"arm64", arm64.GoFrame, func(i machine.Instruction) int { return arm64.PhysicalSize(i, abi.StackPlatform, 0) }, []int{32, 240, 256, 8192}},
		{"386", i386.GoFrame, func(i machine.Instruction) int { return i386.PhysicalSize(i, abi.StackPlatform, 0) }, []int{16, 64}},
		{"riscv64", riscv64.GoFrame, func(i machine.Instruction) int { return riscv64.PhysicalSize(i, abi.StackPlatform, 0) }, []int{16, 2032, 4096}},
		{"loong64", loong64.GoFrame, func(i machine.Instruction) int { return loong64.PhysicalSize(i, abi.StackPlatform, 0) }, []int{16, 2032, 4096}},
		{"ppc64le", ppc64.GoFrame, emitter.Size, []int{32, 640}},
	}
}

func layOut(size func(machine.Instruction) int, code []machine.Instruction) ([]machine.LocatedInstruction, int) {
	located := make([]machine.LocatedInstruction, len(code))
	pc := 0
	for i, ins := range code {
		located[i] = machine.LocatedInstruction{Instruction: ins, PC: pc}
		pc += size(ins)
	}
	return located, pc
}

// spAt returns the SP delta in effect at pc.
func spAt(frame goregister.Frame, pc int) int {
	delta := 0
	for _, point := range frame.SP {
		if point.PC <= pc {
			delta = point.Delta
		}
	}
	return delta
}

func TestGoFrame(t *testing.T) {
	for _, target := range goFrameTargets(t) {
		for _, frameBytes := range target.frames {
			// A frame with an early return that a later label re-enters with
			// the frame still open.
			code, codeBytes := layOut(target.size, []machine.Instruction{
				{Op: machine.OpFrameEnter, Imm: uint64(frameBytes)},
				{Op: machine.OpMove, A: 1, B: 2},
				{Op: machine.OpFrameLeave, Imm: uint64(frameBytes)},
				{Op: machine.OpReturn},
				{Op: machine.OpMove, A: 1, B: 2},
				{Op: machine.OpFrameLeave, Imm: uint64(frameBytes)},
				{Op: machine.OpReturn},
			})
			frame, err := target.goFrame(code, codeBytes, frameBytes)
			if err != nil {
				t.Fatalf("%s frame %d: %v", target.name, frameBytes, err)
			}
			if frame.CodeBytes != codeBytes || frame.FrameBytes != frameBytes || len(frame.SP) == 0 || frame.SP[0] != (goregister.SPPoint{}) {
				t.Fatalf("%s frame %d: %+v", target.name, frameBytes, frame)
			}
			for i := 1; i < len(frame.SP); i++ {
				if frame.SP[i].PC <= frame.SP[i-1].PC || frame.SP[i].PC > codeBytes {
					t.Fatalf("%s frame %d: SP points out of order: %+v", target.name, frameBytes, frame.SP)
				}
			}
			for i, want := range []int{-1, frameBytes, -1, 0, frameBytes, -1, 0} {
				if want < 0 {
					continue
				}
				// Check after the instruction before it completes.
				if got := spAt(frame, code[i].PC); got != want {
					t.Errorf("%s frame %d: SP at %s (pc %d) = %d, want %d: %+v", target.name, frameBytes, []string{"enter", "body", "leave", "return", "label", "leave", "return"}[i], code[i].PC, got, want, frame.SP)
				}
			}
			if _, err := target.goFrame(code, codeBytes, frameBytes+16); !errors.Is(err, goregister.ErrInvalidFrame) {
				t.Errorf("%s frame %d: mismatched frame size: %v", target.name, frameBytes, err)
			}
			if _, err := target.goFrame(code, codeBytes-1, frameBytes); err == nil && frame.SP[len(frame.SP)-1].PC == codeBytes {
				t.Errorf("%s frame %d: SP beyond the code accepted", target.name, frameBytes)
			}
		}
	}
}
