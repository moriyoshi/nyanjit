//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || 386 || loong64)

package execution

import (
	"errors"
	"testing"
)

// TestArenaPlaceReportsFillErrors checks that a failed relocation fails the
// placement and returns its pages to the arena.
func TestArenaPlaceReportsFillErrors(t *testing.T) {
	a, err := NewArena(2 * pageSize())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	failed := errors.New("relocation failed")
	pages, err := a.place(make([]byte, 16), func([]byte) error { return failed })
	if !errors.Is(err, failed) || pages != nil {
		t.Fatalf("place = %v, %v; want the relocation error", pages, err)
	}
	if a.used != 0 || len(a.free) != 1 || a.free[0] != (pageRun{0, 2}) {
		t.Fatalf("pages not returned: used %d, free %v", a.used, a.free)
	}
	// The whole arena is still available.
	pages, err = a.place(make([]byte, 2*a.page), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.release(pages); err != nil {
		t.Fatal(err)
	}
}
