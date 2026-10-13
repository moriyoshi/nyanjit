//go:build linux && 386

package compile_test

import (
	"errors"
	"os"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/cpuprofile"
	"github.com/moriyoshi/nyanjit/compile"
)

func Test386MissingSSE2Gate(t *testing.T) {
	if os.Getenv("NYANJIT_TEST_NO_SSE2") != "1" {
		t.Skip("negative CPU probe requires GO386=softfloat and SSE2 disabled")
	}
	if cpuprofile.Host().Has("sse2") {
		t.Fatal("negative probe CPU exposes SSE2")
	}
	b := nyanjit.NewBuilder()
	b.Return(b.ConstNative(42))
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range []compile.TargetSpec{{}, {Vectors: compile.VectorsOff()}, {CPU: abi.CPUProfile{Extensions: []string{"sse2"}}}} {
		code, err := nyanjit.Compile(p, spec, compile.Func{})
		if code != nil {
			code.Close()
			t.Fatal("unsupported CPU produced executable code")
		}
		if !errors.Is(err, compile.ErrCPUUnsupported) {
			t.Fatalf("missing SSE2: %v", err)
		}
	}
	t.Log("missing SSE2 rejected with ErrCPUUnsupported")
}
