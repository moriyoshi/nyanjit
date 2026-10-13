package op

// BranchCondition selects the integer comparison used by a predicate.
type BranchCondition uint8

const (
	// BranchEqual and BranchNotEqual test equality.
	BranchEqual BranchCondition = iota
	BranchNotEqual
	// BranchLess, BranchGreaterEqual, BranchLessEqual and BranchGreater
	// compare as two's complement signed integers.
	BranchLess
	BranchGreaterEqual
	BranchLessEqual
	BranchGreater
	// BranchBelow, BranchAboveEqual, BranchBelowEqual and BranchAbove compare
	// as unsigned integers.
	BranchBelow
	BranchAboveEqual
	BranchBelowEqual
	BranchAbove
)

// FloatCondition selects the IEEE 754 comparison of OpFCompare. Conditions
// come in pairs: c^1 is the negation of c. Ordered conditions are false when
// either operand is NaN, and unordered conditions are then true.
type FloatCondition uint8

const (
	// FloatEqual is a == b, and FloatNotEqual is a != b as Go defines it.
	FloatEqual FloatCondition = iota
	FloatNotEqual
	// FloatLess is a < b; FloatUnorderedGreaterEqual negates it.
	FloatLess
	FloatUnorderedGreaterEqual
	// FloatLessEqual is a <= b; FloatUnorderedGreater negates it.
	FloatLessEqual
	FloatUnorderedGreater
	// FloatGreater is a > b; FloatUnorderedLessEqual negates it.
	FloatGreater
	FloatUnorderedLessEqual
	// FloatGreaterEqual is a >= b; FloatUnorderedLess negates it.
	FloatGreaterEqual
	FloatUnorderedLess
	// FloatOrdered holds when neither operand is NaN, and FloatUnordered
	// when either is.
	FloatOrdered
	FloatUnordered
	// FloatOrderedNotEqual is a < b || a > b; FloatUnorderedEqual negates it.
	FloatOrderedNotEqual
	FloatUnorderedEqual
)
