package compile

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func TestPredicateComparisonsRejectMalformedGraphs(t *testing.T) {
	leaf := &ir.PredicateExpr{Kind: ir.PredicateCompare}
	cycle := &ir.PredicateExpr{Kind: ir.PredicateNot}
	cycle.Left = cycle
	for _, p := range []*ir.PredicateExpr{nil, {Kind: ir.PredicateKind(255)}, {Kind: ir.PredicateNot}, {Kind: ir.PredicateAnd, Left: leaf}, {Kind: ir.PredicateOr, Right: leaf}, cycle} {
		rejected := false
		for _, err := range predicateComparisons(p) {
			if err != nil {
				rejected = errors.Is(err, ErrInvalidFunction)
			}
		}
		if !rejected {
			t.Fatalf("malformed predicate accepted: %p", p)
		}
	}
}
func TestPredicateComparisonsVisitSharedLeavesInOrder(t *testing.T) {
	a, b := &ir.PredicateExpr{Kind: ir.PredicateCompare, Value: 1}, &ir.PredicateExpr{Kind: ir.PredicateCompare, Value: 2}
	p := &ir.PredicateExpr{Kind: ir.PredicateAnd, Left: a, Right: &ir.PredicateExpr{Kind: ir.PredicateOr, Left: a, Right: b}}
	var ids []int
	for node, err := range predicateComparisons(p) {
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, node.Value)
	}
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("visit order %v", ids)
	}
}
