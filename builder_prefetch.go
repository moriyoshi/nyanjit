package nyanjit

import "github.com/moriyoshi/nyanjit/ir"

// Prefetch hints that address will soon be read (ir.PrefetchRead) or written
// (ir.PrefetchWrite). It never faults, even for an invalid address, and has
// no architectural effect. It stays in order with memory operations.
func (b *Builder) Prefetch(address Value, kind ir.PrefetchKind) {
	if b.ready() {
		b.okDo(b.inner.Prefetch(address, kind))
	}
}
