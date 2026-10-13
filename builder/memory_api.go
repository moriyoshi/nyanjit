package builder

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/ir"
)

func (b *Builder) normalizeAddress(address Value) (Value, error) {
	if err := requireScalars("Address", address); err != nil {
		return Value{}, err
	}
	var err error
	address, err = b.materialize(address)
	if err != nil {
		return Value{}, err
	}
	if !b.valid(address) {
		return Value{}, ErrInvalidFunction
	}
	switch address.Type().LaneBits {
	case 32:
		return b.CastUnsigned(address, 64)
	case 64:
		return address, nil
	default:
		return Value{}, ErrInvalidFunction
	}
}

func (b *Builder) nativeByteAddress(address Value, offset int) (Value, error) {
	if offset == 0 {
		return address, nil
	}
	delta, err := b.ConstNative(uintptr(offset))
	if err != nil {
		return Value{}, err
	}
	return b.Add(address, delta)
}

func (b *Builder) loadNativeBits(address Value, bits uint8) (Value, error) {
	var result Value
	for offset := 0; offset < int(bits)/8; offset++ {
		at, err := b.nativeByteAddress(address, offset)
		if err != nil {
			return Value{}, err
		}
		part := b.append(instruction{Op: opLoad8, A: at.id, Bits: 8})
		if bits == 8 {
			return part, nil
		}
		part, err = b.CastNative(part)
		if err != nil {
			return Value{}, err
		}
		if offset != 0 {
			part, err = b.ShiftLeft(part, uint8(8*offset))
			if err != nil {
				return Value{}, err
			}
			result, err = b.Or(result, part)
			if err != nil {
				return Value{}, err
			}
		} else {
			result = part
		}
	}
	return b.CastUnsigned(result, bits)
}

func (b *Builder) storeNativeBits(address, value Value) error {
	bits := value.Type().LaneBits
	if bits == 8 {
		b.append(instruction{Op: opStore8, A: address.id, B: value.id, Bits: 8})
		return nil
	}
	wide, err := b.CastNative(value)
	if err != nil {
		return err
	}
	for offset := 0; offset < int(bits)/8; offset++ {
		at, err := b.nativeByteAddress(address, offset)
		if err != nil {
			return err
		}
		part := wide
		if offset != 0 {
			part, err = b.ShiftRight(wide, uint8(8*offset))
			if err != nil {
				return err
			}
		}
		part, err = b.CastUnsigned(part, 8)
		if err != nil {
			return err
		}
		b.append(instruction{Op: opStore8, A: at.id, B: part.id, Bits: 8})
	}
	return nil
}

// LoadBits reads a little-endian unsigned integer and gives it the requested
// virtual width. Native describes the address width, not the word's byte order.
func (b *Builder) LoadBits(address Value, bits uint8) (Value, error) {
	if err := requireScalars("LoadBits", address); err != nil {
		return Value{}, err
	}
	var err error
	address, err = b.materialize(address)
	if err != nil {
		return Value{}, err
	}
	if !validScalarBits(bits) && bits != 128 {
		return Value{}, ErrInvalidFunction
	}
	if bits <= 32 && b.valid(address) && address.Type().LaneBits == NativeBits {
		return b.loadNativeBits(address, bits)
	}
	if bits == 64 && b.valid(address) && address.Type().LaneBits == NativeBits {
		return b.load64Order(address, ir.ByteOrderLittleEndian)
	}
	address, err = b.normalizeAddress(address)
	if err != nil {
		return Value{}, err
	}
	if bits == 128 {
		return loadWide(b, address)
	}
	if bits == 64 {
		return b.load64Order(address, ir.ByteOrderLittleEndian)
	}
	var value Value
	switch bits {
	case 8:
		value, err = b.Load8(address)
	case 16:
		value, err = b.Load16(address)
	case 32:
		value, err = b.Load32(address)
	}
	if err != nil {
		return Value{}, err
	}
	return b.CastUnsigned(value, bits)
}

// StoreBits writes a width-matched integer to native memory in little-endian
// order. Byte-based stores keep native-width addresses.
func (b *Builder) StoreBits(address, value Value) error {
	if err := requireScalars("StoreBits", address, value); err != nil {
		return err
	}
	var err error
	address, err = b.materialize(address)
	if err != nil {
		return err
	}
	value, err = b.materialize(value)
	if err != nil {
		return err
	}
	if b.valid(address) && b.valid(value) && address.Type().LaneBits == NativeBits && value.Type().LaneBits <= 32 {
		return b.storeNativeBits(address, value)
	}
	if b.valid(address) && b.valid(value) && address.Type().LaneBits == NativeBits && value.Type().LaneBits == 64 {
		return b.store64Order(address, value, ir.ByteOrderLittleEndian)
	}
	address, err = b.normalizeAddress(address)
	if err != nil {
		return err
	}
	if value.composite != nil {
		if !b.validComposite(value) || value.Type().LaneBits != 128 {
			return ErrInvalidFunction
		}
		return storeWide(b, address, value)
	}
	if !b.valid(value) {
		return ErrInvalidFunction
	}
	bits := value.Type().LaneBits
	if bits == 64 {
		return b.store64Order(address, value, ir.ByteOrderLittleEndian)
	}
	wide, err := b.CastUnsigned(value, 64)
	if err != nil {
		return err
	}
	switch bits {
	case 8:
		return b.Store8(address, wide)
	case 16:
		return b.Store16(address, wide)
	case 32:
		return b.Store32(address, wide)
	default:
		return ErrInvalidFunction
	}
}

// Load16 reads two little-endian bytes from a native address and zero-extends.
func (b *Builder) Load16(address Value) (Value, error) { return b.loadBytes(address, 2) }

// Load32 reads four little-endian bytes from a native address and zero-extends.
func (b *Builder) Load32(address Value) (Value, error) { return b.loadBytes(address, 4) }

func (b *Builder) loadBytes(address Value, count int) (Value, error) {
	if err := requireScalars("Load", address); err != nil {
		return Value{}, err
	}
	var err error
	address, err = b.materialize(address)
	if err != nil {
		return Value{}, err
	}
	if !b.valid(address) {
		return Value{}, ErrInvalidFunction
	}
	result, err := b.Load8(address)
	if err != nil {
		return Value{}, err
	}
	for i := 1; i < count; i++ {
		offset, err := b.Const(uint64(i))
		if err != nil {
			return Value{}, err
		}
		at, err := b.Add(address, offset)
		if err != nil {
			return Value{}, err
		}
		part, err := b.Load8(at)
		if err != nil {
			return Value{}, err
		}
		result, err = b.addScaled(result, part, uint64(1)<<(8*i))
		if err != nil {
			return Value{}, err
		}
	}
	return result, nil
}

// Store16 writes the low two bytes of a 16- or 64-bit value to a native
// address in little-endian order.
func (b *Builder) Store16(address, value Value) error { return b.storeBytes(address, value, 2) }

// Store32 writes the low four bytes of a 32- or 64-bit value to a native
// address in little-endian order.
func (b *Builder) Store32(address, value Value) error { return b.storeBytes(address, value, 4) }

func (b *Builder) storeBytes(address, value Value, count int) error {
	if err := requireScalars("Store", address, value); err != nil {
		return err
	}
	var err error
	address, err = b.materialize(address)
	if err != nil {
		return err
	}
	value, err = b.materialize(value)
	if err != nil {
		return err
	}
	if !b.valid(address) || !b.valid(value) ||
		address.Type().LaneBits != 64 && address.Type().LaneBits != NativeBits ||
		value.Type().LaneBits != 64 && value.Type().LaneBits != uint8(count*8) {
		return fmt.Errorf("store %d bytes: address value %d has width %d (want native width), data value %d has width %d (want %d or 64): %w",
			count, address.id, address.Type().LaneBits, value.id, value.Type().LaneBits, count*8, ErrInvalidFunction)
	}
	if address.Type().LaneBits == NativeBits {
		if value.Type().LaneBits == 64 {
			value, err = b.CastUnsigned(value, uint8(count*8))
			if err != nil {
				return err
			}
		}
		return b.storeNativeBits(address, value)
	}
	for i := 0; i < count; i++ {
		at, part := address, value
		if i != 0 {
			offset, err := b.Const(uint64(i))
			if err != nil {
				return err
			}
			at, err = b.Add(address, offset)
			if err != nil {
				return err
			}
			part, err = b.ShiftRight(value, uint8(8*i))
			if err != nil {
				return err
			}
		}
		if err := b.Store8(at, part); err != nil {
			return err
		}
	}
	return nil
}

// SignExtend interprets the low 8, 16, or 32 bits as a signed integer and
// returns its uint64 two's-complement representation.
func (b *Builder) SignExtend(value Value, width uint8) (Value, error) {
	if err := requireScalars("SignExtend", value); err != nil {
		return Value{}, err
	}
	if value.expression != nil {
		if !b.validExpression(value) || width != 8 && width != 16 && width != 32 {
			return Value{}, ErrInvalidFunction
		}
		return value.SignExtend(width), nil
	}
	var err error
	value, err = b.materialize(value)
	if err != nil {
		return Value{}, err
	}
	if !b.valid(value) || width != 8 && width != 16 && width != 32 {
		return Value{}, ErrInvalidFunction
	}
	return b.append(instruction{Op: opSignExtend, A: value.id, Imm: uint64(width)}), nil
}

// IndexedAddress returns an address and the unsigned bounds predicate for its
// index. Branch on the predicate and pass the address to the valid edge with
// BranchArgs before using it for a memory access.
func (b *Builder) IndexedAddress(base, index, count Value, scale uint64) (Value, Predicate, error) {
	var err error
	base, err = b.materialize(base)
	if err != nil {
		return Value{}, Predicate{}, err
	}
	index, err = b.materialize(index)
	if err != nil {
		return Value{}, Predicate{}, err
	}
	count, err = b.materialize(count)
	if err != nil {
		return Value{}, Predicate{}, err
	}
	if !b.valid(base) || !b.valid(index) || !b.valid(count) {
		return Value{}, Predicate{}, ErrInvalidFunction
	}
	address, err := b.addScaled(base, index, scale)
	if err != nil {
		return Value{}, Predicate{}, err
	}
	predicate, err := b.Compare(index, BranchBelow, count)
	if err != nil {
		return Value{}, Predicate{}, err
	}
	return address, predicate, nil
}
