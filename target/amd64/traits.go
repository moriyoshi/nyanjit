package amd64

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

// ReturnAddress identifies the architectural resource holding the return PC.
func (Policy) ReturnAddress() machine.ReturnAddress {
	return machine.ReturnAddress{Resource: machine.Resource{}}
}

// NativeByteOrder reports the target's memory order, independent of the host.
func (Policy) NativeByteOrder() ir.ByteOrder         { return ir.ByteOrderLittleEndian }
func (*ByteAssembler) NativeByteOrder() ir.ByteOrder { return ir.ByteOrderLittleEndian }
func (*TextAssembler) NativeByteOrder() ir.ByteOrder { return ir.ByteOrderLittleEndian }

// Integer128Result returns ABI registers in logical low/high word order.
func (p Policy) Integer128Result() ([2]machine.Register, bool) {
	return [2]machine.Register{0, 2}, p.ABIFlavor() == abi.C
}

// ManagedFrameLimit bounds a generated frame within Go's 800-byte nosplit
// reserve, leaving room for the execution gateway and call linkage.
func (Policy) ManagedFrameLimit() int { return 640 }

// Compare consumes a comparison whose scalar operands have already been
// normalized to the full comparison width. These backends' legacy comparison
// forms support 64-bit operands; narrower forms need an explicit implementation.
func (e *ByteAssembler) Compare(ins machine.Instruction) error {
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
