package riscv64

import (
	"fmt"
	"strings"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

// TextAssembler visits the same selected instructions as ByteAssembler and
// renders the resulting RV64 words at their final PCs. Each line represents
// one instruction, including the instructions used to expand a selected op.
type TextAssembler struct{ *ByteAssembler }

func NewTextAssembler(stack abi.StackPractice, goHomes int) *TextAssembler {
	return &TextAssembler{ByteAssembler: NewByteAssembler(stack, goHomes)}
}

func (e *TextAssembler) Text() string {
	symbols := make(map[int]string, len(e.relocations))
	for _, relocation := range e.relocations {
		symbols[relocation.Offset] = relocation.Symbol
	}
	var out strings.Builder
	for index, word := range e.words {
		pc := index * 4
		if e.literalData[index] {
			fmt.Fprintf(&out, "%04x  .byte %02x %02x %02x %02x", pc, byte(word), byte(word>>8), byte(word>>16), byte(word>>24))
		} else {
			fmt.Fprintf(&out, "%04x  %s", pc, decodeWord(word, pc))
		}
		if name, ok := symbols[pc]; ok {
			fmt.Fprintf(&out, " # symbol %s", name)
		}
		out.WriteByte('\n')
	}
	return out.String()
}

func signBits(value uint32, width uint) int64 {
	shift := 32 - width
	return int64(int32(value<<shift) >> shift)
}

func decodeWord(word uint32, pc int) string {
	rd, rs1, rs2 := word>>7&31, word>>15&31, word>>20&31
	funct3, funct7 := word>>12&7, word>>25&127
	immI := signBits(word>>20, 12)
	switch word & 0x7f {

	case 0x43:
		width := "s"
		if word>>25&3 == 1 {
			width = "d"
		}
		return fmt.Sprintf("fmadd.%s f%d, f%d, f%d, f%d", width, rd, rs1, rs2, word>>27&31)
	case 0x37:
		return fmt.Sprintf("lui x%d, 0x%x", rd, word>>12)
	case 0x17:
		return fmt.Sprintf("auipc x%d, 0x%x", rd, word>>12)
	case 0x1b:
		if funct3 == 1 && (word>>20 == 0x600 || word>>20 == 0x601) {
			op := "clzw"
			if word>>20 == 0x601 {
				op = "ctzw"
			}
			return fmt.Sprintf("%s x%d, x%d", op, rd, rs1)
		}
		if funct3 == 5 && word&0xfe000000 == 0x60000000 {
			return fmt.Sprintf("roriw x%d, x%d, %d", rd, rs1, word>>20&31)
		}
		if funct3 == 0 {
			return fmt.Sprintf("addiw x%d, x%d, %d", rd, rs1, immI)
		}
	case 0x13:
		switch funct3 {
		case 0:
			if word == 0x00000013 {
				return "nop"
			}
			return fmt.Sprintf("addi x%d, x%d, %d", rd, rs1, immI)
		case 3:
			return fmt.Sprintf("sltiu x%d, x%d, %d", rd, rs1, immI)
		case 6:
			return fmt.Sprintf("ori x%d, x%d, %d", rd, rs1, immI)
		case 7:
			return fmt.Sprintf("andi x%d, x%d, %d", rd, rs1, immI)
		case 4:
			return fmt.Sprintf("xori x%d, x%d, %d", rd, rs1, immI)
		case 1:
			if word>>20 == 0x600 || word>>20 == 0x601 {
				op := "clz"
				if word>>20 == 0x601 {
					op = "ctz"
				}
				return fmt.Sprintf("%s x%d, x%d", op, rd, rs1)
			}
			if word&0xfc000000 == 0 {
				return fmt.Sprintf("slli x%d, x%d, %d", rd, rs1, word>>20&63)
			}
		case 5:
			switch word & 0xfc000000 {
			case 0:
				return fmt.Sprintf("srli x%d, x%d, %d", rd, rs1, word>>20&63)
			case 0x60000000:
				return fmt.Sprintf("rori x%d, x%d, %d", rd, rs1, word>>20&63)
			case 0x40000000:
				return fmt.Sprintf("srai x%d, x%d, %d", rd, rs1, word>>20&63)
			}
		}
	case 0x33, 0x3b:
		mnemonic := ""
		switch {
		case funct3 == 1 && funct7 == 0x30:
			mnemonic = "rol"
		case funct3 == 5 && funct7 == 0x30:
			mnemonic = "ror"
		case funct3 == 2 && funct7 == 0x10:
			mnemonic = "sh1add"
		case funct3 == 4 && funct7 == 0x10:
			mnemonic = "sh2add"
		case funct3 == 6 && funct7 == 0x10:
			mnemonic = "sh3add"
		case funct3 == 0 && funct7 == 0:
			mnemonic = "add"
		case funct3 == 0 && funct7 == 0x20:
			mnemonic = "sub"
		case funct3 == 3 && funct7 == 0:
			mnemonic = "sltu"
		case funct3 == 3 && funct7 == 1:
			mnemonic = "mulhu"
		case funct3 == 1 && funct7 == 1:
			mnemonic = "mulh"
		case funct3 == 1 && funct7 == 0:
			mnemonic = "sll"
		case funct3 == 5 && funct7 == 0:
			mnemonic = "srl"
		case funct3 == 5 && funct7 == 0x20:
			mnemonic = "sra"
		case funct3 == 0 && funct7 == 1:
			mnemonic = "mul"
		case funct3 >= 4 && funct7 == 1:
			mnemonic = []string{"div", "divu", "rem", "remu"}[funct3-4]
		case funct3 == 7 && funct7 == 0x20:
			mnemonic = "andn"
		case funct3 == 7 && funct7 == 0:
			mnemonic = "and"
		case funct3 == 6 && funct7 == 0:
			mnemonic = "or"
		case funct3 == 4 && funct7 == 0:
			mnemonic = "xor"
		}
		if mnemonic != "" {
			if word&0x7f == 0x3b {
				mnemonic += "w"
			}
			return fmt.Sprintf("%s x%d, x%d, x%d", mnemonic, rd, rs1, rs2)
		}
	case 0x53:
		if (funct7 == 0x10 || funct7 == 0x11) && funct3 <= 2 {
			width := "s"
			if funct7 == 0x11 {
				width = "d"
			}
			if rs1 == rs2 {
				name := []string{"fmv", "fneg", "fabs"}[funct3]
				return fmt.Sprintf("%s.%s f%d, f%d", name, width, rd, rs1)
			}
			name := []string{"fsgnj", "fsgnjn", "fsgnjx"}[funct3]
			return fmt.Sprintf("%s.%s f%d, f%d, f%d", name, width, rd, rs1, rs2)
		}
		if rs2 == 0 && funct3 == 0 {
			switch funct7 {
			case 0x78:
				return fmt.Sprintf("fmv.w.x f%d, x%d", rd, rs1)
			case 0x79:
				return fmt.Sprintf("fmv.d.x f%d, x%d", rd, rs1)
			case 0x70:
				return fmt.Sprintf("fmv.x.w x%d, f%d", rd, rs1)
			case 0x71:
				return fmt.Sprintf("fmv.x.d x%d, f%d", rd, rs1)
			}
		}
		if (funct7 == 0x20 && rs2 == 1 && funct3 == 7) || (funct7 == 0x21 && rs2 == 0 && funct3 == 0) {
			name := "fcvt.s.d"
			if funct7 == 0x21 {
				name = "fcvt.d.s"
			}
			return fmt.Sprintf("%s f%d, f%d", name, rd, rs1)
		}
		types := []string{"w", "wu", "l", "lu"}
		width := "s"
		if funct7&1 == 1 {
			width = "d"
		}
		switch {
		case (funct7 == 0x60 || funct7 == 0x61) && rs2 < 4:
			rounding := ""
			if funct3 == 1 {
				rounding = ", rtz"
			}
			return fmt.Sprintf("fcvt.%s.%s x%d, f%d%s", types[rs2], width, rd, rs1, rounding)
		case (funct7 == 0x68 || funct7 == 0x69) && rs2 < 4 && funct3 == 7:
			return fmt.Sprintf("fcvt.%s.%s f%d, x%d", width, types[rs2], rd, rs1)
		case (funct7 == 0x50 || funct7 == 0x51) && funct3 <= 2:
			name := []string{"fle", "flt", "feq"}[funct3]
			return fmt.Sprintf("%s.%s x%d, f%d, f%d", name, width, rd, rs1, rs2)
		}
		if funct3 == 7 {
			if (funct7 == 0x2c || funct7 == 0x2d) && rs2 == 0 {
				width := "s"
				if funct7 == 0x2d {
					width = "d"
				}
				return fmt.Sprintf("fsqrt.%s f%d, f%d", width, rd, rs1)
			}
			mnemonic := map[uint32]string{0: "fadd.s", 1: "fadd.d", 4: "fsub.s", 5: "fsub.d", 8: "fmul.s", 9: "fmul.d", 12: "fdiv.s", 13: "fdiv.d"}[funct7]
			if mnemonic != "" {
				return fmt.Sprintf("%s f%d, f%d, f%d", mnemonic, rd, rs1, rs2)
			}
		}
	case 0x03:
		switch funct3 {
		case 5:
			return fmt.Sprintf("lhu x%d, %d(x%d)", rd, immI, rs1)
		case 6:
			return fmt.Sprintf("lwu x%d, %d(x%d)", rd, immI, rs1)
		case 3:
			return fmt.Sprintf("ld x%d, %d(x%d)", rd, immI, rs1)
		case 4:
			return fmt.Sprintf("lbu x%d, %d(x%d)", rd, immI, rs1)
		}
	case 0x07:
		if funct3 == 2 {
			return fmt.Sprintf("flw f%d, %d(x%d)", rd, immI, rs1)
		}
		if funct3 == 3 {
			return fmt.Sprintf("fld f%d, %d(x%d)", rd, immI, rs1)
		}
	case 0x23, 0x27:
		imm := signBits((word>>25)<<5|word>>7&31, 12)
		if word&0x7f == 0x27 && funct3 == 3 {
			return fmt.Sprintf("fsd f%d, %d(x%d)", rs2, imm, rs1)
		}
		if word&0x7f == 0x23 {
			switch funct3 {
			case 1:
				return fmt.Sprintf("sh x%d, %d(x%d)", rs2, imm, rs1)
			case 2:
				return fmt.Sprintf("sw x%d, %d(x%d)", rs2, imm, rs1)
			case 0:
				return fmt.Sprintf("sb x%d, %d(x%d)", rs2, imm, rs1)
			case 3:
				return fmt.Sprintf("sd x%d, %d(x%d)", rs2, imm, rs1)
			}
		}
	case 0x63:
		mnemonic := map[uint32]string{0: "beq", 1: "bne", 4: "blt", 5: "bge", 6: "bltu", 7: "bgeu"}[funct3]
		if mnemonic != "" {
			offset := (word>>31&1)<<12 | (word>>7&1)<<11 | (word>>25&63)<<5 | (word>>8&15)<<1
			return fmt.Sprintf("%s x%d, x%d, 0x%x", mnemonic, rs1, rs2, int64(pc)+signBits(offset, 13))
		}
	case 0x6f:
		offset := (word>>31&1)<<20 | (word>>12&255)<<12 | (word>>20&1)<<11 | (word>>21&1023)<<1
		return fmt.Sprintf("jal x%d, 0x%x", rd, int64(pc)+signBits(offset, 21))
	case 0x67:
		if rd == 0 && rs1 == 1 && immI == 0 {
			return "ret"
		}
		if funct3 == 0 {
			return fmt.Sprintf("jalr x%d, %d(x%d)", rd, immI, rs1)
		}
	case 0x0f:
		switch word {
		case 0x0330000f:
			return "fence rw, rw"
		case 0x0000100f:
			return "fence.i"
		}
	case 0x73:
		switch word {
		case 0x00000073:
			return "ecall"
		case 0x00100073:
			return "ebreak"
		}
	}
	return fmt.Sprintf(".word 0x%08x", word)
}
