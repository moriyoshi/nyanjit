//go:build !linux || (!amd64 && !arm64 && !386 && !riscv64 && !ppc64 && !ppc64le)

package execution

func CCallBridgeAddress(int) uintptr { return 0 }
