//go:build linux && (arm64 || amd64 || 386 || riscv64 || ppc64le || loong64)

package compile

import (
	"fmt"
	"runtime"

	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/backend/machine"
	targetamd64 "github.com/moriyoshi/nyanjit/backend/target/amd64"
	targetarm64 "github.com/moriyoshi/nyanjit/backend/target/arm64"
	targeti386 "github.com/moriyoshi/nyanjit/backend/target/i386"
	targetloong64 "github.com/moriyoshi/nyanjit/backend/target/loong64"
	targetppc64 "github.com/moriyoshi/nyanjit/backend/target/ppc64"
	targetriscv64 "github.com/moriyoshi/nyanjit/backend/target/riscv64"
)

// goUnwindFromProgram describes a native managed frame to the Go unwinder
// with the host target's GoFrame. Go calls additionally need the frame open:
// the runtime walks through it from their return PCs.
func goUnwindFromProgram(program physicalProgram) (goUnwindInfo, error) {
	var goFrame func([]machine.LocatedInstruction, int, int) (goregister.Frame, error)
	switch runtime.GOARCH {
	case "amd64":
		goFrame = targetamd64.GoFrame
	case "arm64":
		goFrame = targetarm64.GoFrame
	case "386":
		goFrame = targeti386.GoFrame
	case "riscv64":
		goFrame = targetriscv64.GoFrame
	case "loong64":
		goFrame = targetloong64.GoFrame
	case "ppc64le":
		goFrame = targetppc64.GoFrame
	}
	code := make([]machine.LocatedInstruction, len(program.insns))
	for i, located := range program.insns {
		code[i] = machine.LocatedInstruction{Instruction: located.ins, PC: located.pc}
	}
	frame, err := goFrame(code, program.codeBytes, program.stackBytes)
	if err != nil {
		return goUnwindInfo{}, fmt.Errorf("%w: %w", ErrInvalidFunction, err)
	}
	point := 0
	for _, located := range program.insns {
		for point+1 < len(frame.SP) && frame.SP[point+1].PC <= located.pc {
			point++
		}
		if located.ins.Op == pCallGo && frame.SP[point].Delta == 0 {
			return goUnwindInfo{}, ErrStackRequired
		}
	}
	return goUnwindInfo{CodeBytes: frame.CodeBytes, FrameBytes: frame.FrameBytes, SP: frame.SP}, nil
}
