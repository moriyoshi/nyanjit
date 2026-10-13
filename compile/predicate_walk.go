package compile

import (
	"iter"

	"github.com/moriyoshi/nyanjit/ir"
)

// predicateComparisons visits each comparison once, in left-to-right order.
// Malformed children, unknown kinds and cycles produce ErrInvalidFunction.
func predicateComparisons(root *ir.PredicateExpr) iter.Seq2[*ir.PredicateExpr, error] {
	return func(yield func(*ir.PredicateExpr, error) bool) {
		type task struct {
			node *ir.PredicateExpr
			exit bool
		}
		seen := make(map[*ir.PredicateExpr]uint8)
		work := []task{{node: root}}
		for len(work) != 0 {
			f := work[len(work)-1]
			work = work[:len(work)-1]
			node := f.node
			if node == nil {
				yield(nil, ErrInvalidFunction)
				return
			}
			if f.exit {
				seen[node] = 2
				continue
			}
			if seen[node] == 2 {
				continue
			}
			if seen[node] == 1 {
				yield(nil, ErrInvalidFunction)
				return
			}
			switch node.Kind {
			case ir.PredicateCompare:
				if node.Left != nil || node.Right != nil {
					yield(nil, ErrInvalidFunction)
					return
				}
				seen[node] = 2
				if !yield(node, nil) {
					return
				}
			case ir.PredicateNot:
				if node.Left == nil || node.Right != nil {
					yield(nil, ErrInvalidFunction)
					return
				}
				seen[node] = 1
				work = append(work, task{node: node, exit: true}, task{node: node.Left})
			case ir.PredicateAnd, ir.PredicateOr:
				if node.Left == nil || node.Right == nil {
					yield(nil, ErrInvalidFunction)
					return
				}
				seen[node] = 1
				work = append(work, task{node: node, exit: true}, task{node: node.Right}, task{node: node.Left})
			default:
				yield(nil, ErrInvalidFunction)
				return
			}
		}
	}
}
