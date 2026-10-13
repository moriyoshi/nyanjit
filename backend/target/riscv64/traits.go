package riscv64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// ReturnAddress identifies the architectural resource holding the return PC.
func (Policy) ReturnAddress() machine.ReturnAddress {
	return machine.ReturnAddress{Resource: machine.Resource{Class: machine.ResourceGeneralRegister, Number: 1}}
}

// NativeByteOrder reports the target's memory order, independent of the host.
func (Policy) NativeByteOrder() op.ByteOrder         { return op.ByteOrderLittleEndian }
func (*ByteAssembler) NativeByteOrder() op.ByteOrder { return op.ByteOrderLittleEndian }
func (*TextAssembler) NativeByteOrder() op.ByteOrder { return op.ByteOrderLittleEndian }

// Integer128Result returns ABI registers in logical low/high word order.
func (p Policy) Integer128Result() ([2]backend.Register, bool) {
	return [2]backend.Register{10, 11}, p.ABIFlavor() == abi.C
}

// ManagedFrameLimit leaves 160 bytes of Go's 800-byte NOSPLIT reserve for
// the ABI0 wrapper and morestack. RISC-V morestack has no user-stack frame;
// outgoing register-argument homes are included in the generated frame.
// Keep the registration bound in goregister in sync.
func (Policy) ManagedFrameLimit() int { return 640 }

// Compare consumes a comparison whose scalar operands have already been
// normalized to the full comparison width. These backends' legacy comparison
// forms support 64-bit operands; narrower forms need an explicit implementation.
func (e *ByteAssembler) Compare(ins machine.Instruction) error {
	if !e.require((validInstructionRegisters(ins))) {
		return e.err
	}

	if ins.Bits != 0 && ins.Bits != 64 {
		return machine.ErrUnsupportedOperation
	}
	if ins.Op == machine.OpCompareImmediate {
		e.CompareImmediate(ins.A, int64(ins.Imm))
	} else if ins.Op == machine.OpCompareRegister {
		e.CompareRegister(ins.A, ins.B)
	} else {
		return machine.ErrUnsupportedOperation
	}
	return nil
}
func (e *TextAssembler) Compare(ins machine.Instruction) error {
	if ins.Bits != 0 && ins.Bits != 64 {
		return machine.ErrUnsupportedOperation
	}
	if ins.Op == machine.OpCompareImmediate {
		e.CompareImmediate(ins.A, int64(ins.Imm))
	} else if ins.Op == machine.OpCompareRegister {
		e.CompareRegister(ins.A, ins.B)
	} else {
		return machine.ErrUnsupportedOperation
	}
	return nil
}
