package regalloc

// Class selects an independent physical register file.
type Class uint8

const (
	General Class = iota
	Vector
	Predicate
	Float // scalar FP storage; aliases the vector register file
)
