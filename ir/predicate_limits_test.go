package ir

import (
	"errors"
	"testing"
)

func TestPredicateLimits(t *testing.T) {
	leaf := &PredicateExpr{Kind: PredicateCompare}
	root := leaf
	for range MaxPredicateNodes - 1 {
		root = &PredicateExpr{Kind: PredicateNot, Left: root}
	}
	if err := validatePredicateLimits(root); err != nil {
		t.Fatalf("deep tree: %v", err)
	}
	root = leaf
	for nodes := 1; nodes <= MaxPredicateNodes; nodes = 2*nodes + 1 {
		if err := validatePredicateLimits(root); err != nil {
			t.Fatalf("nodes%d: %v", nodes, err)
		}
		root = &PredicateExpr{Kind: PredicateAnd, Left: root, Right: root}
	}
	if !errors.Is(validatePredicateLimits(root), ErrPredicateLimit) {
		t.Fatal("missing expanded DAG size limit")
	}
	exact := &PredicateExpr{Kind: PredicateNot, Left: root.Left}
	if err := validatePredicateLimits(exact); err != nil {
		t.Fatalf("exact expanded-node limit: %v", err)
	}
	if !errors.Is(validatePredicateLimits(&PredicateExpr{Kind: PredicateNot, Left: exact}), ErrPredicateLimit) {
		t.Fatal("accepted one node above expanded limit")
	}
	root = &PredicateExpr{Kind: PredicateNot}
	root.Left = root
	if !errors.Is(validatePredicateLimits(root), ErrInvalidProgram) {
		t.Fatal("cycle accepted")
	}
	copy := RebasePredicate(root, 0)
	if copy == root || copy.Left != copy {
		t.Fatal("cyclic graph copy broken")
	}
}
