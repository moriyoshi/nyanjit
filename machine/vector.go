package machine

import "github.com/moriyoshi/nyanjit/ir"

// VectorRegister identifies SIMD storage without aliasing a GPR ordinal.
func VectorRegister(number uint8) Register { return Register(64 + number) }

// IsVectorRegister reports the vector register file.
func IsVectorRegister(r Register) bool { return r >= 64 && r < 96 }

// VectorNumber returns the architecture's SIMD ordinal.
func VectorNumber(r Register) uint8 { return uint8(r) - 64 }

// VectorRegisters reserves scalar-FP and expansion temporaries, and uses only volatile whole-vector homes.
func VectorRegisters(arch string) []Register {
	out := []Register{}
	switch arch {
	case "amd64":
		// X15 is Go ABIInternal's zero register, including managed C bridges.
		for n := uint8(3); n < 15; n++ {
			out = append(out, VectorRegister(n))
		}
	case "arm64":
		for n := uint8(0); n < 8; n++ {
			out = append(out, VectorRegister(n))
		}
		for n := uint8(19); n < 30; n++ {
			out = append(out, VectorRegister(n))
		}
	}
	return out
}

// NativeVectorInstruction is the legalized 128-bit register operation.
func NativeVectorInstruction(i ir.Instruction) bool { return i.IsVector() && i.Op != ir.OpLabelParam }
