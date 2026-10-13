package riscv64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func countZerosWords(operation op.Opcode, w uint8, d, s backend.Register) []uint32 {
	opcode := uint32(0x13)
	hardware := uint8(64)
	if w <= 32 {
		opcode = 0x1b
		hardware = 32
	}
	imm := int64(0x600)
	if operation == op.OpTrailingZeros {
		imm = 0x601
	}
	words := []uint32{iWord(imm, uint32(s), 1, uint32(d), opcode)}
	if operation == op.OpLeadingZeros && w < hardware {
		words = append(words, iWord(-int64(hardware-w), uint32(d), 0, uint32(d), 0x13))
	}
	return words
}
func (e *ByteAssembler) CountZeros(operation op.Opcode, w uint8, d, s backend.Register) {
	if !e.require((validGeneralRegisters(d, s)) && (validWidth(w)) && (operation == op.OpLeadingZeros || operation == op.OpTrailingZeros)) {
		return
	}

	e.words = append(e.words, countZerosWords(operation, w, d, s)...)
}
