package compile

import (
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/regalloc"
)

func floatRegisters(arch string) []physReg {
	if arch == "amd64" || arch == "arm64" {
		return machine.VectorRegisters(arch)
	}
	var out []physReg
	if arch == "ppc64" || arch == "ppc64le" {
		for n := uint8(3); n < 14; n++ {
			out = append(out, machine.VectorRegister(n))
		}
	}
	if arch == "riscv64" {
		for _, pair := range [][2]uint8{{3, 8}, {10, 18}, {28, 32}} {
			for n := pair[0]; n < pair[1]; n++ {
				out = append(out, machine.VectorRegister(n))
			}
		}
	}
	return out
}
func fpInstruction(i machineInstruction) bool {
	return i.Op == mFloatBinary || i.Op == mFloatUnary || i.Op == mFloatFMA || i.Op == machine.SelectedFloatBits
}

// assignFloatResidency keeps FP-producing values in their shared FP/vector
// register file. Integer edges get bit-preserving copies, including Select,
// bit manipulation, outgoing arguments, scalar label homes and returns.
func assignFloatResidency(program machineProgram) machineProgram {
	program.Values = append([]machineValue(nil), program.Values...)
	original := append([]machineInstruction(nil), program.Instructions...)
	fpUses := make([]int, len(program.Values))
	for _, i := range original {
		if fpInstruction(i) {
			for _, id := range i.Inputs() {
				fpUses[id]++
			}
		}
	}
	for _, i := range original {
		if fpInstruction(i) {
			program.Values[i.Dst].Class = regalloc.Float
			program.Values[i.Dst].Bits = i.Bits
		}
	}
	// Keep sign/absolute-value operations in FP when their result feeds FP.
	for n := range original {
		i := original[n]
		if i.Dst < 0 || fpUses[i.Dst] == 0 || i.Bits != 32 && i.Bits != 64 {
			continue
		}
		if i.Op == mBinaryImmediate && (i.Arithmetic == ir.OpAnd || i.Arithmetic == ir.OpXor) {
		} else if i.Op == mBinary && (i.Arithmetic == ir.OpAnd || i.Arithmetic == ir.OpXor) && program.Values[i.B].Constant && program.Values[i.B].Symbol == "" {
			i.Imm = program.Values[i.B].Imm
		} else {
			continue
		}
		sign := uint64(1) << (i.Bits - 1)
		if i.Arithmetic == ir.OpXor && i.Imm != sign || i.Arithmetic == ir.OpAnd && i.Imm != sign-1 {
			continue
		}
		i.Op = machine.SelectedFloatBits
		original[n] = i
		program.Values[i.Dst].Class = regalloc.Float
		program.Values[i.Dst].Bits = i.Bits
	}
	width := func(id int) uint8 {
		w := program.Values[id].Bits
		if w == 0 {
			return 64
		}
		return w
	}
	// Source IDs are immutable, so bank copies can be reused within a block.
	type floatCopyKey struct {
		id   int
		bits uint8
	}
	general, float := map[int]int{}, map[floatCopyKey]int{}
	var out []machineInstruction
	addValue := func(bits uint8, class regalloc.Class) int {
		id := len(program.Values)
		program.Values = append(program.Values, machineValue{Class: class, Words: 1, Bits: bits, Param: -1, EntrySlot: -1, EntryReg: -1})
		return id
	}
	toGeneral := func(id int) int {
		if program.Values[id].Class != regalloc.Float {
			return id
		}
		if copy, ok := general[id]; ok {
			return copy
		}
		copy := addValue(width(id), regalloc.General)
		out = append(out, machineInstruction{Op: machine.SelectedFloatMove, Bits: width(id), Dst: copy, A: id})
		general[id] = copy
		return copy
	}
	toFloat := func(id int, bits uint8) int {
		if program.Values[id].Class == regalloc.Float && width(id) == bits {
			return id
		}
		key := floatCopyKey{id, bits}
		if copy, ok := float[key]; ok {
			return copy
		}
		if program.Values[id].Class == regalloc.Float {
			id = toGeneral(id)
		}
		copy := addValue(bits, regalloc.Float)
		if program.Values[id].Constant && program.Values[id].Symbol == "" {
			program.Values[copy].Constant = true
			program.Values[copy].Imm = program.Values[id].Imm
			out = append(out, machineInstruction{Op: mConst, Bits: bits, Dst: copy, Imm: program.Values[id].Imm})
		} else {
			out = append(out, machineInstruction{Op: machine.SelectedFloatMove, Bits: bits, Dst: copy, A: id})
		}
		float[key] = copy
		return copy
	}
	boundary := make([]int, len(original)+1)
	for pos, i := range original {
		boundary[pos] = len(out)
		inputs := i.Inputs()
		mapped := make(map[int]int, len(inputs))
		for _, id := range inputs {
			if fpInstruction(i) || i.Op == machine.SelectedHomeSet && program.HomePlan != nil && program.HomePlan.FloatHomes[i.Variable] {
				bits := i.Bits
				if i.Op == mFloatUnary && i.Arithmetic == ir.OpFConvert {
					bits = 96 - bits
				}
				mapped[id] = toFloat(id, bits)
			} else {
				mapped[id] = toGeneral(id)
			}
		}
		replace := func(id int) int {
			if v, ok := mapped[id]; ok {
				return v
			}
			return id
		}
		switch i.Op {
		case mConst, mSymbol: // no operands
		case mCallC, mCallGo, mSyscall:
			i.Args = append([]int(nil), i.Args...)
			for n := range i.Args {
				i.Args[n] = replace(i.Args[n])
			}
		case mWordCarry:
			if len(i.Args) > 0 {
				i.Args = append([]int(nil), i.Args...)
				for n := range i.Args {
					i.Args[n] = replace(i.Args[n])
				}
			} else {
				i.A, i.B, i.C = replace(i.A), replace(i.B), replace(i.C)
			}
		default:
			i.A, i.B, i.C = replace(i.A), replace(i.B), replace(i.C)
		}
		out = append(out, i)
	}
	boundary[len(original)] = len(out)
	if !program.DiscardResult {
		if program.ResultAsFloat {
			program.Result = toFloat(program.Result, width(program.Result))
		} else {
			program.Result = toGeneral(program.Result)
		}
	}
	program.Exports = append([]int(nil), program.Exports...)
	for n, id := range program.Exports {
		if !program.DirectExports || program.ExportRegisters[n] < 0 || !machine.IsVectorRegister(machine.Register(program.ExportRegisters[n])) {
			program.Exports[n] = toGeneral(id)
		} else {
			program.Exports[n] = toFloat(id, width(id))
		}
	}
	program.ResultParts = append([]int(nil), program.ResultParts...)
	for n, id := range program.ResultParts {
		program.ResultParts[n] = toGeneral(id)
	}
	// Integer branch/ABI hints belong to the integer copy, not the original
	// FP-producing value. Preserve explicit FP hints in their own register file.
	hints := make(map[int]machine.Register, len(program.PreferredRegisters))
	for id, reg := range program.PreferredRegisters {
		if id >= 0 && id < len(program.Values) && program.Values[id].Class != regalloc.Float && machine.IsVectorRegister(reg) {
			if copy, ok := float[floatCopyKey{id, width(id)}]; ok {
				hints[copy] = reg
				continue
			}
		}
		if id >= 0 && id < len(program.Values) && program.Values[id].Class == regalloc.Float && !machine.IsVectorRegister(reg) {
			if copy, ok := general[id]; ok {
				hints[copy] = reg
			}
			continue
		}
		hints[id] = reg
	}
	program.PreferredRegisters = hints
	program.Scopes = append([]machine.SelectedScope(nil), program.Scopes...)
	for n := range program.Scopes {
		s := &program.Scopes[n]
		s.Start, s.End = boundary[s.Start], boundary[s.End]
	}
	program.Instructions = out
	return program
}
