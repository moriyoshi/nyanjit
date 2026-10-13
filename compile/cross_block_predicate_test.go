package compile

import (
	"github.com/moriyoshi/nyanjit/ir"
	"testing"
)

func TestThreadedPredicateRemapsAnchorsAndSharing(t *testing.T) {
	leaf := &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchNotEqual, Value: 3, Other: 5, WithValue: true, Anchor: 7, HasAnchor: true}
	memo := map[*ir.PredicateExpr]*ir.PredicateExpr{leaf: leaf}
	remap := func(id int) int { return id + 20 }
	got := remapPredicate(leaf, memo, remap)
	if got == leaf || got.Value != 23 || got.Other != 25 || got.Anchor != 27 || got.Condition != leaf.Condition || !got.WithValue || !got.HasAnchor {
		t.Fatalf("incorrect relocated comparison: %+v", got)
	}
	if leaf.Value != 3 || leaf.Other != 5 || leaf.Anchor != 7 {
		t.Fatal("remapping mutated source")
	}
	graph := &ir.PredicateExpr{Kind: ir.PredicateOr, Left: leaf, Right: leaf}
	copied := remapPredicate(graph, memo, remap)
	if copied == graph || copied.Left == leaf || copied.Left != copied.Right || *copied.Left != *got {
		t.Fatalf("lost shared predicate identity or operands: %+v", copied)
	}
}
