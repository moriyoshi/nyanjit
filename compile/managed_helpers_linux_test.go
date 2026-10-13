//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"runtime"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

// Shared by managed execution tests and lowering-only contract tests. Keep
// these function descriptions available when runtime registration is disabled.
func managedFuncs() map[string]compile.Func {
	stack := compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640}
	if runtime.GOARCH == "386" {
		stack.BytesAvailable = 240
	}
	return map[string]compile.Func{
		"C":  {ABI: abi.C, Context: compile.ContextGoManaged, Stack: stack, AssumePointerFree: true},
		"Go": {ABI: abi.Go, Context: compile.ContextGoManaged, Stack: stack, AssumePointerFree: true},
	}
}
