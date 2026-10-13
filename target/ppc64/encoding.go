package ppc64

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/machine"
)

func invalidInstruction(ins Instruction) error {
	return fmt.Errorf("ppc64 instruction %d at PC %#x has invalid operands: %w", ins.Op, ins.PC, machine.ErrUnsupportedOperation)
}

// Encode converts one concrete POWER instruction to an instruction word.
// Byte order is applied by ByteAssembler after encoding.
func Encode(ins Instruction) (word uint32, err error) {
	if ins.Record {
		switch ins.Op {
		case Add, Adde, Subfe, Addze, Subfze, Addc, Subf, Subfc, Mulld, Mulhdu, Mulhwu, And, Or, Xor, Rldicl, Rldicr, Rlwinm, Rlwimi, Rlwnm, Rldcl, Sld, Srd, Srad, Slw, Srw, Sraw, Sradi, Extsb, Extsh, Extsw:
			defer func() {
				if err == nil {
					word |= 1
				}
			}()
		case Addic: // Record is encoded in its primary opcode.
		default:
			return 0, invalidInstruction(ins)
		}
	}
	if ins.A > 31 || ins.B > 31 || ins.C > 31 || ins.D > 31 {
		return 0, invalidInstruction(ins)
	}
	a, b, c := uint32(ins.A), uint32(ins.B), uint32(ins.C)
	d := func(opcode uint32, first, second uint32) uint32 {
		return opcode<<26 | first<<21 | second<<16 | uint32(uint16(ins.Imm))
	}
	x := func(xo uint32, first, second, third uint32) uint32 {
		return 31<<26 | first<<21 | second<<16 | third<<11 | xo<<1
	}
	md := func(opcode uint32) (uint32, error) {
		if ins.Shift > 63 || ins.Mask > 63 {
			return 0, invalidInstruction(ins)
		}
		sh, mask := uint32(ins.Shift), uint32(ins.Mask)
		return 30<<26 | b<<21 | a<<16 | (sh&31)<<11 | (sh>>5)<<1 |
			(mask&31)<<6 | (mask>>5)<<5 | opcode, nil
	}
	switch ins.Op {
	case Addi, Addis, Addic, Subfic:
		if ins.Imm < -32768 || ins.Imm > 32767 {
			return 0, invalidInstruction(ins)
		}
		opcode := uint32(14)
		if ins.Op == Subfic {
			opcode = 8
		} else if ins.Op == Addis {
			opcode = 15
		} else if ins.Op == Addic {
			opcode = 12
			if ins.Record {
				opcode = 13
			}
		}
		return d(opcode, a, b), nil
	case Ori, Oris, Xori, Andi:
		if ins.Imm < 0 || ins.Imm > 65535 {
			return 0, invalidInstruction(ins)
		}
		opcode := uint32(24)
		switch ins.Op {
		case Oris:
			opcode = 25
		case Xori:
			opcode = 26
		case Andi:
			opcode = 28
		}
		return d(opcode, b, a), nil
	case Add:
		return x(266, a, b, c), nil
	case Adde:
		return x(138, a, b, c), nil
	case Subfe:
		return x(136, a, c, b), nil
	case Addze:
		return x(202, a, b, 0), nil
	case Subfze:
		return x(200, a, b, 0), nil
	case Addc:
		return x(10, a, b, c), nil
	case Subfc:
		return x(8, a, c, b), nil
	case Mfxer:
		return 0x7c0102a6 | a<<21, nil
	case Mcrxrx:
		if ins.Imm < 0 || ins.Imm > 7 {
			return 0, invalidInstruction(ins)
		}
		return 0x7c000480 | uint32(ins.Imm)<<23, nil
	case Subf:
		return x(40, a, c, b), nil // RB - RA implements left - right.
	case Mulhdu:
		return x(9, a, b, c), nil
	case Mulhwu:
		return x(11, a, b, c), nil
	case Mulld:
		return x(233, a, b, c), nil
	case Maddld:
		return 4<<26 | a<<21 | b<<16 | c<<11 | uint32(ins.D)<<6 | 51, nil
	case And:
		return x(28, b, a, c), nil
	case Or:
		return x(444, b, a, c), nil
	case Xor:
		return x(316, b, a, c), nil
	case Rldicl:
		return md(0)
	case Rldicr:
		return md(4)
	case Sld, Srd, Srad, Slw, Srw, Sraw:
		xo := map[Opcode]uint32{Sld: 27, Srd: 539, Srad: 794, Slw: 24, Srw: 536, Sraw: 792}[ins.Op]
		return x(xo, b, a, c), nil
	case Rldcl:
		if ins.Mask > 63 {
			return 0, invalidInstruction(ins)
		}
		mask := uint32(ins.Mask)
		return 30<<26 | b<<21 | a<<16 | c<<11 | (mask&31)<<6 | (mask>>5)<<5 | 16, nil
	case Rlwinm, Rlwimi, Rlwnm:
		if ins.Shift > 31 || ins.Mask > 31 || ins.MaskEnd > 31 {
			return 0, invalidInstruction(ins)
		}
		opcode, sh := uint32(21), uint32(ins.Shift)
		if ins.Op == Rlwimi {
			opcode = 20
		} else if ins.Op == Rlwnm {
			opcode, sh = 23, c
		}
		return opcode<<26 | b<<21 | a<<16 | sh<<11 | uint32(ins.Mask)<<6 | uint32(ins.MaskEnd)<<1, nil
	case Sradi:
		if ins.Shift > 63 {
			return 0, invalidInstruction(ins)
		}
		return 31<<26 | b<<21 | a<<16 | (uint32(ins.Shift)&31)<<11 | 413<<2 | uint32(ins.Shift>>5)<<1, nil
	case Extsb:
		return x(954, b, a, 0), nil
	case Extsh:
		return x(922, b, a, 0), nil
	case Extsw:
		return x(986, b, a, 0), nil
	case Ld, Std, Stdu:
		if ins.Imm < -32768 || ins.Imm > 32764 || ins.Imm&3 != 0 {
			return 0, invalidInstruction(ins)
		}
		opcode, suffix := uint32(58), uint32(0)
		if ins.Op == Std || ins.Op == Stdu {
			opcode = 62
		}
		if ins.Op == Stdu {
			suffix = 1
		}
		return d(opcode, a, b) | suffix, nil
	case Ldx:
		return x(21, a, b, c), nil
	case Stdx:
		return x(149, a, b, c), nil
	case Ldbrx:
		return x(532, a, b, c), nil
	case Stdbrx:
		return x(660, a, b, c), nil
	case Stdux:
		return x(181, a, b, c), nil
	case Lbz, Stb:
		if ins.Imm < -32768 || ins.Imm > 32767 {
			return 0, invalidInstruction(ins)
		}
		opcode := uint32(34)
		if ins.Op == Stb {
			opcode = 38
		}
		return d(opcode, a, b), nil
	case Cmp, Cmpl, Cmpi, Cmpli:
		if ins.Bits != 32 && ins.Bits != 64 {
			return 0, invalidInstruction(ins)
		}
		length := uint32(0)
		if ins.Bits == 64 {
			length = 1 << 21
		}
		switch ins.Op {
		case Cmp:
			return x(0, 0, b, c) | length, nil
		case Cmpl:
			return x(32, 0, b, c) | length, nil
		case Cmpi:
			if ins.Imm < -32768 || ins.Imm > 32767 {
				return 0, invalidInstruction(ins)
			}
			return d(11, 0, b) | length, nil
		default:
			if ins.Imm < 0 || ins.Imm > 65535 {
				return 0, invalidInstruction(ins)
			}
			return d(10, 0, b) | length, nil
		}
	case Isel:
		if ins.BI > 31 {
			return 0, invalidInstruction(ins)
		}
		return x(15, a, b, c) | uint32(ins.BI)<<6, nil
	case B:
		if ins.Imm&3 != 0 || ins.Imm < -(1<<25) || ins.Imm > (1<<25)-4 {
			return 0, invalidInstruction(ins)
		}
		return 18<<26 | uint32(ins.Imm)&0x03fffffc, nil
	case Bc:
		if ins.BO > 31 || ins.BI > 31 || ins.Imm&3 != 0 || ins.Imm < -32768 || ins.Imm > 32764 {
			return 0, invalidInstruction(ins)
		}
		word := uint32(16<<26 | uint32(ins.BO)<<21 | uint32(ins.BI)<<16 | uint32(ins.Imm)&0xfffc)
		if ins.Link {
			word |= 1
		}
		return word, nil
	case Lfd, Stfd, Lfs:
		if ins.Imm < -32768 || ins.Imm > 32767 {
			return 0, invalidInstruction(ins)
		}
		opcode := uint32(50)
		if ins.Op == Stfd {
			opcode = 54
		}
		if ins.Op == Lfs {
			opcode = 48
		}
		return d(opcode, a, b), nil
	case Lvx:
		return x(103, a, b, c), nil
	case Stvx:
		return x(231, a, b, c), nil
	case Mfcr:
		return 0x7c000026 | a<<21, nil
	case Mtcrf:
		if ins.Imm < 0 || ins.Imm > 255 {
			return 0, invalidInstruction(ins)
		}
		return 0x7c000120 | a<<21 | uint32(ins.Imm)<<12, nil
	case Mtvsrd:
		return x(179, a, b, 0), nil
	case Mfvsrd:
		return x(51, b, a, 0), nil // VSR source occupies the first register field.
	case Mfvsrwz:
		return x(115, b, a, 0), nil
	case Xxsldwi:
		if ins.Shift > 3 {
			return 0, invalidInstruction(ins)
		}
		return 60<<26 | a<<21 | b<<16 | c<<11 | uint32(ins.Shift)<<8 | 2<<3, nil
	case Xscvspdpn:
		return 60<<26 | a<<21 | b<<11 | 331<<2, nil
	case Xscvdpspn:
		return 60<<26 | a<<21 | b<<11 | 267<<2, nil
	case Frsp:
		return 63<<26 | a<<21 | b<<11 | 12<<1, nil
	case Fsqrt:
		if ins.Bits != 32 && ins.Bits != 64 {
			return 0, invalidInstruction(ins)
		}
		opcode := uint32(63)
		if ins.Bits == 32 {
			opcode = 59
		}
		return opcode<<26 | a<<21 | b<<11 | 22<<1, nil
	case Fadd, Fsub, Fmul, Fdiv, Fmadd:
		if ins.Bits != 32 && ins.Bits != 64 {
			return 0, invalidInstruction(ins)
		}
		opcode := uint32(63)
		if ins.Bits == 32 {
			opcode = 59
		}
		word := opcode<<26 | a<<21 | b<<16
		switch ins.Op {
		case Fadd:
			word |= c<<11 | 21<<1
		case Fsub:
			word |= c<<11 | 20<<1
		case Fmul:
			word |= c<<6 | 25<<1 // Multiply uses FRC rather than FRB.
		case Fdiv:
			word |= c<<11 | 18<<1
		case Fmadd:
			word |= c<<6 | uint32(ins.D)<<11 | 29<<1
		}
		return word, nil // Non-record forms preserve all CR fields.
	case Fmr, Fneg, Fabs:
		xo := uint32(72)
		if ins.Op == Fneg {
			xo = 40
		}
		if ins.Op == Fabs {
			xo = 264
		}
		return 63<<26 | a<<21 | b<<11 | xo<<1, nil
	case Mflr:
		return 0x7c0802a6 | a<<21, nil
	case Mtlr:
		return 0x7c0803a6 | a<<21, nil
	case Mtctr:
		return 0x7c0903a6 | a<<21, nil
	case Bctr:
		return 0x4e800420, nil
	case Bctrl:
		return 0x4e800421, nil
	case Sc:
		return 0x44000002, nil
	case Neg:
		return x(104, a, b, 0), nil
	case Blr:
		return 0x4e800020, nil
	default:
		return 0, invalidInstruction(ins)
	}
}
