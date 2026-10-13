//go:build linux && riscv64

package compile

// goUnwindFromProgram records every SP adjustment in an RV64 generated frame.
// The return address is stored at 0(SP) after the prologue.
func goUnwindFromProgram(program physicalProgram) (goUnwindInfo, error) {
	info := goUnwindInfo{CodeBytes: program.codeBytes, FrameBytes: program.stackBytes,
		SP: []goSPPoint{{PC: 0, Delta: 0}}}
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
			for remaining := program.stackBytes; remaining > 0; {
				step := remaining
				if step > 2032 {
					step = 2032
				}
				remaining -= step
				pc += 4
				if err := setSP(pc, program.stackBytes-remaining); err != nil {
					return goUnwindInfo{}, err
				}
			}
		case pFrameLeave:
			if int(ins.Imm) != program.stackBytes || delta != program.stackBytes {
				return goUnwindInfo{}, ErrInvalidFunction
			}
			pc += 4 // Restore RA before changing SP.
			for remaining := program.stackBytes; remaining > 0; {
				step := remaining
				if step > 2032 {
					step = 2032
				}
				remaining -= step
				pc += 4
				if err := setSP(pc, remaining); err != nil {
					return goUnwindInfo{}, err
				}
			}
		case pCallGo:
			if delta == 0 {
				return goUnwindInfo{}, ErrStackRequired
			}
			info.GoCalls = append(info.GoCalls, pc+4)
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
