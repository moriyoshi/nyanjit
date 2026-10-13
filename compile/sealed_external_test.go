package compile_test

import (
	"bytes"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/workload"
)

func TestSealedLoweringBorrowsFactsWithoutCloning(t *testing.T) {
	compile.CheckSealedLoweringBorrowsFactsWithoutCloning(t, workload.Loops(80))
}

func TestSealedConcurrentLoweringsKeepFactsImmutable(t *testing.T) {
	compile.CheckSealedConcurrentLoweringsKeepFacts(t, workload.Loops(80))
}

func TestSealedNativeTemplate(t *testing.T) {
	if runtime.GOOS != "linux" || !compile.HostBackend() {
		t.Skip("no native backend")
	}
	p := workload.Chain(20)
	u, err := lowerUnit(p, compile.TargetSpec{}, compile.Func{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.Template(); err != nil {
		t.Fatal(err)
	}
	raw, err := lowerTemplate(p.Clone(), compile.TargetSpec{}, compile.Func{Context: compile.ContextExternal})
	if err != nil {
		t.Fatal(err)
	}
	rawBytes, err := raw.Bytes(nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := compile.EncodedBytes(u)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rawBytes, encoded) {
		t.Fatal("sealed adapter changed native encoding")
	}
}
