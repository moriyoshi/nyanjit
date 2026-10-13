//go:build !linux || (!arm64 && !amd64 && !386 && !riscv64 && !ppc64 && !ppc64le)

package goregister

func Supported() bool { return false }

func Register(_ []byte, _ Frame) (bool, error) { return false, ErrUnsupported }

func RuntimeEntry(string) (uintptr, error) { return 0, ErrUnsupported }
