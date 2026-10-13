package builder

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/ir"
)

// atomicAddress materializes an atomic operation's address and operands. The
// address has native or 64-bit width, as for plain memory; operands are
// scalars of one width (8, 16, 32 or 64 bits).
func (b *Builder) atomicOperands(name string, address Value, operands ...Value) (Value, []Value, error) {
	if err := requireScalars(name, append([]Value{address}, operands...)...); err != nil {
		return Value{}, nil, err
	}
	address, err := b.materialize(address)
	if err != nil {
		return Value{}, nil, err
	}
	operands, err = b.materializeValues(operands)
	if err != nil {
		return Value{}, nil, err
	}
	if !b.valid(address) || address.Bits() != NativeBits && address.Bits() != 64 {
		return Value{}, nil, fmt.Errorf("%s: address value %d has width %d (want native or 64): %w", name, address.id, address.Bits(), ErrInvalidFunction)
	}
	for _, v := range operands {
		if !b.valid(v) || !validScalarBits(v.Bits()) || v.Bits() != operands[0].Bits() {
			return Value{}, nil, fmt.Errorf("%s: operand value %d has width %d (want 8, 16, 32 or 64, all alike): %w", name, v.id, v.Bits(), ErrInvalidFunction)
		}
	}
	return address, operands, nil
}

// appendAtomic emits an atomic operation. Like a call, it may change memory
// that indexed Data views were derived from, so it expires them.
func (b *Builder) appendAtomic(operation instruction) Value {
	b.dataEpoch++
	operation.Imm = uint64(ir.MemoryOrderSequential)
	return b.append(operation)
}

// AtomicLoad atomically loads a bits-wide (8, 16, 32 or 64) zero-extended value
// from naturally aligned address. Memory order is sequentially consistent.
func (b *Builder) AtomicLoad(address Value, bits uint8) (Value, error) {
	address, _, err := b.atomicOperands("AtomicLoad", address)
	if err != nil {
		return Value{}, err
	}
	if !validScalarBits(bits) {
		return Value{}, fmt.Errorf("AtomicLoad: width %d is not 8, 16, 32 or 64: %w", bits, ErrInvalidFunction)
	}
	return b.appendAtomic(instruction{Op: ir.OpAtomicLoad, A: address.id, Bits: bits}), nil
}

// AtomicStore atomically stores value (8, 16, 32 or 64 bits) at address.
func (b *Builder) AtomicStore(address, value Value) error {
	address, operands, err := b.atomicOperands("AtomicStore", address, value)
	if err != nil {
		return err
	}
	b.appendAtomic(instruction{Op: ir.OpAtomicStore, A: address.id, B: operands[0].id, Bits: uint8(operands[0].Bits())})
	return nil
}

// atomicUpdate emits a read-modify-write of value's width.
func (b *Builder) atomicUpdate(name string, operation ir.Opcode, address, value Value) (Value, error) {
	address, operands, err := b.atomicOperands(name, address, value)
	if err != nil {
		return Value{}, err
	}
	return b.appendAtomic(instruction{Op: operation, A: address.id, B: operands[0].id, Bits: uint8(operands[0].Bits())}), nil
}

// AtomicAdd, AtomicAnd, AtomicOr, AtomicXor and AtomicSwap atomically replace
// the value at address, of value's width, and return the old value.
func (b *Builder) AtomicAdd(address, value Value) (Value, error) {
	return b.atomicUpdate("AtomicAdd", ir.OpAtomicAdd, address, value)
}

// AtomicAnd is AtomicAdd with bitwise and.
func (b *Builder) AtomicAnd(address, value Value) (Value, error) {
	return b.atomicUpdate("AtomicAnd", ir.OpAtomicAnd, address, value)
}

// AtomicOr is AtomicAdd with bitwise or.
func (b *Builder) AtomicOr(address, value Value) (Value, error) {
	return b.atomicUpdate("AtomicOr", ir.OpAtomicOr, address, value)
}

// AtomicXor is AtomicAdd with bitwise exclusive or.
func (b *Builder) AtomicXor(address, value Value) (Value, error) {
	return b.atomicUpdate("AtomicXor", ir.OpAtomicXor, address, value)
}

// AtomicSwap stores value and returns the old value.
func (b *Builder) AtomicSwap(address, value Value) (Value, error) {
	return b.atomicUpdate("AtomicSwap", ir.OpAtomicSwap, address, value)
}

// AtomicCompareSwap stores replacement if the value at address equals expected,
// and returns the old value; the swap happened exactly when it equals expected.
// expected and replacement have the same width.
func (b *Builder) AtomicCompareSwap(address, expected, replacement Value) (Value, error) {
	address, operands, err := b.atomicOperands("AtomicCompareSwap", address, expected, replacement)
	if err != nil {
		return Value{}, err
	}
	return b.appendAtomic(instruction{Op: ir.OpAtomicCompareSwap, A: address.id, B: operands[0].id, C: operands[1].id, Bits: uint8(operands[0].Bits())}), nil
}

// Fence orders every earlier memory operation before every later one, as a
// sequentially consistent fence.
func (b *Builder) Fence() error {
	if b == nil || !b.canAppend() {
		return ErrInvalidFunction
	}
	b.appendAtomic(instruction{Op: ir.OpFence})
	return nil
}
