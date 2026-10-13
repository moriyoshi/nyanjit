// Package regalloc holds IR-neutral register allocation. An Allocator
// allocates a whole Function and emits its moves through the client's Mover
// while the client walks the function: Batch allocates it at once, with
// liveness and live-range splitting; Online allocates as the client walks,
// for speed; Verify checks any Allocator. Residency is the client-driven
// core beneath Online, for clients that walk a linear Program themselves.
// The primitives they are built from are register groups and pins, value
// homes and locations, and edge transfer planning.
//
// Construct this package's exported structs with keyed fields. Field order is
// not part of the API: fields may be reordered or packed, though removing a
// field or changing its meaning is a breaking change.
package regalloc
