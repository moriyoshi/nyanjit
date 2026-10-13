package i386

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// FloatMemory uses scalar SSE2 loads/stores. Slots are native 4-byte words;
// binary64 spans two adjacent slots and needs no stronger alignment.
func (e *ByteAssembler) FloatMemory(i machine.Instruction) error {
	if !e.require(validFloatForm(i)) {
		return e.err
	}

	base := backend.Register(5)
	if e.stack == abi.StackPlatform {
		base = 4
	}
	prefix, op := byte(0xf2), byte(0x11)
	if i.Bits == 32 {
		prefix = 0xf3
	}
	if i.Op == machine.OpFloatReload {
		op = 0x10
	}
	e.code = appendBase32(append(e.code, prefix, 0x0f), op, backend.Register(backend.VectorNumber(i.A)), base, e.callArea+i.Slot*4)
	return nil
}
func (e *TextAssembler) FloatMemory(i machine.Instruction) error {
	if !e.require(validFloatForm(i)) {
		return e.err
	}

	base := backend.Register(5)
	if e.stack == abi.StackPlatform {
		base = 4
	}
	dst, src := fmt.Sprintf("[%s + %d]", name(base), e.callArea+i.Slot*4), fmt.Sprintf("xmm%d", backend.VectorNumber(i.A))
	if i.Op == machine.OpFloatReload {
		dst, src = src, dst
	}
	mnemonic := "movsd"
	if i.Bits == 32 {
		mnemonic = "movss"
	}
	e.record("%s %s, %s", mnemonic, dst, src)
	return e.ByteAssembler.FloatMemory(i)
}
