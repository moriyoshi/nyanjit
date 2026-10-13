package execution

import (
	"github.com/moriyoshi/nyanjit/abi"
	"reflect"
	"runtime"
)

// PPC64CCallBridgeAddress returns a Go ABI helper for an explicitly selected
// PPC64 C pointer convention. Callbacks return through the corresponding
// runtime-aware C transition.
func PPC64CCallBridgeAddress(arity int, convention abi.PPC64CABI) uintptr {
	if runtime.GOARCH != "ppc64" && runtime.GOARCH != "ppc64le" {
		return 0
	}
	if convention == abi.PPC64CABIUnspecified {
		convention, _ = abi.DefaultPPC64CABI(runtime.GOARCH)
	}
	if _, err := abi.NewPPC64Profile(runtime.GOARCH, abi.C, convention); err != nil {
		return 0
	}
	var functions []any
	if convention == abi.PPC64ELFv1 {
		functions = []any{ppc64CCallV1_0, ppc64CCallV1_1, ppc64CCallV1_2, ppc64CCallV1_3, ppc64CCallV1_4, ppc64CCallV1_5, ppc64CCallV1_6, ppc64CCallV1_7, ppc64CCallV1_8}
	} else {
		functions = []any{ppc64CCallV2_0, ppc64CCallV2_1, ppc64CCallV2_2, ppc64CCallV2_3, ppc64CCallV2_4, ppc64CCallV2_5, ppc64CCallV2_6, ppc64CCallV2_7, ppc64CCallV2_8}
	}
	if arity < 0 || arity >= len(functions) {
		return 0
	}
	return reflect.ValueOf(functions[arity]).Pointer()
}

func ppc64CCall(target uintptr, args []uintptr, convention abi.PPC64CABI) uintptr {
	if runtime.GOARCH == "ppc64le" {
		return CallForeign(target, args)
	}
	return callForeignPPC64(target, args, convention)
}

func ppc64CCallV1_0(target uintptr) uintptr { return ppc64CCall(target, []uintptr{}, abi.PPC64ELFv1) }

func ppc64CCallV1_1(target uintptr, a0 uintptr) uintptr {
	return ppc64CCall(target, []uintptr{a0}, abi.PPC64ELFv1)
}

func ppc64CCallV1_2(target uintptr, a0 uintptr, a1 uintptr) uintptr {
	return ppc64CCall(target, []uintptr{a0, a1}, abi.PPC64ELFv1)
}

func ppc64CCallV1_3(target uintptr, a0 uintptr, a1 uintptr, a2 uintptr) uintptr {
	return ppc64CCall(target, []uintptr{a0, a1, a2}, abi.PPC64ELFv1)
}

func ppc64CCallV1_4(target uintptr, a0 uintptr, a1 uintptr, a2 uintptr, a3 uintptr) uintptr {
	return ppc64CCall(target, []uintptr{a0, a1, a2, a3}, abi.PPC64ELFv1)
}

func ppc64CCallV1_5(target uintptr, a0 uintptr, a1 uintptr, a2 uintptr, a3 uintptr, a4 uintptr) uintptr {
	return ppc64CCall(target, []uintptr{a0, a1, a2, a3, a4}, abi.PPC64ELFv1)
}

func ppc64CCallV1_6(target uintptr, a0 uintptr, a1 uintptr, a2 uintptr, a3 uintptr, a4 uintptr, a5 uintptr) uintptr {
	return ppc64CCall(target, []uintptr{a0, a1, a2, a3, a4, a5}, abi.PPC64ELFv1)
}

func ppc64CCallV1_7(target uintptr, a0 uintptr, a1 uintptr, a2 uintptr, a3 uintptr, a4 uintptr, a5 uintptr, a6 uintptr) uintptr {
	return ppc64CCall(target, []uintptr{a0, a1, a2, a3, a4, a5, a6}, abi.PPC64ELFv1)
}

func ppc64CCallV1_8(target uintptr, a0 uintptr, a1 uintptr, a2 uintptr, a3 uintptr, a4 uintptr, a5 uintptr, a6 uintptr, a7 uintptr) uintptr {
	return ppc64CCall(target, []uintptr{a0, a1, a2, a3, a4, a5, a6, a7}, abi.PPC64ELFv1)
}

func ppc64CCallV2_0(target uintptr) uintptr { return ppc64CCall(target, []uintptr{}, abi.PPC64ELFv2) }

func ppc64CCallV2_1(target uintptr, a0 uintptr) uintptr {
	return ppc64CCall(target, []uintptr{a0}, abi.PPC64ELFv2)
}

func ppc64CCallV2_2(target uintptr, a0 uintptr, a1 uintptr) uintptr {
	return ppc64CCall(target, []uintptr{a0, a1}, abi.PPC64ELFv2)
}

func ppc64CCallV2_3(target uintptr, a0 uintptr, a1 uintptr, a2 uintptr) uintptr {
	return ppc64CCall(target, []uintptr{a0, a1, a2}, abi.PPC64ELFv2)
}

func ppc64CCallV2_4(target uintptr, a0 uintptr, a1 uintptr, a2 uintptr, a3 uintptr) uintptr {
	return ppc64CCall(target, []uintptr{a0, a1, a2, a3}, abi.PPC64ELFv2)
}

func ppc64CCallV2_5(target uintptr, a0 uintptr, a1 uintptr, a2 uintptr, a3 uintptr, a4 uintptr) uintptr {
	return ppc64CCall(target, []uintptr{a0, a1, a2, a3, a4}, abi.PPC64ELFv2)
}

func ppc64CCallV2_6(target uintptr, a0 uintptr, a1 uintptr, a2 uintptr, a3 uintptr, a4 uintptr, a5 uintptr) uintptr {
	return ppc64CCall(target, []uintptr{a0, a1, a2, a3, a4, a5}, abi.PPC64ELFv2)
}

func ppc64CCallV2_7(target uintptr, a0 uintptr, a1 uintptr, a2 uintptr, a3 uintptr, a4 uintptr, a5 uintptr, a6 uintptr) uintptr {
	return ppc64CCall(target, []uintptr{a0, a1, a2, a3, a4, a5, a6}, abi.PPC64ELFv2)
}

func ppc64CCallV2_8(target uintptr, a0 uintptr, a1 uintptr, a2 uintptr, a3 uintptr, a4 uintptr, a5 uintptr, a6 uintptr, a7 uintptr) uintptr {
	return ppc64CCall(target, []uintptr{a0, a1, a2, a3, a4, a5, a6, a7}, abi.PPC64ELFv2)
}
