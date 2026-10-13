//go:build !linux || (!arm64 && !amd64 && !386 && !riscv64 && !ppc64le && !loong64)

package execution

import "github.com/moriyoshi/nyanjit/backend/abi"

func mapCode([]byte, func([]byte) error) ([]byte, error)                          { return nil, ErrUnsupported }
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

func pageSize() int                                     { return 4096 }
func fillCode([]byte, []byte, func([]byte) error) error { return ErrUnsupported }
func makeExecutable([]byte, int) error                  { return ErrUnsupported }
func reserveArena(int) ([]byte, error)                  { return nil, ErrUnsupported }
func unreserveArena([]byte) error                       { return nil }
func commitPages([]byte) error                          { return ErrUnsupported }
func decommitPages([]byte) error                        { return nil }
