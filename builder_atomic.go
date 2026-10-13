package nyanjit

// AtomicLoad atomically loads a bits-wide (8, 16, 32 or 64) zero-extended value
// from naturally aligned address. Memory order is sequentially consistent.
func (b *Builder) AtomicLoad(address Value, bits uint8) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.AtomicLoad(address, bits))
}

// AtomicStore atomically stores value (8, 16, 32 or 64 bits) at address.
func (b *Builder) AtomicStore(address, value Value) {
	if b.ready() {
		b.okDo(b.inner.AtomicStore(address, value))
	}
}

// AtomicAdd, AtomicAnd, AtomicOr, AtomicXor and AtomicSwap atomically replace
// the value at address, of value's width, and return the old value.
func (b *Builder) AtomicAdd(address, value Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.AtomicAdd(address, value))
}

// AtomicAnd is AtomicAdd with bitwise and.
func (b *Builder) AtomicAnd(address, value Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.AtomicAnd(address, value))
}

// AtomicOr is AtomicAdd with bitwise or.
func (b *Builder) AtomicOr(address, value Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.AtomicOr(address, value))
}

// AtomicXor is AtomicAdd with bitwise exclusive or.
func (b *Builder) AtomicXor(address, value Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.AtomicXor(address, value))
}

// AtomicSwap stores value and returns the old value.
func (b *Builder) AtomicSwap(address, value Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.AtomicSwap(address, value))
}

// AtomicCompareSwap stores replacement if the value at address equals expected,
// and returns the old value; the swap happened exactly when it equals expected.
// expected and replacement have the same width.
func (b *Builder) AtomicCompareSwap(address, expected, replacement Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.AtomicCompareSwap(address, expected, replacement))
}

// Fence orders every earlier memory operation before every later one, as a
// sequentially consistent fence.
func (b *Builder) Fence() {
	if b.ready() {
		b.okDo(b.inner.Fence())
	}
}
