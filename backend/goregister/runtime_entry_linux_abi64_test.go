//go:build !nyanjit_no_goregister && linux && (arm64 || amd64 || 386 || riscv64 || ppc64le || loong64)

package goregister_test

import (
	"errors"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"runtime"
	"testing"
)

func TestRuntimeEntryUsesExecutableFunctionMetadata(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("unsupported runtime layout")
	}
	for _, name := range []string{"runtime.GC", "runtime.asmcgocall"} {
		address, err := goregister.RuntimeEntry(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		fn := runtime.FuncForPC(address)
		if fn == nil || fn.Name() != name || fn.Entry() != address {
			t.Fatalf("%s entry %#x is not its runtime function", name, address)
		}
	}
	if _, err := goregister.RuntimeEntry("runtime.this_function_does_not_exist"); !errors.Is(err, goregister.ErrUnsupported) {
		t.Fatalf("unknown entry = %v", err)
	}
}
