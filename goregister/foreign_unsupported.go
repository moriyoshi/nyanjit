//go:build !linux || !ppc64

package goregister

import "unsafe"

func ForeignCallSupported() bool                       { return false }
func ForeignCall(unsafe.Pointer, unsafe.Pointer) int32 { panic(ErrUnsupported) }
