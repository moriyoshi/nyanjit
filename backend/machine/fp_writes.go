package machine

import (
	"slices"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// FloatWrites reports FP registers actually written by scalar expansion plans.
// Numbers use the same FP/vector file ordinal as VectorRegister.
func FloatWrites(i Instruction, arch string) []backend.Register {
	// At most six distinct registers are added; collect them on the stack and
	// return one exactly sized slice.
	var buffer [8]backend.Register
	out := buffer[:0]
	add := func(n uint8) {
		r := backend.VectorRegister(n)
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	if i.Op == OpLiteralLoad || i.Op == OpFloatReload {
		add(backend.VectorNumber(i.A))
		return cloneRegisters(out)
	}
	// A conversion writes its FP destination, and a conversion to an integer
	// on a target that converts within the FP bank writes its FP temporary,
	// f0, before moving the result out.
	if i.Op == OpIntToFloat {
		add(backend.VectorNumber(i.A))
		return cloneRegisters(out)
	}
	if i.Op == OpFloatToInt {
		if arch == "loong64" || arch == "ppc64le" {
			add(0)
			return cloneRegisters(out)
		}
		return nil
	}
	if i.Op == OpFloatMove {
		if arch == "386" && i.Bits == 64 && (!backend.IsVectorRegister(i.A) || !backend.IsVectorRegister(i.B)) {
			add(2)
		}
		if backend.IsVectorRegister(i.A) {
			if i.A != i.B {
				add(backend.VectorNumber(i.A))
			}
		} else if arch == "ppc64le" && i.Bits == 32 {
			add(0)
		}
		return cloneRegisters(out)
	}
	if i.Op != OpFloatBinary && i.Op != OpFloatUnary && i.Op != OpFloatFMA && i.Op != OpFloatBits {
		return nil
	}
	temp := uint8(0)
	if arch == "arm64" {
		temp = 16
	}
	if i.Op == OpFloatFMA && arch == "amd64" {
		if !backend.IsVectorRegister(i.D) {
			add(0)
		}
		if !backend.IsVectorRegister(i.B) {
			add(1)
		}
		if !backend.IsVectorRegister(i.C) {
			add(2)
		}
	} else {
		if !backend.IsVectorRegister(i.B) {
			if (arch == "amd64" || arch == "386") && i.Op == OpFloatBits {
				add(1)
			} else {
				add(temp)
			}
		}
		if i.Op == OpFloatBinary || i.Op == OpFloatFMA {
			if !backend.IsVectorRegister(i.C) {
				add(temp + 1)
			}
		}
		if i.Op == OpFloatFMA && !backend.IsVectorRegister(i.D) {
			add(temp + 2)
		}
	}
	if arch == "386" {
		inputWidth := i.Bits
		if i.Op == OpFloatUnary && i.Arithmetic == op.OpFConvert {
			inputWidth = 96 - i.Bits
		}
		if !backend.IsVectorRegister(i.B) && inputWidth == 64 || i.Op == OpFloatBinary && !backend.IsVectorRegister(i.C) && i.Bits == 64 || !backend.IsVectorRegister(i.A) && i.Bits == 64 {
			add(2)
		}
	}
	if backend.IsVectorRegister(i.A) {
		add(backend.VectorNumber(i.A))
	} else {
		add(temp)
	}
	if arch == "ppc64le" && !backend.IsVectorRegister(i.A) && i.Bits == 32 {
		add(0)
	}
	return cloneRegisters(out)
}

// cloneRegisters copies a stack-collected register list, keeping nil for none.
func cloneRegisters(regs []backend.Register) []backend.Register {
	if len(regs) == 0 {
		return nil
	}
	return slices.Clone(regs)
}
