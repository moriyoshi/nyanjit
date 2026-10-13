package ir

import (
	"errors"
	"fmt"
)

// MaxPredicateNodes bounds total expanded work across a program, including repeated DAG subtrees.
// Predicate walks use worklists and impose no separate depth limit.
const MaxPredicateNodes = 1 << 14

// ErrPredicateLimit identifies predicates beyond the compiler's expanded-work budget.
var ErrPredicateLimit = errors.New("ir: predicate exceeds backend limits")

// validatePredicateLimits checks cycles and expanded size in linear time for DAGs.
func validatePredicateLimits(root *PredicateExpr) error {
	_, err := predicateWork(root)
	return err
}

func predicateWork(root *PredicateExpr) (int, error) {
	if root == nil {
		return 0, nil
	}
	if root.Kind == PredicateCompare {
		if root.Left != nil || root.Right != nil {
			return 0, invalidRule("predicate node 0: comparison has children")
		}
		return 1, nil
	}

	type frame struct {
		node  *PredicateExpr
		done  bool
		index int
	}
	state := map[*PredicateExpr]uint8{}
	sizes := map[*PredicateExpr]int{}
	work := []frame{{node: root}}
	nextIndex := 0
	for len(work) != 0 {
		f := work[len(work)-1]
		work = work[:len(work)-1]
		n := f.node
		if !f.done && n != nil && state[n] == 2 {
			continue
		}
		if !f.done {
			f.index = nextIndex
			nextIndex++
		}
		invalid := func(rule string) error {
			return invalidRule("predicate node %d: %s", f.index, rule)
		}
		if n == nil {
			return 0, invalid("missing child")
		}
		if f.done {
			nodes := 1
			if n.Kind != PredicateCompare {
				nodes += sizes[n.Left]
				if n.Kind != PredicateNot {
					nodes += sizes[n.Right]
				}
			}
			if nodes > MaxPredicateNodes {
				return 0, fmt.Errorf("predicate node %d: expanded work exceeds limit: %w", f.index, ErrPredicateLimit)
			}
			sizes[n], state[n] = nodes, 2
			continue
		}
		if state[n] == 1 {
			return 0, invalid("cycle")
		}
		if state[n] == 2 {
			continue
		}
		state[n] = 1
		work = append(work, frame{node: n, done: true, index: f.index})
		switch n.Kind {
		case PredicateCompare:
			if n.Left != nil || n.Right != nil {
				return 0, invalid("comparison has children")
			}
		case PredicateNot:
			if n.Right != nil {
				return 0, invalid("not has a right child")
			}
			work = append(work, frame{node: n.Left})
		case PredicateAnd, PredicateOr:
			work = append(work, frame{node: n.Right}, frame{node: n.Left})
		default:
			return 0, invalid("unknown predicate kind")
		}
	}
	return sizes[root], nil
}
