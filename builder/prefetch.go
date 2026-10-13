package builder

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/ir"
)

// Prefetch hints that address will soon be read (ir.PrefetchRead) or written
// (ir.PrefetchWrite). It is only a hint: it never faults, even for an invalid
// address, and has no architectural effect. It stays in program order with
// the other memory operations and is never removed. It has no locality
// options; every target prefetches into its first-level cache.
func (b *Builder) Prefetch(address Value, kind ir.PrefetchKind) error {
	if err := requireScalars("Prefetch", address); err != nil {
		return err
	}
	if !kind.Valid() {
		return fmt.Errorf("Prefetch: kind %d is not an ir.PrefetchKind: %w", kind, ErrInvalidFunction)
	}
	var err error
	address, err = b.materialize(address)
	if err != nil {
		return err
	}
	if b == nil || b.returned || !b.valid(address) || address.Type().LaneBits != 64 && address.Type().LaneBits != NativeBits {
		return fmt.Errorf("Prefetch: address value %d has width %d (want native width): %w", address.id, address.Type().LaneBits, ErrInvalidFunction)
	}
	b.append(instruction{Op: ir.OpPrefetch, A: address.id, Imm: uint64(kind)})
	return nil
}
