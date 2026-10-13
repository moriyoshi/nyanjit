//go:build !linux || !386

package compile

func cdeclCallBridgeAddress(int) uintptr { return 0 }
