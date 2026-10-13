package machine

// FloatWrites reports FP registers actually written by scalar expansion plans.
// Numbers use the same FP/vector file ordinal as VectorRegister.
func FloatWrites(i Instruction, arch string) []Register {
	var out []Register
	seen := map[Register]bool{}
	add := func(n uint8) {
		r := VectorRegister(n)
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	if i.Op == OpLiteralLoad || i.Op == OpFloatReload {
		add(VectorNumber(i.A))
		return out
	}
	if i.Op == OpFloatMove {
		if IsVectorRegister(i.A) {
			if i.A != i.B {
				add(VectorNumber(i.A))
			}
		} else if (arch == "ppc64" || arch == "ppc64le") && i.Bits == 32 {
			add(0)
		}
		return out
	}
	if i.Op != OpFloatBinary && i.Op != OpFloatUnary && i.Op != OpFloatFMA && i.Op != OpFloatBits {
		return nil
	}
	temp := uint8(0)
	if arch == "arm64" {
		temp = 16
	}
	if i.Op == OpFloatFMA && arch == "amd64" {
		if !IsVectorRegister(i.D) {
			add(0)
		}
		if !IsVectorRegister(i.B) {
			add(1)
		}
		if !IsVectorRegister(i.C) {
			add(2)
		}
	} else {
		if !IsVectorRegister(i.B) {
			if arch == "amd64" && i.Op == OpFloatBits {
				add(1)
			} else {
				add(temp)
			}
		}
		if i.Op == OpFloatBinary || i.Op == OpFloatFMA {
			if !IsVectorRegister(i.C) {
				add(temp + 1)
			}
		}
		if i.Op == OpFloatFMA && !IsVectorRegister(i.D) {
			add(temp + 2)
		}
	}
	if IsVectorRegister(i.A) {
		add(VectorNumber(i.A))
	} else {
		add(temp)
	}
	if (arch == "ppc64" || arch == "ppc64le") && !IsVectorRegister(i.A) && i.Bits == 32 {
		add(0)
	}
	return out
}
