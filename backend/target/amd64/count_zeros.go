package amd64

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func (Policy) CanCountZeros(op op.Opcode, w uint8) bool {
	return w == 8 || w == 16 || w == 32 || w == 64
}
func countZerosPlan(operation op.Opcode, w uint8, d, s backend.Register, p abi.ResolvedCPU) []shiftByInstruction {
	var out []shiftByInstruction
	emit := func(b []byte, text string) { out = append(out, shiftByInstruction{b, text}) }
	rex := byte(0x48) | byte(d>>3)<<2 | byte(s>>3)
	mod := byte(0xc0) | byte(d&7)<<3 | byte(s&7)
	leading := operation == op.OpLeadingZeros
	if leading && p.Has("lzcnt") {
		emit([]byte{0xf3, rex, 0x0f, 0xbd, mod}, fmt.Sprintf("lzcnt %s, %s", amd64Name(d), amd64Name(s)))
		if w < 64 {
			emit([]byte{0x48 | byte(d>>3), 0x83, 0xe8 | byte(d&7), 64 - w}, fmt.Sprintf("sub %s, %d", amd64Name(d), 64-w))
		}
		return out
	}
	if !leading && p.Has("bmi1") && w >= 32 {
		name := amd64Name
		if w == 32 {
			rex &^= 8
			name = func(r backend.Register) string { return amd64RegisterNames32[r] }
		}
		emit([]byte{0xf3, rex, 0x0f, 0xbc, mod}, fmt.Sprintf("tzcnt %s, %s", name(d), name(s)))
		return out
	}
	opcode := byte(0xbc)
	mnemonic := "bsf"
	if leading {
		opcode = 0xbd
		mnemonic = "bsr"
	}
	emit([]byte{rex, 0x0f, opcode, mod}, fmt.Sprintf("%s %s, %s", mnemonic, amd64Name(d), amd64Name(s)))
	if leading {
		emit([]byte{0x48 | byte(d>>3), 0x83, 0xf0 | byte(d&7), w - 1}, fmt.Sprintf("xor %s, %d", amd64Name(d), w-1))
		emit(appendTestReg(nil, uint8(s)), "test "+amd64Name(s)+", "+amd64Name(s))
	}
	emit(appendMovImm64(nil, 11, uint64(w)), fmt.Sprintf("mov R11D, %d", w))
	emit([]byte{0x49 | byte(d>>3)<<2, 0x0f, 0x44, 0xc0 | byte(d&7)<<3 | 3}, "cmovz "+amd64Name(d)+", R11")
	return out
}
func (e *ByteAssembler) CountZeros(operation op.Opcode, w uint8, d, s backend.Register) {
	if !e.require((validGeneralRegisters(d, s)) && (validWidth(w)) && (operation == op.OpLeadingZeros || operation == op.OpTrailingZeros)) {
		return
	}

	for _, i := range countZerosPlan(operation, w, d, s, e.profile) {
		e.code = append(e.code, i.code...)
	}
}
func (e *TextAssembler) CountZeros(op op.Opcode, w uint8, d, s backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.CountZeros(op, w, d, s) }) {
		return
	}
	for _, i := range countZerosPlan(op, w, d, s, e.profile) {
		e.line(len(i.code), "%s", i.text)
	}
}
