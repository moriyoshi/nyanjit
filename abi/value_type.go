package abi

// Kind distinguishes scalars, lane vectors and logical boolean masks.
type ValueKind uint8

const (
	InvalidKind ValueKind = iota
	ScalarKind
	VectorKind
	MaskKind
)

// Type describes logical shape, independently of physical register width.
type ValueType struct {
	Kind     ValueKind
	Lanes    uint16
	LaneBits uint8
}

// Valid reports whether the shape is supported by the logical type system.
func (t ValueType) Valid() bool {
	switch t.Kind {
	case ScalarKind:
		return t.Lanes == 1 && (t.LaneBits == 8 || t.LaneBits == 16 || t.LaneBits == 32 || t.LaneBits == 64 || t.LaneBits == 128)
	case VectorKind:
		return t.Lanes >= 2 && uint32(t.Lanes)*uint32(t.LaneBits) <= 512 && (t.LaneBits == 8 || t.LaneBits == 16 || t.LaneBits == 32 || t.LaneBits == 64)
	case MaskKind:
		return t.Lanes >= 2 && t.Lanes <= 64 && t.LaneBits == 1
	}
	return false
}

// Bits returns total logical width; invalid types have width zero.
func (t ValueType) Bits() uint16 {
	if !t.Valid() {
		return 0
	}
	return t.Lanes * uint16(t.LaneBits)
}
