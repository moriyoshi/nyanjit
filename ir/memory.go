package ir

import "github.com/moriyoshi/nyanjit/backend/op"

// ByteOrder specifies the representation of a memory word. It is
// op.ByteOrder.
type ByteOrder = op.ByteOrder

// The byte orders of op.ByteOrder, re-exported for IR code.
const (
	ByteOrderNative       = op.ByteOrderNative
	ByteOrderLittleEndian = op.ByteOrderLittleEndian
	ByteOrderBigEndian    = op.ByteOrderBigEndian
)

// NarrowMemoryBits returns the access width of a 16/32-bit memory opcode, or
// zero for other operations. Loads return Scalar(width); stores have no value.
func (i Instruction) NarrowMemoryBits() uint8 {
	switch i.Op {
	case OpLoad16, OpStore16:
		return 16
	case OpLoad32, OpStore32:
		return 32
	}
	return 0
}
