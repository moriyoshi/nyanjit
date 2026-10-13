package op

// PrefetchKind says which access OpPrefetch anticipates. An instruction's Imm
// holds it.
type PrefetchKind uint8

const (
	// PrefetchRead hints that the address will soon be read.
	PrefetchRead PrefetchKind = iota
	// PrefetchWrite hints that the address will soon be written.
	PrefetchWrite

	prefetchEnd
)

// Valid reports whether k is a defined PrefetchKind.
func (k PrefetchKind) Valid() bool { return k < prefetchEnd }
