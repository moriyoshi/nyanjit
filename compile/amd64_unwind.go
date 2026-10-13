//go:build linux && amd64

package compile

// goUnwindFromProgram records the stack depth visible at every amd64 PC. CALL
// pushes the return address, so the Go unwinder adds that word separately;
// pcsp describes only the generated function's SUB/ADD RSP adjustment.
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
			if int(ins.Imm) != program.stackBytes || delta != 0 {
				return goUnwindInfo{}, ErrInvalidFunction
			}
			if err := setSP(pc+7, program.stackBytes); err != nil {
				return goUnwindInfo{}, err
			}
		case pFrameLeave:
			if int(ins.Imm) != program.stackBytes || delta != program.stackBytes {
				return goUnwindInfo{}, ErrInvalidFunction
			}
			if err := setSP(pc+7, 0); err != nil {
				return goUnwindInfo{}, err
			}
		case pCallGo:
			if delta == 0 {
				return goUnwindInfo{}, ErrStackRequired
			}
			info.GoCalls = append(info.GoCalls, pc+3)
		case pReturn:
			// A later label can be reached directly while the shared frame
			// remains open, even though linear layout follows a RET.
			if program.stackBytes != 0 && index+1 < len(program.insns) {
				if err := setSP(pc+1, program.stackBytes); err != nil {
					return goUnwindInfo{}, err
				}
			}
		}
	}
	return info, nil
}
