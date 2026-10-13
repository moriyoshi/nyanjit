package machine

import "github.com/moriyoshi/nyanjit/backend"

// buildVectorRegisters reserves scalar-FP and expansion temporaries.
func buildVectorRegisters(arch string) []backend.Register {
	out := []backend.Register{}
	switch arch {
	case "loong64":
		// Scalar FP and whole-vector homes share this volatile bank.
		// C preserves only 64 bits of f24-f31; exclude their vector aliases.
		for n := uint8(8); n < 24; n++ {
			out = append(out, backend.VectorRegister(n))
		}
	case "386":
		for n := uint8(3); n < 8; n++ {
			out = append(out, backend.VectorRegister(n))
		}
	case "amd64":
		// X15 is Go ABIInternal's zero register, including managed C bridges.
		for n := uint8(3); n < 15; n++ {
			out = append(out, backend.VectorRegister(n))
		}
	case "arm64":
		for n := uint8(0); n < 8; n++ {
			out = append(out, backend.VectorRegister(n))
		}
		for n := uint8(19); n < 30; n++ {
			out = append(out, backend.VectorRegister(n))
		}
	}
	return out
}

// VectorRegisters returns an immutable value of volatile whole-vector homes.
func VectorRegisters(arch string) backend.RegisterSet {
	if regs, ok := vectorRegisterSets[arch]; ok {
		return regs
	}
	return backend.RegisterSet{}
}

var vectorRegisterSets = func() map[string]backend.RegisterSet {
	out := make(map[string]backend.RegisterSet, 5)
	for _, arch := range []string{"arm64", "amd64", "386", "loong64"} {
		regs := buildVectorRegisters(arch)
		out[arch] = backend.NewRegisterSet(regs...)
	}
	return out
}()
