package ir

// ByteOrder specifies the representation of a memory word. It does not change
// the logical low/high ordering of a multiword Value or its ABI locations.
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
