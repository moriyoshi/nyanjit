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

// LoadBits reads a little-endian unsigned integer and gives it the requested
// virtual width. Widths up to 32 bits permit arbitrary byte alignment in
// ordinary user memory, without atomicity guarantees. Wider accesses require
// target alignment. Native describes the address width, not the byte order.
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
		return b.loadNarrow(address, bits, false)
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
// order. Widths up to 32 bits permit arbitrary byte alignment in ordinary user
// memory, without atomicity guarantees. Wider accesses require target alignment.
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
		return b.storeNarrow(address, value, value.Type().LaneBits, false)
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

// Load16 reads a little-endian unsigned integer as Scalar(16). The address
// may have native or 64-bit width and any byte alignment. Ordinary user memory
// is required; this API provides neither atomic nor device-memory semantics.
func (b *Builder) Load16(address Value) (Value, error) { return b.loadNarrow(address, 16, false) }

// Load32 is Load16's 32-bit counterpart and returns Scalar(32).
func (b *Builder) Load32(address Value) (Value, error) { return b.loadNarrow(address, 32, false) }

// Store16 writes the low 16 bits of a 16- or 64-bit scalar, leaving adjacent
// bytes untouched. Addresses have native or 64-bit width and any byte alignment.
// Like Load16, it accesses ordinary user memory without atomicity guarantees.
func (b *Builder) Store16(address, value Value) error {
	return b.storeNarrow(address, value, 16, false)
}

// Store32 is Store16's 32-bit counterpart, accepting a 32- or 64-bit scalar.
func (b *Builder) Store32(address, value Value) error {
	return b.storeNarrow(address, value, 32, false)
}

func (b *Builder) loadNarrow(address Value, bits uint8, aligned bool) (Value, error) {
	if err := requireScalars("Load", address); err != nil {
		return Value{}, err
	}
	var err error
	address, err = b.materialize(address)
	if err != nil {
		return Value{}, err
	}
	if !b.valid(address) || (address.Bits() != NativeBits && address.Bits() != 64) {
		return Value{}, ErrInvalidFunction
	}
	op := opLoad8
	if bits == 16 {
		op = opLoad16
	}
	if bits == 32 {
		op = opLoad32
	}
	return b.append(instruction{Op: op, A: address.id, Bits: bits, ByteOrder: ir.ByteOrderLittleEndian, Aligned: aligned && bits != 8}), nil
}
func (b *Builder) storeNarrow(address, value Value, bits uint8, aligned bool) error {
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
	if !b.valid(address) || !b.valid(value) || (address.Bits() != NativeBits && address.Bits() != 64) || (value.Bits() != uint16(bits) && value.Bits() != 64) {
		return fmt.Errorf("store %d bits: address width %d (want native or 64), data width %d (want %d or 64): %w", bits, address.Bits(), value.Bits(), bits, ErrInvalidFunction)
	}
	op := opStore8
	if bits == 16 {
		op = opStore16
	}
	if bits == 32 {
		op = opStore32
	}
	b.append(instruction{Op: op, A: address.id, B: value.id, Bits: bits, ByteOrder: ir.ByteOrderLittleEndian, Aligned: aligned && bits != 8})
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

// IndexedAddress branches to outOfBounds unless index is unsigned-less-than count.
// It resumes in a fresh in-bounds block and returns base + index*scale there.
// Values defined before the call dominate that block and stay usable there.
// The returned address and arithmetic aliases cannot
// be stored in Vars or passed across control edges; check again in each block.
// Raw addresses remain subject to host lifetime rules.
func (b *Builder) IndexedAddress(base, index, count Value, scale uint64, outOfBounds Label) (Value, error) {
	if !b.canAppend() {
		return Value{}, ErrInvalidFunction
	}
	outOfRange, ok := b.localLabel(outOfBounds)
	if !ok || b.control.privateLabels[outOfRange] {
		return Value{}, b.labelError("IndexedAddress", outOfBounds)
	}
	var err error
	base, err = b.materialize(base)
	if err != nil {
		return Value{}, err
	}
	index, err = b.materialize(index)
	if err != nil {
		return Value{}, err
	}
	count, err = b.materialize(count)
	if err != nil {
		return Value{}, err
	}
	if !b.valid(base) || !b.valid(index) || !b.valid(count) {
		return Value{}, ErrInvalidFunction
	}
	if err := requireScalars("IndexedAddress", base, index, count); err != nil {
		return Value{}, err
	}
	if base.Bits() != index.Bits() || index.Bits() != count.Bits() || base.Bits() != 64 && base.Bits() != uint16(NativeBits) {
		return Value{}, ErrInvalidFunction
	}
	predicate, err := b.Compare(index, ir.BranchBelow, count)
	if err != nil {
		return Value{}, err
	}
	// The continuation receives base and index on its one edge, so that a
	// hardened bounds check can clamp the index it passes.
	valid := b.newLabel()
	if _, err := b.branch(predicate, valid, []Value{base, index}, outOfRange, nil, true); err != nil {
		return Value{}, err
	}
	params, err := b.bindParamsBits(valid, []uint8{uint8(base.Bits()), uint8(index.Bits())})
	if err != nil {
		return Value{}, err
	}
	if b.control.privateLabels == nil {
		b.control.privateLabels = make(map[ir.Label]bool)
	}
	b.control.privateLabels[valid] = true
	address, err := b.addScaled(params[0], params[1], scale)
	if err == nil {
		b.markCheckedAddress(address)
	}
	return address, err
}
