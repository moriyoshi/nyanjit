//go:build linux && 386

package compile

// goUnwindFromProgram describes the 386 frame at Go call return PCs. The
// prologue pushes four preserved registers and then adjusts SP; the Go
// runtime only enters this pointer-free frame at a call return PC.
func goUnwindFromProgram(program physicalProgram) (goUnwindInfo, error) {
	info := goUnwindInfo{
		CodeBytes: program.codeBytes, FrameBytes: program.stackBytes,
		SP: []goSPPoint{{PC: 0, Delta: 0}},
	}
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
		case pFrameEnter:
			if int(ins.Imm) != program.stackBytes || delta != 0 {
				return goUnwindInfo{}, ErrInvalidFunction
			}
			if err := setSP(pc+10, program.stackBytes); err != nil {
				return goUnwindInfo{}, err
			}
		case pFrameLeave:
			if int(ins.Imm) != program.stackBytes || delta != program.stackBytes {
				return goUnwindInfo{}, ErrInvalidFunction
			}
			if err := setSP(pc+10, 0); err != nil {
				return goUnwindInfo{}, err
			}
		case pCallGo:
			if delta == 0 {
				return goUnwindInfo{}, ErrStackRequired
			}
			info.GoCalls = append(info.GoCalls, pc+2)
		case pReturn:
			if program.stackBytes != 0 && index+1 < len(program.insns) {
				if err := setSP(pc+1, program.stackBytes); err != nil {
					return goUnwindInfo{}, err
				}
			}
		}
	}
	return info, nil
}
