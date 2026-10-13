//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile

import (
	"reflect"

	"github.com/moriyoshi/nyanjit/backend/execution"
)

// cCallBridgeAddress returns a Go ABI gateway for a scalar C call of the
// requested arity. Managed generated code can call only ordinary Go functions
// with fixed signatures; the gateway passes the call to execution.CallForeign,
// which performs the runtime-aware Go-to-C transition.
func cCallBridgeAddress(arity int) uintptr {
	if arity < 0 || arity >= len(cCallBridges) {
		return 0
	}
	return cCallBridges[arity]
}

var cCallBridges = [...]uintptr{
	reflect.ValueOf(cCallBridge0).Pointer(),
	reflect.ValueOf(cCallBridge1).Pointer(),
	reflect.ValueOf(cCallBridge2).Pointer(),
	reflect.ValueOf(cCallBridge3).Pointer(),
	reflect.ValueOf(cCallBridge4).Pointer(),
	reflect.ValueOf(cCallBridge5).Pointer(),
	reflect.ValueOf(cCallBridge6).Pointer(),
	reflect.ValueOf(cCallBridge7).Pointer(),
	reflect.ValueOf(cCallBridge8).Pointer(),
}

func cCallBridge0(target uintptr) uintptr {
	return execution.CallForeign(target, []uintptr{})
}

func cCallBridge1(target uintptr, a0 uintptr) uintptr {
	return execution.CallForeign(target, []uintptr{a0})
}

func cCallBridge2(target uintptr, a0 uintptr, a1 uintptr) uintptr {
	return execution.CallForeign(target, []uintptr{a0, a1})
}

func cCallBridge3(target uintptr, a0 uintptr, a1 uintptr, a2 uintptr) uintptr {
	return execution.CallForeign(target, []uintptr{a0, a1, a2})
}

func cCallBridge4(target uintptr, a0 uintptr, a1 uintptr, a2 uintptr, a3 uintptr) uintptr {
	return execution.CallForeign(target, []uintptr{a0, a1, a2, a3})
}

func cCallBridge5(target uintptr, a0 uintptr, a1 uintptr, a2 uintptr, a3 uintptr, a4 uintptr) uintptr {
	return execution.CallForeign(target, []uintptr{a0, a1, a2, a3, a4})
}

func cCallBridge6(target uintptr, a0 uintptr, a1 uintptr, a2 uintptr, a3 uintptr, a4 uintptr, a5 uintptr) uintptr {
	return execution.CallForeign(target, []uintptr{a0, a1, a2, a3, a4, a5})
}

func cCallBridge7(target uintptr, a0 uintptr, a1 uintptr, a2 uintptr, a3 uintptr, a4 uintptr, a5 uintptr, a6 uintptr) uintptr {
	return execution.CallForeign(target, []uintptr{a0, a1, a2, a3, a4, a5, a6})
}

func cCallBridge8(target uintptr, a0 uintptr, a1 uintptr, a2 uintptr, a3 uintptr, a4 uintptr, a5 uintptr, a6 uintptr, a7 uintptr) uintptr {
	return execution.CallForeign(target, []uintptr{a0, a1, a2, a3, a4, a5, a6, a7})
}
