package loong64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// SlotAddress implements OpSlotAddress: dst gets the address of spill slot
// slot, as Spill addresses it (8*(slot+1+homes)(sp), or 8*slot(r23) without a
// platform stack). A near offset is one addi.d; a far one is built in dst
// and added to the base, so no scratch register is written.
func (e *ByteAssembler) SlotAddress(dst backend.Register, slot int) {
	if !e.require(validSlot(slot, e.goHomes) && validGeneralRegisters(dst)) {
		return
	}
	base, offset := e.spillAddress(slot)
	if !e.require(dst != 0 && dst != 3 && dst != base) {
		return
	}
	if offset <= 2047 {
		e.ri(0x02c00000, "addi.d", dst, base, int64(offset))
		return
	}
	e.Constant(dst, offset)
	e.Binary(op.OpAdd, 64, dst, base, dst)
}
