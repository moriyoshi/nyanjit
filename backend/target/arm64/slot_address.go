package arm64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
)

// slotAddressMax bounds the byte offset an OpSlotAddress forms: two ADD
// immediates, the first shifted by 12, reach just below 16 MiB.
const slotAddressMax = 1<<24 - 1

// slotAddressOffset returns the base register and byte offset of spill slot
// slot, as Spill addresses it: SP after the link register and the Go homes
// on a platform stack, X8 (the workspace) otherwise.
func slotAddressOffset(slot int, stack abi.StackPractice, goHomes int) (base uint32, offset int, ok bool) {
	if !validSlot(slot, goHomes) || stack != abi.StackNoUse && stack != abi.StackPlatform {
		return 0, 0, false
	}
	base = 8
	if stack == abi.StackPlatform {
		base, slot = 31, slot+1+goHomes
	}
	offset = slot * 8
	return base, offset, offset <= slotAddressMax
}

// slotAddressSize is the byte size of a valid OpSlotAddress, or zero.
func slotAddressSize(dst backend.Register, slot int, stack abi.StackPractice, goHomes int) int {
	_, offset, ok := slotAddressOffset(slot, stack, goHomes)
	if !ok || dst > 30 {
		return 0
	}
	if offset > 4095 && offset&4095 != 0 {
		return 8
	}
	return 4
}

// SlotAddress implements OpSlotAddress: X dst gets the address of spill slot
// slot, with ADD dst, SP (or X8), #offset and, above 4095 bytes, an ADD of the
// offset's high part shifted by 12 first. It writes no flags.
func (e *ByteAssembler) SlotAddress(dst backend.Register, slot int) {
	base, offset, ok := slotAddressOffset(slot, e.stack, e.goHomes)
	if !e.require(ok && dst <= 30) {
		return
	}
	add := func(d, n uint32, imm int, shift uint32) uint32 {
		return 0x91000000 | shift<<22 | uint32(imm)<<10 | n<<5 | d
	}
	if offset <= 4095 {
		e.words = append(e.words, add(uint32(dst), base, offset, 0))
		return
	}
	e.words = append(e.words, add(uint32(dst), base, offset>>12, 1))
	if offset&4095 != 0 {
		e.words = append(e.words, add(uint32(dst), uint32(dst), offset&4095, 0))
	}
}

// SlotAddress renders the sequence the byte encoder emits.
func (e *TextAssembler) SlotAddress(dst backend.Register, slot int) {
	if !e.valid(func(enc *ByteAssembler) { enc.SlotAddress(dst, slot) }) {
		return
	}
	base, offset, _ := slotAddressOffset(slot, e.stack, e.goHomes)
	name := "SP"
	if base == 8 {
		name = "X8"
	}
	if offset <= 4095 {
		e.line("add X%d, %s, #%d", dst, name, offset)
		return
	}
	e.line("add X%d, %s, #%d, lsl #12", dst, name, offset>>12)
	if offset&4095 != 0 {
		e.line("add X%d, X%d, #%d", dst, dst, offset&4095)
	}
}
