package abi

// VectorLowering classifies legalization of one semantic vector operation.
type VectorLowering uint8

const (
	Native VectorLowering = iota
	Expanded
	Split
	Scalarized
)

func (l VectorLowering) String() string {
	switch l {
	case Native:
		return "Native"
	case Expanded:
		return "Expanded"
	case Split:
		return "Split"
	case Scalarized:
		return "Scalarized"
	}
	return "invalid"
}

// VectorOperation records the original IR position and complete logical shape.
type VectorOperation struct {
	Position  int
	Operation uint8
	Type      ValueType
	Lowering  VectorLowering
}
