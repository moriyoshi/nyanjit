package ir

// Label identifies a control-flow destination within one program.
type Label int

// PredicateKind is the shape of a deferred boolean expression.
type PredicateKind uint8

const (
	PredicateCompare PredicateKind = iota
	PredicateAnd
	PredicateOr
	PredicateNot
)

// PredicateExpr is the Program form of a Builder predicate.
// Condition is a target-independent integer comparison.
type PredicateExpr struct {
	Kind      PredicateKind
	Condition BranchCondition
	Value     int
	Other     int
	Anchor    int
	Immediate int64
	WithValue bool
	HasAnchor bool
	Left      *PredicateExpr
	Right     *PredicateExpr
}

// RebasePredicate copies an expression and adjusts its value indices for a
// segment beginning at offset.
func RebasePredicate(root *PredicateExpr, offset int) *PredicateExpr {
	memo := make(map[*PredicateExpr]*PredicateExpr)
	var copyNode func(*PredicateExpr) *PredicateExpr
	copyNode = func(node *PredicateExpr) *PredicateExpr {
		if node == nil {
			return nil
		}
		if existing := memo[node]; existing != nil {
			return existing
		}
		copy := *node
		memo[node] = &copy
		if copy.Kind == PredicateCompare {
			copy.Value -= offset
			if copy.HasAnchor {
				copy.Anchor -= offset
			}
			if copy.WithValue {
				copy.Other -= offset
			}
		} else {
			copy.Left = copyNode(node.Left)
			copy.Right = copyNode(node.Right)
		}
		return &copy
	}
	return copyNode(root)
}

// PredicateUsesLeftValue reports whether every comparison in an expression
// uses value as its left operand.
func PredicateUsesLeftValue(node *PredicateExpr, value int) bool {
	if node == nil {
		return false
	}
	switch node.Kind {
	case PredicateCompare:
		return node.Value == value
	case PredicateNot:
		return PredicateUsesLeftValue(node.Left, value)
	case PredicateAnd, PredicateOr:
		return PredicateUsesLeftValue(node.Left, value) &&
			PredicateUsesLeftValue(node.Right, value)
	default:
		return false
	}
}

type ControlTerm uint8

const (
	TermJump ControlTerm = iota
	TermBranch
	TermReturn
)

// ControlStep names a label or a terminated instruction segment.
type ControlStep struct {
	Label, To, Other int
	IsLabel          bool
	Term             ControlTerm
	Start, End       int
	LeaveStart       int
	LeaveEnd         int
	Result           int
	// ResultParts contains low and high 64-bit IDs for a 128-bit return.
	ResultParts []int
	Arity       int
	Values      []int
	Args        []int
	OtherArgs   []int
	Predicate   *PredicateExpr
}

// Control holds the target-independent labeled control graph.
type Control struct {
	Labels      []bool
	LabelScopes []int
	Params      []int
	ParamBits   [][]uint8
	ParamTypes  [][]Type
	Steps       []ControlStep
}

func cloneControl(source *Control) *Control {
	if source == nil {
		return nil
	}
	out := &Control{
		ParamTypes:  make([][]Type, len(source.ParamTypes)),
		Labels:      append([]bool(nil), source.Labels...),
		LabelScopes: append([]int(nil), source.LabelScopes...),
		Params:      append([]int(nil), source.Params...),
		ParamBits:   make([][]uint8, len(source.ParamBits)),
		Steps:       make([]ControlStep, len(source.Steps)),
	}
	for i, t := range source.ParamTypes {
		out.ParamTypes[i] = append([]Type(nil), t...)
	}
	for i, bits := range source.ParamBits {
		out.ParamBits[i] = append([]uint8(nil), bits...)
	}
	memo := make(map[*PredicateExpr]*PredicateExpr)
	var clonePredicate func(*PredicateExpr) *PredicateExpr
	clonePredicate = func(node *PredicateExpr) *PredicateExpr {
		if node == nil {
			return nil
		}
		if copyNode := memo[node]; copyNode != nil {
			return copyNode
		}
		copyNode := *node
		memo[node] = &copyNode
		copyNode.Left, copyNode.Right = clonePredicate(node.Left), clonePredicate(node.Right)
		return &copyNode
	}
	for i, step := range source.Steps {
		step.Values = append([]int(nil), step.Values...)
		step.ResultParts = append([]int(nil), step.ResultParts...)
		step.Args = append([]int(nil), step.Args...)
		step.OtherArgs = append([]int(nil), step.OtherArgs...)
		step.Predicate = clonePredicate(step.Predicate)
		out.Steps[i] = step
	}
	return out
}
