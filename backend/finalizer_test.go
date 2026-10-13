package backend

import (
	"slices"
	"testing"
)

func TestFinalizersRunLastFirstOnce(t *testing.T) {
	var order []int
	fs := Finalizers{
		FinalizerFunc(func() { order = append(order, 1) }),
		nil,
		FinalizerFunc(func() { order = append(order, 2) }),
		Finalizers{FinalizerFunc(func() { order = append(order, 3) })},
	}
	fs.Finalize()
	if !slices.Equal(order, []int{3, 2, 1}) {
		t.Fatalf("order %v", order)
	}
	Finalizers(nil).Finalize()
}
