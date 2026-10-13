package riscv64

import (
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

func countZerosWords(op ir.Opcode, w uint8, d, s machine.Register) []uint32 {
	opcode := uint32(0x13)
	hardware := uint8(64)
	if w <= 32 {
		opcode = 0x1b
		hardware = 32
	}
	imm := int64(0x600)
	if op == ir.OpTrailingZeros {
		imm = 0x601
	}
	words := []uint32{iWord(imm, uint32(s), 1, uint32(d), opcode)}
	if op == ir.OpLeadingZeros && w < hardware {
		words = append(words, iWord(-int64(hardware-w), uint32(d), 0, uint32(d), 0x13))
	}
	return words
}
func (e *ByteAssembler) CountZeros(op ir.Opcode, w uint8, d, s machine.Register) {
	e.words = append(e.words, countZerosWords(op, w, d, s)...)
}
