//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package execution

import "github.com/ebitengine/purego"

// enterForeignFloat runs the float-passing shim for block on the system
// stack through purego's runtime-aware C call.
func enterForeignFloat(block uintptr) { purego.SyscallN(floatForeignAddress, block) }
