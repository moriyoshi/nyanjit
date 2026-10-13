//go:build linux && (ppc64 || ppc64le)

package compile

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/target/ppc64"
	"runtime"
)

// All registered PPC64 frames keep LR at 0(SP), including runtime-origin C
// entries. Track concrete instruction PCs so expansions cannot skew pcsp.
func goUnwindFromProgram(program physicalProgram) (goUnwindInfo, error) {
	info := goUnwindInfo{CodeBytes: program.codeBytes, FrameBytes: program.stackBytes, SP: []goSPPoint{{PC: 0, Delta: 0}}}
	profile, _ := abi.NewPPC64Profile(runtime.GOARCH, abi.Go, abi.PPC64CABIUnspecified)
	emitter, _ := ppc64.NewEmitter(profile, abi.StackPlatform)
	delta := 0
	setSP := func(pc, value int) error {
		if pc < 0 || pc > program.codeBytes || value < 0 || value > program.stackBytes {
			return ErrInvalidFunction
		}
		if info.SP[len(info.SP)-1].PC == pc {
			info.SP[len(info.SP)-1].Delta = value
		} else {
			info.SP = append(info.SP, goSPPoint{PC: pc, Delta: value})
		}
		delta = value
		return nil
	}
	for index, located := range program.insns {
		ins, pc := located.ins, located.pc
		switch ins.Op {
		case pFrameEnter, pFrameLeave:
			if int(ins.Imm) != program.stackBytes || ins.Op == pFrameEnter && delta != 0 || ins.Op == pFrameLeave && delta != program.stackBytes {
				return goUnwindInfo{}, ErrInvalidFunction
			}
			sequence, err := emitter.Expand(ins, pc)
			if err != nil {
				return goUnwindInfo{}, err
			}
			found := false
			for _, native := range sequence {
				if native.A == 1 && (native.Op == ppc64.Addi || native.Op == ppc64.Add) {
					next := 0
					if ins.Op == pFrameEnter {
						next = program.stackBytes
					}
					if err := setSP(native.PC+4, next); err != nil {
						return goUnwindInfo{}, err
					}
					found = true
				}
			}
			if !found {
				return goUnwindInfo{}, ErrInvalidFunction
			}
		case pCallGo:
			if delta == 0 {
				return goUnwindInfo{}, ErrStackRequired
			}
			sequence, err := emitter.Expand(ins, pc)
			if err != nil {
				return goUnwindInfo{}, err
			}
			for _, native := range sequence {
				if native.Op == ppc64.Bctrl {
					info.GoCalls = append(info.GoCalls, native.PC+4)
				}
			}
		case pReturn:
			if program.stackBytes != 0 && index+1 < len(program.insns) {
				if err := setSP(pc+4, program.stackBytes); err != nil {
					return goUnwindInfo{}, err
				}
			}
		}
	}
	return info, nil
}
