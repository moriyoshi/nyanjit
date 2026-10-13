//go:build linux && arm64

package compile

// goUnwindFromProgram describes the frame as the Go unwinder sees it.
// A small prologue changes SP in its first instruction. A large prologue
// builds the frame through X17 and changes SP after both links are saved.
func goUnwindFromProgram(program physicalProgram) (goUnwindInfo, error) {
	info := goUnwindInfo{
		CodeBytes:  program.codeBytes,
		FrameBytes: program.stackBytes,
		SP:         []goSPPoint{{PC: 0, Delta: 0}},
	}
	delta := 0
	setSP := func(pc, value int) error {
		if pc < 0 || pc > program.codeBytes || value < 0 || value > program.stackBytes {
			return ErrInvalidFunction
		}
		if pc == info.SP[len(info.SP)-1].PC {
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
		case pFrameEnter:
			frame := int(ins.Imm)
			if frame != program.stackBytes || delta != 0 {
				return goUnwindInfo{}, ErrInvalidFunction
			}
			changePC := pc + 4
			if frame > 240 {
				changePC = pc + arm64FrameEnterSize(frame) - 4
			}
			if err := setSP(changePC, frame); err != nil {
				return goUnwindInfo{}, err
			}
		case pFrameLeave:
			frame := int(ins.Imm)
			if frame != program.stackBytes || delta != frame {
				return goUnwindInfo{}, ErrInvalidFunction
			}
			if frame <= 240 {
				if err := setSP(pc+8, 0); err != nil {
					return goUnwindInfo{}, err
				}
				continue
			}
			at := pc + 8 // FP and LR loads precede the ADD SP instructions.
			for remaining := frame; remaining > 0; {
				step := remaining
				if step > 4080 {
					step = 4080
				}
				remaining -= step
				at += 4
				if err := setSP(at, remaining); err != nil {
					return goUnwindInfo{}, err
				}
			}
		case pCallGo:
			if delta == 0 {
				return goUnwindInfo{}, ErrStackRequired
			}
			info.GoCalls = append(info.GoCalls, pc+4)
		case pReturn:
			// A later label can be reached by a branch with the original
			// frame still open, even though linear layout follows a RET.
			if program.stackBytes != 0 && index+1 < len(program.insns) {
				if err := setSP(pc+4, program.stackBytes); err != nil {
					return goUnwindInfo{}, err
				}
			}
		}
	}
	return info, nil
}
