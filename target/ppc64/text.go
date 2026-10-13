package ppc64

import (
	"fmt"
	"strings"
)

// TextAssembler renders concrete POWER instructions without producing bytes.
type TextAssembler struct {
	text strings.Builder
	size int
}

func NewTextAssembler() *TextAssembler      { return &TextAssembler{} }
func (e *TextAssembler) AssembledSize() int { return e.size }
func (e *TextAssembler) Text() string       { return e.text.String() }
func (e *TextAssembler) Visit(ins Instruction) error {
	if _, err := Encode(ins); err != nil {
		return err
	}
	line := instructionText(ins)
	if ins.Record {
		if space := strings.IndexByte(line, ' '); space >= 0 {
			line = line[:space] + "." + line[space:]
		}
	}
	if ins.Symbol != "" {
		line += " # symbol " + ins.Symbol
	}
	fmt.Fprintf(&e.text, "  %04x: %s\n", ins.PC, line)
	e.size += 4
	return nil
}

func instructionText(ins Instruction) string {
	r := func(register uint8) string { return fmt.Sprintf("r%d", register) }
	a, b, c := r(uint8(ins.A)), r(uint8(ins.B)), r(uint8(ins.C))
	three := func(name string) string { return fmt.Sprintf("%s %s, %s, %s", name, a, b, c) }
	logicalImmediate := func(name string) string { return fmt.Sprintf("%s %s, %s, %#x", name, a, b, ins.Imm) }
	memory := func(name string) string { return fmt.Sprintf("%s %s, %d(%s)", name, a, ins.Imm, b) }
	switch ins.Op {
	case Addi:
		if ins.B == 0 {
			return fmt.Sprintf("li %s, %d", a, ins.Imm)
		}
		return fmt.Sprintf("addi %s, %s, %d", a, b, ins.Imm)
	case Addis:
		if ins.B == 0 {
			return fmt.Sprintf("lis %s, %d", a, ins.Imm)
		}
		return fmt.Sprintf("addis %s, %s, %d", a, b, ins.Imm)
	case Ori:
		return logicalImmediate("ori")
	case Oris:
		return logicalImmediate("oris")
	case Xori:
		return logicalImmediate("xori")
	case Andi:
		return logicalImmediate("andi.")
	case Add:
		return three("add")
	case Adde:
		return three("adde")
	case Subfe:
		return fmt.Sprintf("subfe %s, %s, %s", a, c, b)
	case Addze:
		return fmt.Sprintf("addze %s, %s", a, b)
	case Subfze:
		return fmt.Sprintf("subfze %s, %s", a, b)
	case Subfic:
		return fmt.Sprintf("subfic %s, %s, %d", a, b, ins.Imm)
	case Addc:
		return three("addc")
	case Subfc:
		return fmt.Sprintf("subfc %s, %s, %s", a, c, b)
	case Addic:
		return fmt.Sprintf("addic %s, %s, %d", a, b, ins.Imm)
	case Mfxer:
		return fmt.Sprintf("mfxer %s", a)
	case Mcrxrx:
		return fmt.Sprintf("mcrxrx cr%d", ins.Imm)
	case Subf:
		return fmt.Sprintf("subf %s, %s, %s", a, c, b)
	case Mulhdu:
		return three("mulhdu")
	case Mulhwu:
		return three("mulhwu")
	case Mulld:
		return three("mulld")
	case Maddld:
		return fmt.Sprintf("maddld %s, %s, %s, r%d", a, b, c, ins.D)
	case And:
		return three("and")
	case Or:
		if ins.B == ins.C {
			return fmt.Sprintf("mr %s, %s", a, b)
		}
		return three("or")
	case Xor:
		return three("xor")
	case Rldicl:
		return fmt.Sprintf("rldicl %s, %s, %d, %d", a, b, ins.Shift, ins.Mask)
	case Rldicr:
		return fmt.Sprintf("rldicr %s, %s, %d, %d", a, b, ins.Shift, ins.Mask)
	case Sld, Srd, Srad, Slw, Srw, Sraw:
		return three(map[Opcode]string{Sld: "sld", Srd: "srd", Srad: "srad", Slw: "slw", Srw: "srw", Sraw: "sraw"}[ins.Op])
	case Rldcl:
		return fmt.Sprintf("rldcl %s, %s, %s, %d", a, b, c, ins.Mask)
	case Rlwnm:
		return fmt.Sprintf("rlwnm %s, %s, %s, %d, %d", a, b, c, ins.Mask, ins.MaskEnd)
	case Rlwimi:
		return fmt.Sprintf("rlwimi %s, %s, %d, %d, %d", a, b, ins.Shift, ins.Mask, ins.MaskEnd)
	case Rlwinm:
		return fmt.Sprintf("rlwinm %s, %s, %d, %d, %d", a, b, ins.Shift, ins.Mask, ins.MaskEnd)
	case Sradi:
		return fmt.Sprintf("sradi %s, %s, %d", a, b, ins.Shift)
	case Extsb:
		return fmt.Sprintf("extsb %s, %s", a, b)
	case Extsh:
		return fmt.Sprintf("extsh %s, %s", a, b)
	case Extsw:
		return fmt.Sprintf("extsw %s, %s", a, b)
	case Ld:
		return memory("ld")
	case Std:
		return memory("std")
	case Lbz:
		return memory("lbz")
	case Stb:
		return memory("stb")
	case Stdu:
		return memory("stdu")
	case Ldx:
		return three("ldx")
	case Stdx:
		return three("stdx")
	case Ldbrx:
		return three("ldbrx")
	case Stdbrx:
		return three("stdbrx")
	case Stdux:
		return three("stdux")
	case Cmp, Cmpl, Cmpi, Cmpli:
		name := "cmp"
		if ins.Op == Cmpl || ins.Op == Cmpli {
			name += "l"
		}
		if ins.Bits == 64 {
			name += "d"
		} else {
			name += "w"
		}
		if ins.Op == Cmpi || ins.Op == Cmpli {
			return fmt.Sprintf("%si %s, %d", name, b, ins.Imm)
		}
		return fmt.Sprintf("%s %s, %s", name, b, c)
	case Isel:
		return fmt.Sprintf("isel %s, %s, %s, %d", a, b, c, ins.BI)
	case B:
		return fmt.Sprintf("b %#x", int64(ins.PC)+ins.Imm)
	case Bc:
		if ins.Link {
			return fmt.Sprintf("bcl %d, %d, %#x", ins.BO, ins.BI, int64(ins.PC)+ins.Imm)
		}
		if ins.BO == 16 || ins.BO == 18 {
			name := "bdnz"
			if ins.BO == 18 {
				name = "bdz"
			}
			return fmt.Sprintf("%s %#x", name, int64(ins.PC)+ins.Imm)
		}
		if ins.BO == 4 || ins.BO == 12 {
			name := ""
			switch ins.BI {
			case 0:
				if ins.BO == 12 {
					name = "blt"
				} else {
					name = "bge"
				}
			case 1:
				if ins.BO == 12 {
					name = "bgt"
				} else {
					name = "ble"
				}
			case 2:
				if ins.BO == 12 {
					name = "beq"
				} else {
					name = "bne"
				}
			}
			if name != "" {
				return fmt.Sprintf("%s %#x", name, int64(ins.PC)+ins.Imm)
			}
		}
		return fmt.Sprintf("bc %d, %d, %#x", ins.BO, ins.BI, int64(ins.PC)+ins.Imm)
	case Lfs:
		return fmt.Sprintf("lfs f%d, %d(r%d)", ins.A, ins.Imm, ins.B)
	case Fneg, Fabs:
		op := "fneg"
		if ins.Op == Fabs {
			op = "fabs"
		}
		return fmt.Sprintf("%s f%d, f%d", op, ins.A, ins.B)
	case Fmr:
		return fmt.Sprintf("fmr f%d, f%d", ins.A, ins.B)
	case Lfd:
		return fmt.Sprintf("lfd f%d, %d(%s)", ins.A, ins.Imm, b)
	case Stfd:
		return fmt.Sprintf("stfd f%d, %d(%s)", ins.A, ins.Imm, b)
	case Lvx:
		return fmt.Sprintf("lvx v%d, %s, %s", ins.A, b, c)
	case Stvx:
		return fmt.Sprintf("stvx v%d, %s, %s", ins.A, b, c)
	case Mfcr:
		return "mfcr " + a
	case Mtcrf:
		return fmt.Sprintf("mtcrf %#x, %s", ins.Imm, a)
	case Mtvsrd:
		return fmt.Sprintf("mtvsrd vs%d, %s", ins.A, b)
	case Mfvsrd:
		return fmt.Sprintf("mfvsrd %s, vs%d", a, ins.B)
	case Mfvsrwz:
		return fmt.Sprintf("mfvsrwz %s, vs%d", a, ins.B)
	case Xxsldwi:
		return fmt.Sprintf("xxsldwi vs%d, vs%d, vs%d, %d", ins.A, ins.B, ins.C, ins.Shift)
	case Xscvspdpn:
		return fmt.Sprintf("xscvspdpn vs%d, vs%d", ins.A, ins.B)
	case Xscvdpspn:
		return fmt.Sprintf("xscvdpspn vs%d, vs%d", ins.A, ins.B)
	case Frsp:
		return fmt.Sprintf("frsp f%d, f%d", ins.A, ins.B)
	case Fsqrt:
		name := "fsqrt"
		if ins.Bits == 32 {
			name += "s"
		}
		return fmt.Sprintf("%s f%d, f%d", name, ins.A, ins.B)
	case Fadd, Fsub, Fmul, Fdiv, Fmadd:
		name := map[Opcode]string{Fadd: "fadd", Fsub: "fsub", Fmul: "fmul", Fdiv: "fdiv", Fmadd: "fmadd"}[ins.Op]
		if ins.Bits == 32 {
			name += "s"
		}
		if ins.Op == Fmadd {
			return fmt.Sprintf("%s f%d, f%d, f%d, f%d", name, ins.A, ins.B, ins.C, ins.D)
		}
		return fmt.Sprintf("%s f%d, f%d, f%d", name, ins.A, ins.B, ins.C)
	case Mflr:
		return "mflr " + a
	case Mtlr:
		return "mtlr " + a
	case Mtctr:
		return "mtctr " + a
	case Bctr:
		return "bctr"
	case Bctrl:
		return "bctrl"
	case Sc:
		return "sc"
	case Neg:
		return fmt.Sprintf("neg %s, %s", a, b)
	case Blr:
		return "blr"
	default:
		return "invalid"
	}
}

func (e *TextAssembler) Data(data []byte) error {
	fmt.Fprintf(&e.text, "%04x  .byte % x\n", e.size, data)
	e.size += len(data)
	return nil
}
