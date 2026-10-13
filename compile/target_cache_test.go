package compile

import (
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

// TestTargetCacheNormalizesCPUNames checks that CPU profiles differing only in
// the case, order or repetition of their names share one cached Target.
func TestTargetCacheNormalizesCPUNames(t *testing.T) {
	spellings := [][]string{
		{"fp", "neon"},
		{"neon", "fp"},
		{"NEON", "Fp"},
		{"neon", "fp", "neon", "fp", "neon"},
	}
	var first Target
	for _, extensions := range spellings {
		target, err := NewTarget(TargetSpec{Architecture: "arm64", CPU: abi.CPUProfile{Extensions: extensions}})
		if err != nil {
			t.Fatalf("NewTarget(%q): %v", extensions, err)
		}
		if first == nil {
			first = target
		} else if target != first {
			t.Fatalf("NewTarget(%q) returned a second Target", extensions)
		}
	}
	if got := first.Spec().CPU.Extensions; !slices.Equal(got, []string{"fp", "neon"}) {
		t.Fatalf("Spec().CPU.Extensions = %q, want normalized [fp neon]", got)
	}
	level, err := NewTarget(TargetSpec{Architecture: "ppc64le", CPU: abi.CPUProfile{Level: "POWER9"}})
	if err != nil {
		t.Fatal(err)
	}
	lower, err := NewTarget(TargetSpec{Architecture: "ppc64le", CPU: abi.CPUProfile{Level: "power9"}})
	if err != nil {
		t.Fatal(err)
	}
	if level != lower {
		t.Fatal("CPU levels differing in case returned different Targets")
	}
}

// TestTargetCacheIsBounded checks that ever more distinct specs stop growing
// the target cache once it holds maxCachedTargets entries.
func TestTargetCacheIsBounded(t *testing.T) {
	before := map[any]bool{}
	targetCache.Range(func(key, _ any) bool {
		before[key] = true
		return true
	})
	entries := targetEntries.Load()
	t.Cleanup(func() {
		targetCache.Range(func(key, _ any) bool {
			if !before[key] {
				targetCache.Delete(key)
			}
			return true
		})
		targetEntries.Store(entries)
		for i := range recentTargets {
			recentTargets[i].Store(nil)
		}
	})
	var extensions []string
	for n := 0; n < 2*maxCachedTargets; n++ {
		extensions = append(extensions, "neon")
		target, err := NewTarget(TargetSpec{Architecture: "arm64", Vectors: VectorsRequire(extensions...)})
		if err != nil {
			t.Fatalf("NewTarget with %d neon requests: %v", n+1, err)
		}
		if target.Architecture() != "arm64" {
			t.Fatalf("Architecture() = %q", target.Architecture())
		}
	}
	cached := 0
	targetCache.Range(func(any, any) bool {
		cached++
		return true
	})
	if cached > maxCachedTargets || targetEntries.Load() > maxCachedTargets {
		t.Fatalf("target cache holds %d entries (counter %d), want at most %d", cached, targetEntries.Load(), maxCachedTargets)
	}
}
