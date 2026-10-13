package main

import (
	"os/exec"
	"testing"
)

func TestPrivateBinfmtSelection(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	cmd := exec.Command(python, "-B", "-m", "unittest", "test_binfmt.py")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("binfmt tests: %v\n%s", err, out)
	}
}
