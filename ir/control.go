package ir

// Label identifies a control-flow destination within one program.
type Label int

// PredicateKind is the shape of a deferred boolean expression.
type PredicateKind uint8

const (
	// PredicateCompare compares Value with Other (WithValue) or Immediate.
	PredicateCompare PredicateKind = iota
	// PredicateAnd is the short-circuit conjunction of Left and Right.
	PredicateAnd
	// PredicateOr is the short-circuit disjunction of Left and Right.
	PredicateOr
	// PredicateNot negates Left; Right must be nil.
	PredicateNot
)

// PredicateExpr is the Program form of a Builder predicate.
// Condition is a target-independent integer comparison. Operands are scalars
// of at most 64 bits, compared in their zero-extended register image, so
// sign-extend narrow operands before a signed comparison. Every operand must
// appear in the branch step's Values. HasAnchor names the arithmetic value
// whose flags the comparison mirrors; it does not change the result.
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
	return copyPredicate(root, make(map[*PredicateExpr]*PredicateExpr), offset)
}

// copyPredicate preserves sharing and cycles without depending on Go stack depth.
// The nodes it copies share one array: it first collects, breadth first, the
// nodes memo does not hold, marking them visiting, then copies and links them.
func copyPredicate(root *PredicateExpr, memo map[*PredicateExpr]*PredicateExpr, offset int) *PredicateExpr {
	if root == nil {
		return nil
	}
	var order []*PredicateExpr
	visit := func(n *PredicateExpr) {
		if n != nil && memo[n] == nil {
			memo[n] = predicateVisiting
			order = append(order, n)
		}
	}
	visit(root)
	for i := 0; i < len(order); i++ {
		visit(order[i].Left)
		visit(order[i].Right)
	}
	copies := make([]PredicateExpr, len(order))
	for i, n := range order {
		copies[i] = *n
		memo[n] = &copies[i]
	}
	for i, n := range order {
		c := &copies[i]
		c.Left, c.Right = memo[n.Left], memo[n.Right]
		if c.Kind == PredicateCompare {
			c.Value -= offset
			if c.HasAnchor {
				c.Anchor -= offset
			}
			if c.WithValue {
				c.Other -= offset
			}
		}
	}
	return memo[root]
}

// predicateVisiting marks a node copyPredicate has collected but not copied.
var predicateVisiting = new(PredicateExpr)

// PredicateUsesLeftValue reports whether every comparison uses value on the left.
func PredicateUsesLeftValue(node *PredicateExpr, value int) bool {
	work := []*PredicateExpr{node}
	seen := map[*PredicateExpr]bool{}
	for len(work) != 0 {
		n := work[len(work)-1]
		work = work[:len(work)-1]
		if n == nil {
			return false
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		switch n.Kind {
		case PredicateCompare:
			if n.Value != value {
				return false
			}
		case PredicateNot:
			work = append(work, n.Left)
		case PredicateAnd, PredicateOr:
			work = append(work, n.Right, n.Left)
		default:
			return false
		}
	}
	return true
}

// ControlTerm says how a control step's segment ends.
type ControlTerm uint8

const (
	// TermJump continues at label To with Args.
	TermJump ControlTerm = iota
	// TermBranch continues at To with Args when Predicate holds, and at
	// Other with OtherArgs otherwise.
	TermBranch
	// TermReturn returns Result, or the 128-bit pair ResultParts.
	TermReturn
)

// ControlStep names a label or a terminated instruction segment.
//
// A label uses IsLabel and Label. Its other fields must be zero or empty,
// except To and Other may also be -1, and Result is ignored.
//
// A segment uses Start, End, LeaveStart, LeaveEnd and Arity; Label is ignored.
// TermJump uses To and Args. TermBranch uses To/Args and Other/OtherArgs for
// its true and false edges, Predicate, Values (materialized predicate operands
// and any additional exports), and Result (the segment result used when lowering
// the predicate).
// TermReturn uses Result and optionally ResultParts. Result is ignored on jumps.
//
// Edge destinations not used by the terminator must be 0 or -1, and unused
// argument lists, Predicate, Values and ResultParts must be empty. Validation
// rejects these unused payloads before any predicate traversal or lowering.
type ControlStep struct {
	Label, To, Other int
	IsLabel          bool
	Term             ControlTerm
	Start, End       int
	LeaveStart       int
	LeaveEnd         int
	// Result is ignored on TermJump steps and labels.
	Result int
	// ResultParts contains low and high 64-bit IDs for a 128-bit return.
	ResultParts []int
	Arity       int
	Values      []int
	Args        []int
	OtherArgs   []int
	Predicate   *PredicateExpr
}

// controlStepFieldRule rejects unused payloads before any reader can treat them
// as edges, materialized operands, or predicate work. Empty destination fields
// accept both the Go zero value and Builder's explicit -1 sentinel.
func controlStepFieldRule(step *ControlStep) string {
	unset := func(label int) bool { return label == 0 || label == -1 }
	if step.IsLabel {
		if !unset(step.To) || !unset(step.Other) || len(step.Args) != 0 || len(step.OtherArgs) != 0 ||
			step.Predicate != nil || len(step.Values) != 0 || len(step.ResultParts) != 0 ||
			step.Term != TermJump || step.Start != 0 || step.End != 0 ||
			step.LeaveStart != 0 || step.LeaveEnd != 0 || step.Arity != 0 {
			return "segment fields on label"
		}
		return ""
	}
	if step.Term != TermBranch && (step.Predicate != nil || len(step.Values) != 0 || len(step.OtherArgs) != 0 || !unset(step.Other)) {
		return "branch-only fields on non-branch terminator"
	}
	if step.Term == TermReturn && (len(step.Args) != 0 || !unset(step.To)) {
		return "edge fields on return"
	}
	if step.Term != TermReturn && len(step.ResultParts) != 0 {
		return "ResultParts on non-return terminator"
	}
	return ""
}

// Control holds the target-independent labeled control graph. Steps lay out
// the program: every segment follows one or more label steps, segments are
// contiguous and cover every instruction and scope exit, and the first
// step's label is the entry, which takes no parameters. A block is a segment
// with its labels. Values are usable in every block their definition
// dominates; see docs/ir.md.
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
	for i, step := range source.Steps {
		step.Values = append([]int(nil), step.Values...)
		step.ResultParts = append([]int(nil), step.ResultParts...)
		step.Args = append([]int(nil), step.Args...)
		step.OtherArgs = append([]int(nil), step.OtherArgs...)
		step.Predicate = copyPredicate(step.Predicate, memo, 0)
		out.Steps[i] = step
	}
	return out
}
