package compile

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

func floatRegisters(arch string) []physReg {
	if arch == "amd64" || arch == "arm64" || arch == "386" || arch == "loong64" {
		return floatRegisterSets[arch]
	}
	switch arch {
	case "ppc64le":
		return ppcFloatRegisters[:]
	case "riscv64":
		return riscvFloatRegisters[:]
	}
	return nil
}
func fpInstruction(i machineInstruction) bool {
	return i.Op == mFloatBinary || i.Op == mFloatUnary || i.Op == mFloatFMA || i.Op == machine.SelectedFloatBits
}

// assignFloatResidency keeps FP-producing values in their shared FP/vector
// register file. Integer edges get bit-preserving copies, including Select,
// bit manipulation, outgoing arguments, scalar label homes and returns.
func assignFloatResidency(program machineProgram, wordBits uint8) machineProgram {
	needed := program.ResultAsFloat
	needed = needed || program.HomePlan.NeedsFloating()
	for _, v := range program.Values {
		needed = needed || v.Class == backend.Float
	}
	for _, i := range program.Instructions {
		needed = needed || fpInstruction(i) || i.Op == mIntToFloat || i.Op == mFloatToInt || i.Op == mFloatCompare
	}
	if program.DirectExports {
		for _, r := range program.ExportRegisters {
			needed = needed || r >= 0 && backend.IsVectorRegister(backend.Register(r))
		}
	}
	if !needed {
		return program
	}
	program.Values = append([]machineValue(nil), program.Values...)
	original := append([]machineInstruction(nil), program.Instructions...)
	var operandBuffer [4]int
	fpUses := make([]int, len(program.Values))
	for _, i := range original {
		if fpInstruction(i) || i.Op == mFloatToInt || i.Op == mFloatCompare {
			for _, id := range i.InputsInto(operandBuffer[:0]) {
				fpUses[id]++
			}
		}
	}
	for _, i := range original {
		// A conversion from an integer has a float result.
		if fpInstruction(i) || i.Op == mIntToFloat {
			program.Values[i.Dst].Class = backend.Float
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
		program.Values[i.Dst].Class = backend.Float
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
	out := make([]machineInstruction, 0, len(original))
	addValue := func(bits uint8, class backend.Class) int {
		id := len(program.Values)
		program.Values = append(program.Values, machineValue{Class: class, Words: (bits + wordBits - 1) / wordBits, Bits: bits, Param: -1, EntrySlot: -1, EntryReg: -1})
		return id
	}
	toGeneral := func(id int) int {
		if program.Values[id].Class != backend.Float {
			return id
		}
		if copy, ok := general[id]; ok {
			return copy
		}
		copy := addValue(width(id), backend.General)
		out = append(out, machineInstruction{Op: machine.SelectedFloatMove, Bits: width(id), Dst: copy, A: id})
		general[id] = copy
		return copy
	}
	toFloat := func(id int, bits uint8) int {
		if program.Values[id].Class == backend.Float && width(id) == bits {
			return id
		}
		key := floatCopyKey{id, bits}
		if copy, ok := float[key]; ok {
			return copy
		}
		if program.Values[id].Class == backend.Float {
			id = toGeneral(id)
		}
		copy := addValue(bits, backend.Float)
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
		inputs := i.InputsInto(operandBuffer[:0])
		var mappedBuffer [4]int
		mapped := mappedBuffer[:0]
		if len(inputs) > cap(mapped) {
			mapped = make([]int, 0, len(inputs))
		}
		for _, id := range inputs {
			if i.Op == mFloatToInt {
				// A conversion to an integer reads a float of its source width.
				mapped = append(mapped, toFloat(id, uint8(i.Disp)))
			} else if i.Op == mFloatCompare {
				mapped = append(mapped, toFloat(id, i.Bits))
			} else if fpInstruction(i) || i.Op == machine.SelectedHomeSet && program.HomePlan != nil && program.HomePlan.FloatHomes[i.Variable] {
				bits := i.Bits
				if i.Op == mFloatUnary && i.Arithmetic == ir.OpFConvert {
					bits = 96 - bits
				}
				mapped = append(mapped, toFloat(id, bits))
			} else {
				mapped = append(mapped, toGeneral(id))
			}
		}
		replace := func(id int) int {
			for n, input := range inputs {
				if input == id {
					return mapped[n]
				}
			}
			return id
		}
		switch i.Op {
		case mVector:
			i.A, i.B, i.C = replace(i.A), replace(i.B), replace(i.C)
			if i.Args != nil {
				i.Args = append([]int(nil), i.Args...)
				for n := range i.Args {
					i.Args[n] = replace(i.Args[n])
				}
			}
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
		if !program.DirectExports || program.ExportRegisters[n] < 0 || !backend.IsVectorRegister(backend.Register(program.ExportRegisters[n])) {
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
	hints := make(map[int]backend.Register, len(program.PreferredRegisters))
	for id, reg := range program.PreferredRegisters {
		if id >= 0 && id < len(program.Values) && program.Values[id].Class != backend.Float && backend.IsVectorRegister(reg) {
			if copy, ok := float[floatCopyKey{id, width(id)}]; ok {
				hints[copy] = reg
				continue
			}
		}
		if id >= 0 && id < len(program.Values) && program.Values[id].Class == backend.Float && !backend.IsVectorRegister(reg) {
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

var ppcFloatRegisters = [...]physReg{backend.VectorRegister(3), backend.VectorRegister(4), backend.VectorRegister(5), backend.VectorRegister(6), backend.VectorRegister(7), backend.VectorRegister(8), backend.VectorRegister(9), backend.VectorRegister(10), backend.VectorRegister(11), backend.VectorRegister(12), backend.VectorRegister(13)}
var riscvFloatRegisters = [...]physReg{backend.VectorRegister(3), backend.VectorRegister(4), backend.VectorRegister(5), backend.VectorRegister(6), backend.VectorRegister(7), backend.VectorRegister(10), backend.VectorRegister(11), backend.VectorRegister(12), backend.VectorRegister(13), backend.VectorRegister(14), backend.VectorRegister(15), backend.VectorRegister(16), backend.VectorRegister(17), backend.VectorRegister(28), backend.VectorRegister(29), backend.VectorRegister(30), backend.VectorRegister(31)}

var floatRegisterSets = func() map[string][]physReg {
	out := make(map[string][]physReg, 4)
	for _, arch := range []string{"amd64", "arm64", "386", "loong64"} {
		out[arch] = machine.VectorRegisters(arch).AppendTo(nil)
		out[arch] = out[arch][:len(out[arch]):len(out[arch])]
	}
	return out
}()
