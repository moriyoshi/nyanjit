//go:build !linux || (!arm64 && !amd64 && !386 && !riscv64 && !ppc64 && !ppc64le)

package execution

import "github.com/moriyoshi/nyanjit/abi"

func mapCode([]byte) ([]byte, error)                                              { return nil, ErrUnsupported }
func unmapCode([]byte) error                                                      { return nil }
func entry([]byte) uintptr                                                        { return 0 }
func scratch(int) ([]byte, error)                                                 { return nil, ErrUnsupported }
func freeScratch([]byte) error                                                    { return nil }
func callWithABI(abi.Flavor, uintptr, *ArgumentWords, uintptr) (uintptr, uintptr) { return 0, 0 }
func callWithABIArity(abi.Flavor, uintptr, *ArgumentWords, uintptr, int) (uintptr, uintptr) {
	return 0, 0
}
func callPair(uintptr, *ArgumentWords, uintptr) (uintptr, uintptr) { return 0, 0 }
func callForeign(uintptr, []uintptr) (uintptr, uintptr)            { return 0, 0 }
func registerForeignCallback(any) (uintptr, error)                 { return 0, ErrUnsupported }
func foreignSupported() bool                                       { return false }
