package workload_test

import (
	"reflect"
	"testing"

	"github.com/moriyoshi/nyanjit/instrument/workload"
)

func TestAllWorkloadsAreDeterministic(t *testing.T) {
	seen := map[string]bool{}
	for _, w := range workload.All() {
		if w.Name == "" || seen[w.Name] || w.Build == nil || w.Size < 1 || w.Scalar && w.SIMD {
			t.Fatalf("malformed workload %+v", w)
		}
		seen[w.Name] = true
		n := min(w.Size, 50)
		first, second := w.Build(n), w.Build(n)
		if first == second || !reflect.DeepEqual(first.Clone(), second.Clone()) {
			t.Fatalf("%s: two builds of size %d differ or share storage", w.Name, n)
		}
		if w.ScalingSize != 0 && w.Build(2*n).Len() <= first.Len() {
			t.Fatalf("%s: size %d is not larger than size %d", w.Name, 2*n, n)
		}
	}
	if len(seen) < 10 {
		t.Fatalf("only %d workloads", len(seen))
	}
}
