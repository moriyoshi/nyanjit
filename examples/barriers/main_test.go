//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package main

import (
	"runtime"
	"strings"
	"testing"
)

func TestBarrierExample(t *testing.T) {
	var out strings.Builder
	if err := run(&out, 2000); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	want := []string{
		"arm64    architectural                        dsb sy; isb",
		"ppc64le  platform (needs host configuration)  ori r31, r31, 0",
		"riscv64  unavailable                          -",
		"loong64  architectural                        dbar 0",
	}
	if runtime.GOARCH == "riscv64" || runtime.GOARCH == "loong64" {
		want = append(want, "has no speculation barrier")
	} else {
		want = append(want, "lookup(0x1) = 0x14", "lookup(0x3) = 0xdead", "lookup(0xffffffffffffffff) = 0xdead")
	}
	for _, s := range want {
		if !strings.Contains(text, s) {
			t.Errorf("output lacks %q:\n%s", s, text)
		}
	}
	// The barrier forbids the store-buffering outcome; without it the
	// outcome is allowed but need not occur.
	_, fenced, ok := strings.Cut(text, "store 1; barrier; load")
	if !ok || !strings.Contains(fenced, "both loads saw 0 in 0 of 2000 rounds") {
		t.Errorf("store-buffering outcome with a barrier:\n%s", text)
	}
}
