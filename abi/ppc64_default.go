package abi

// DefaultPPC64CABI returns the C convention matching the Go toolchain's default
// Linux executable ABI. An arbitrary foreign pointer can use another variant
// and must be described explicitly with a PPC64Profile.
func DefaultPPC64CABI(architecture string) (PPC64CABI, error) {
	switch architecture {
	case "ppc64":
		return defaultBigEndianPPC64CABI, nil
	case "ppc64le":
		return PPC64ELFv2, nil
	default:
		return PPC64CABIUnspecified, ErrUnsupported
	}
}
