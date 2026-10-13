//go:build nyanjit_no_goregister || !linux || (!arm64 && !amd64 && !386 && !riscv64 && !ppc64le && !loong64)

package goregister

func Supported() bool { return false }

func Register(_ []byte, _ Frame) (bool, error) { return false, ErrUnsupported }

func RegisteredFunctions() (int, int) { return 0, MaxRegisteredFunctions }

func RuntimeEntry(string) (uintptr, error) { return 0, ErrUnsupported }

// IsRegisteredPC is false when runtime registration is disabled.
func IsRegisteredPC(uintptr) bool { return false }
