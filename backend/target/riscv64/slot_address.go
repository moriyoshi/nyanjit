package riscv64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
)

// slotAddressWords encodes OpSlotAddress, the address of spill slot slot as
// Spill addresses it: 8*(slot+1+homes)(SP) on a platform stack, 8*slot(X9)
// (the workspace) otherwise. A near offset is one ADDI; a far one is built
// in dst and added to the base, so no scratch register is written. It
// returns nil for an invalid form.
func slotAddressWords(dst backend.Register, slot int, stack abi.StackPractice, goHomes int) []uint32 {
	if !validGeneralRegisters(dst) || dst == 0 || !validSlot(slot, goHomes) || stack != abi.StackNoUse && stack != abi.StackPlatform {
		return nil
	}
	base, offset := uint32(9), slot*8
	if stack == abi.StackPlatform {
		base, offset = 2, (slot+1+goHomes)*8
	}
	if uint32(dst) == base {
		return nil
	}
	if offset <= 2047 {
		return []uint32{iWord(int64(offset), base, 0, uint32(dst), 0x13)}
	}
	words := ConstantWords(dst, uint64(offset))
	return append(words, rWord(0, uint32(dst), base, 0, uint32(dst), 0x33))
}

// SlotAddress implements OpSlotAddress with slotAddressWords. It writes only
// dst.
func (e *ByteAssembler) SlotAddress(dst backend.Register, slot int) {
	words := slotAddressWords(dst, slot, e.stack, e.goHomes)
	if !e.require(words != nil) {
		return
	}
	e.words = append(e.words, words...)
}
