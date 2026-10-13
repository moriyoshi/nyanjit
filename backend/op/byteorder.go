package op

// ByteOrder specifies the representation of a memory word. It does not change
// the logical low/high ordering of a multiword Value or its ABI locations.
// Explicit big-endian order is supported for 8/16/32-bit accesses. Load64 and
// Store64 accept Native or LittleEndian only; all supported targets are little
// endian. Validation rejects unsupported 64-bit orders before lowering.
type ByteOrder uint8

const (
	// ByteOrderNative uses the emitting target's native memory representation.
	ByteOrderNative ByteOrder = iota
	ByteOrderLittleEndian
	ByteOrderBigEndian
)

// Valid reports whether order is a recognized memory representation.
func (order ByteOrder) Valid() bool { return order <= ByteOrderBigEndian }

// Resolve replaces Native with the supplied target order. The caller must
// supply LittleEndian or BigEndian rather than the host's order.
func (order ByteOrder) Resolve(native ByteOrder) ByteOrder {
	if order == ByteOrderNative {
		return native
	}
	return order
}
