package builder

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

// Branch followed by Jump from the empty fallthrough block is one two-way
// branch: the Jump becomes the Branch's false edge and adds no block.
func TestBranchThenJumpIsOneTwoWayBranch(t *testing.T) {
	b := NewBuilder()
	x, _ := b.Param()
	yes, no := b.NewLabel(), b.NewLabel()
	zero, _ := b.Const(0)
	p, _ := b.Compare(x, ir.BranchEqual, zero)
	if err := b.Branch(p, yes); err != nil {
		t.Fatal(err)
	}
	if err := b.Jump(no); err != nil {
		t.Fatal(err)
	}
	for _, label := range []Label{yes, no} {
		if err := b.Bind(label); err != nil {
			t.Fatal(err)
		}
		if err := b.Return(x); err != nil {
			t.Fatal(err)
		}
	}
	program, err := testProgram(b)
	if err != nil {
		t.Fatal(err)
	}
	control := program.Control
	branches := 0
	for _, step := range control.Steps {
		if !step.IsLabel && step.Term == ir.TermBranch {
			branches++
			if step.To != int(yes.index) || step.Other != int(no.index) {
				t.Fatalf("branch edges %d/%d, want %d/%d", step.To, step.Other, yes.index, no.index)
			}
		}
		if !step.IsLabel && step.Term == ir.TermJump {
			t.Fatalf("Branch+Jump left a jump block: %+v", step)
		}
	}
	// Two user labels and the entry label; the fallthrough label is gone.
	if branches != 1 || len(control.Labels) != 3 {
		t.Fatalf("branches %d labels %d, want 1 and 3", branches, len(control.Labels))
	}
}

// A nonempty fallthrough block keeps its own Jump.
func TestBranchFallthroughWithCodeKeepsJump(t *testing.T) {
	b := NewBuilder()
	x, _ := b.Param()
	yes, no := b.NewLabel(), b.NewLabel()
	zero, _ := b.Const(0)
	p, _ := b.Compare(x, ir.BranchEqual, zero)
	if err := b.Branch(p, yes); err != nil {
		t.Fatal(err)
	}
	y, _ := b.Add(x, x)
	if err := b.Jump(no); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(yes); err != nil {
		t.Fatal(err)
	}
	if err := b.Return(x); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(no); err != nil {
		t.Fatal(err)
	}
	if err := b.Return(y); err != nil {
		t.Fatal(err)
	}
	program, err := testProgram(b)
	if err != nil {
		t.Fatal(err)
	}
	jumps := 0
	for _, step := range program.Control.Steps {
		if !step.IsLabel && step.Term == ir.TermJump {
			jumps++
		}
	}
	if jumps != 1 {
		t.Fatalf("jumps %d, want 1", jumps)
	}
}

// Labels are tagged with their Builder: the zero Label, a Label of another
// Builder and a Label of an earlier Builder are rejected.
func TestLabelsAreBuilderTagged(t *testing.T) {
	earlier := NewBuilder()
	stale := earlier.NewLabel()
	if err := earlier.Bind(stale); err != nil {
		t.Fatal(err)
	}
	one, _ := earlier.Const(1)
	if err := earlier.Return(one); err != nil {
		t.Fatal(err)
	}
	if _, err := earlier.Finish(); err != nil {
		t.Fatal(err)
	}
	if label := earlier.NewLabel(); label != (Label{}) {
		t.Fatalf("NewLabel after Finish = %+v, want the zero Label", label)
	}

	b := NewBuilder()
	other := NewBuilder()
	foreign := other.NewLabel()
	own := b.NewLabel()
	x, _ := b.Param()
	zero, _ := b.Const(0)
	p, _ := b.Compare(x, ir.BranchEqual, zero)
	if own.index != foreign.index || own.index != stale.index {
		t.Fatalf("label indices %d/%d/%d differ; the test needs equal indices", own.index, foreign.index, stale.index)
	}
	for name, label := range map[string]Label{"zero": {}, "foreign": foreign, "stale": stale} {
		if err := b.Bind(label); !errors.Is(err, ErrInvalidFunction) {
			t.Fatalf("Bind(%s) = %v", name, err)
		}
		if err := b.Branch(p, label); !errors.Is(err, ErrInvalidFunction) {
			t.Fatalf("Branch(%s) = %v", name, err)
		}
		if err := b.Jump(label); !errors.Is(err, ErrInvalidFunction) {
			t.Fatalf("Jump(%s) = %v", name, err)
		}
		if _, err := b.IndexedAddress(x, x, x, 1, label); !errors.Is(err, ErrInvalidFunction) {
			t.Fatalf("IndexedAddress(%s) = %v", name, err)
		}
	}
	if err := b.Branch(p, own); err != nil {
		t.Fatal(err)
	}
	if err := b.Return(x); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(own); err != nil {
		t.Fatal(err)
	}
	if err := b.Return(zero); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(); err != nil {
		t.Fatal(err)
	}
}

// The zero value of Builder tags its Labels too.
func TestZeroBuilderLabels(t *testing.T) {
	var a, b Builder
	la, lb := a.NewLabel(), b.NewLabel()
	if la == (Label{}) || lb == (Label{}) || la == lb {
		t.Fatalf("zero-value builders gave labels %+v and %+v", la, lb)
	}
	if err := b.Bind(la); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("Bind of another zero Builder's label = %v", err)
	}
}
