package loong64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

func FrameBytes(slots int, call bool) int {
	if slots < 0 || uint64(slots) > uint64(^uint(0)>>1)/16-2 {
		return 0
	}
	if slots == 0 && !call {
		return 0
	}
	return ((slots+1)*8 + 15) &^ 15
}
func FlagEffect(machine.Instruction) bool { return false }

// PhysicalSize measures instructions for the stack policy and number of Go
// argument homes. Zero goHomes selects an unmanaged layout.
func PhysicalSize(i machine.Instruction, s abi.StackPractice, goHomes int) int {
	if !validSizeFields(i) {
		return 0
	}
	e := NewByteAssembler(s, goHomes)
	e.sizeOnly = true
	if err := machine.Visit(i, e); err != nil {
		return 0
	}
	return e.AssembledSize()
}

// ImplicitWrites derives the exact reserved-register writes from the same plan
// that supplies bytes, listings and sizes. Stores/branches have no GP destination.
func ImplicitWrites(i machine.Instruction, s abi.StackPractice, goHomes int) []backend.Register {
	if !validSizeFields(i) {
		return nil
	}
	if i.Op == machine.OpLiteralData || i.Op == machine.OpBarrier {
		return nil
	}
	e := NewByteAssembler(s, goHomes)
	e.sizeOnly = true
	if err := machine.Visit(i, e); err != nil {
		return nil
	}
	seen := map[backend.Register]bool{}
	var out []backend.Register
	for _, p := range e.plan {
		w := p.word
		top := w >> 26
		vectorPick := w&0xfffc0000 == 0x72f00000
		if top >= 0x10 && top != 0x13 && !vectorPick {
			continue
		} // Branches have no GP destination; JIRL writes its link register.
		// Immediate integer stores occupy 0x29xxxxxx; indexed stores 0x381xxxxx.
		if w&0xff000000 == 0x29000000 || w&0xffff8000 == 0x381c0000 || w&0xffc00000 == 0x2bc00000 || w&0xffc00000 == 0x2b800000 || w == 0x002b0000 {
			continue
		}
		// LSX memory instructions name vector destinations, not GP registers.
		if w&0xff800000 == 0x2c000000 || w&0xffff8000 == 0x38400000 || w&0xffff8000 == 0x38440000 {
			continue
		}
		// Scalar FP instructions write the FP bank, except bank transfers to
		// GP (movfr2gr, movcf2gr); fcmp writes only an fcc register.
		if (w >= 0x01000000 && w < 0x02000000 && w&0xfffffc00 != 0x452d<<10 && w&0xfffffc00 != 0x452e<<10 && w&0xffffff00 != 0x0114dc00) || w&0xff000000 == 0x0c000000 || w&0xff000000 == 0x08000000 || w&0xff000000 == 0x2b000000 {
			continue
		}
		d := backend.Register(w & 31)
		if (d == 12 || d == 19 || d == 20) && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}
