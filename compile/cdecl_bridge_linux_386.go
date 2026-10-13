//go:build linux && 386

package compile

import (
	"github.com/moriyoshi/nyanjit/execution"
	"reflect"
)

// The compiler's uint64-returning gateway adapts the native-word execution
// boundary without losing EDX. Bit i of the encoded address's high word says
// logical argument i occupies two cdecl words. All Go helper slots are uint64.
func cdeclForeignCall(encoded uint64, args ...uint64) uint64 {
	words := make([]uintptr, 0, len(args)*2)
	for i, arg := range args {
		words = append(words, uintptr(arg))
		if (encoded>>32)&(1<<i) != 0 {
			words = append(words, uintptr(arg>>32))
		}
	}
	lo, hi := execution.CallForeignPair(uintptr(encoded), words)
	return execution.Wide(lo, hi)
}
func cdeclCallBridgeAddress(arity int) uintptr {
	if arity < 0 || arity >= len(cdeclCallBridges) {
		return 0
	}
	return cdeclCallBridges[arity]
}

var cdeclCallBridges = [...]uintptr{
	reflect.ValueOf(cdeclCallBridge0).Pointer(),
	reflect.ValueOf(cdeclCallBridge1).Pointer(),
	reflect.ValueOf(cdeclCallBridge2).Pointer(),
	reflect.ValueOf(cdeclCallBridge3).Pointer(),
	reflect.ValueOf(cdeclCallBridge4).Pointer(),
	reflect.ValueOf(cdeclCallBridge5).Pointer(),
	reflect.ValueOf(cdeclCallBridge6).Pointer(),
	reflect.ValueOf(cdeclCallBridge7).Pointer(),
	reflect.ValueOf(cdeclCallBridge8).Pointer(),
}

func cdeclCallBridge0(encoded uint64) uint64 {
	return cdeclForeignCall(encoded)
}

func cdeclCallBridge1(encoded uint64, a0 uint64) uint64 {
	return cdeclForeignCall(encoded, a0)
}

func cdeclCallBridge2(encoded uint64, a0 uint64, a1 uint64) uint64 {
	return cdeclForeignCall(encoded, a0, a1)
}

func cdeclCallBridge3(encoded uint64, a0 uint64, a1 uint64, a2 uint64) uint64 {
	return cdeclForeignCall(encoded, a0, a1, a2)
}

func cdeclCallBridge4(encoded uint64, a0 uint64, a1 uint64, a2 uint64, a3 uint64) uint64 {
	return cdeclForeignCall(encoded, a0, a1, a2, a3)
}

func cdeclCallBridge5(encoded uint64, a0 uint64, a1 uint64, a2 uint64, a3 uint64, a4 uint64) uint64 {
	return cdeclForeignCall(encoded, a0, a1, a2, a3, a4)
}

func cdeclCallBridge6(encoded uint64, a0 uint64, a1 uint64, a2 uint64, a3 uint64, a4 uint64, a5 uint64) uint64 {
	return cdeclForeignCall(encoded, a0, a1, a2, a3, a4, a5)
}

func cdeclCallBridge7(encoded uint64, a0 uint64, a1 uint64, a2 uint64, a3 uint64, a4 uint64, a5 uint64, a6 uint64) uint64 {
	return cdeclForeignCall(encoded, a0, a1, a2, a3, a4, a5, a6)
}

func cdeclCallBridge8(encoded uint64, a0 uint64, a1 uint64, a2 uint64, a3 uint64, a4 uint64, a5 uint64, a6 uint64, a7 uint64) uint64 {
	return cdeclForeignCall(encoded, a0, a1, a2, a3, a4, a5, a6, a7)
}
