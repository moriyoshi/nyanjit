package i386

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
)

// slotAddressOperands returns the base register and displacement of spill
// slot slot, as Spill addresses it, and whether dst may receive its address.
func (e *ByteAssembler) slotAddressOperands(dst backend.Register, slot int) (backend.Register, int, bool) {
	base := backend.Register(5) // EBP is the workspace pointer without SP.
	if e.stack == abi.StackPlatform {
		base = 4
	}
	ok := validGeneralRegisters(dst) && dst != 4 && slot >= 0 && slot <= (1<<31-1-e.callArea)/4
	return base, e.callArea + slot*4, ok
}

// SlotAddress implements OpSlotAddress: LEA dst, [ESP + callArea + 4*slot],
// or [EBP + 4*slot] without a platform stack. It writes no flags.
func (e *ByteAssembler) SlotAddress(dst backend.Register, slot int) {
	base, disp, ok := e.slotAddressOperands(dst, slot)
	if !e.require(ok) {
		return
	}
	e.code = appendBase32(e.code, 0x8d, dst, base, disp)
}

// SlotAddress renders the LEA the byte encoder emits.
func (e *TextAssembler) SlotAddress(dst backend.Register, slot int) {
	base, disp, ok := e.slotAddressOperands(dst, slot)
	if !e.require(ok) {
		return
	}
	e.record("lea %s, %s", name(dst), offset(base, uint64(disp)))
	e.ByteAssembler.SlotAddress(dst, slot)
}
