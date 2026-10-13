//go:build !linux || (!amd64 && !arm64 && !386 && !riscv64 && !ppc64le && !loong64)

package compile

func cCallBridgeAddress(int) uintptr { return 0 }
