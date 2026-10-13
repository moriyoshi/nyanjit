package amd64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
)

// slotAddressSize is the byte size of a valid OpSlotAddress, or zero. The
// address is formed as Spill addresses the slot: RSP-relative above the Go
// homes on a platform stack, R10-relative (the workspace) otherwise.
func slotAddressSize(dst backend.Register, slot int, stack abi.StackPractice, goHomes int) int {
	if !validGeneralRegisters(dst) || dst == 4 || !validSlot(slot, goHomes) {
		return 0
	}
	if stack == abi.StackPlatform {
		return 8 // RSP base needs a SIB byte.
	}
	return 7
}

// SlotAddress implements OpSlotAddress: LEA dst, [RSP + 8*(slot+homes)], or
// [R10 + 8*slot] without a platform stack. It writes no flags.
func (e *ByteAssembler) SlotAddress(dst backend.Register, slot int) {
	if !e.require(slotAddressSize(dst, slot, e.stack, e.goHomes) != 0) {
		return
	}
	if e.stack == abi.StackPlatform {
		e.code = append(e.code, 0x48|byte(dst>>3&1)<<2, 0x8D, 0x84|byte(dst&7)<<3, 0x24)
		e.code = appendDisp32(e.code, uint32((slot+e.goHomes)*8))
		return
	}
	e.code = appendREX(e.code, uint8(dst), 10)
	e.code = append(e.code, 0x8D, 0x82|byte(dst&7)<<3)
	e.code = appendDisp32(e.code, uint32(slot*8))
}

// SlotAddress renders the LEA the byte encoder emits.
func (e *TextAssembler) SlotAddress(dst backend.Register, slot int) {
	if !e.valid(func(enc *ByteAssembler) { enc.SlotAddress(dst, slot) }) {
		return
	}
	base := backend.Register(10)
	if e.stack == abi.StackPlatform {
		base = 4
		slot += e.goHomes
	}
	e.line(slotAddressSize(dst, 0, e.stack, 0), "lea %s, [%s + %d]", amd64Name(dst), amd64Name(base), slot*8)
}
