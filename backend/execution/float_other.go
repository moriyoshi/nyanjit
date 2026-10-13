//go:build !linux || (!amd64 && !arm64 && !386 && !riscv64 && !ppc64le && !loong64)

package execution

import "github.com/moriyoshi/nyanjit/backend/abi"

// No float-passing boundary exists here; NewCode rejects such hosts first.
func enterFloat(*floatPlan, bool, abi.Flavor, *floatCall, uintptr) { panic(ErrUnsupported) }
func enterForeignFloat(uintptr)                                    { panic(ErrUnsupported) }
