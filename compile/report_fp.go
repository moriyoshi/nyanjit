package compile

import (
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"strconv"
)

func fpStatusModifiedFromPhysical(program physicalProgram) bool {
	for _, located := range program.insns {
		switch located.ins.Op {
		case pFloatUnary, pFloatBinary, pFloatFMA, pCallC, pCallGo:
			return true
		}
	}
	return false
}

// fpClobbersFromPhysical reports floating-point registers modified by calls
// and by floating arithmetic in the generated body. C entry saves are removed
// because their original values are restored on every return path.
func fpClobbersFromPhysical(program physicalProgram, emitter machineEmitter, architecture string) ([]string, error) {
	seen := make(map[string]bool)
	var result []string
	for _, located := range program.insns {
		if regs := machine.FloatWrites(located.ins, architecture); len(regs) > 0 {
			prefix := "F"
			if architecture == "amd64" {
				prefix = "XMM"
			}
			if architecture == "arm64" {
				prefix = "V"
			}
			for _, reg := range regs {
				name := prefix + strconv.Itoa(int(machine.VectorNumber(reg)))
				if !seen[name] {
					seen[name] = true
					result = append(result, name)
				}
			}
			continue
		}
		flavor := ABIC
		switch located.ins.Op {
		case machine.OpLiteralLoad, machine.OpVector, machine.OpVectorMove, machine.OpVectorSpill, machine.OpVectorReload:
			ins := located.ins
			registers := []machine.Register{}
			if machine.IsVectorRegister(ins.A) && ins.Op != machine.OpVectorSpill && !(ins.Op == machine.OpVector && ins.Arithmetic == ir.OpVStore) {
				registers = append(registers, ins.A)
			}
			if architecture == "amd64" && (ins.Arithmetic == ir.OpVExtract || ins.Arithmetic == ir.OpVInsertZero) {
				registers = append(registers, machine.VectorRegister(0))
			}
			if architecture == "arm64" && ins.Arithmetic == ir.OpVMaskBits {
				registers = append(registers, machine.VectorRegister(30), machine.VectorRegister(31))
			}
			prefix := "V"
			if architecture == "riscv64" || architecture == "ppc64" || architecture == "ppc64le" {
				prefix = "F"
			}
			if architecture == "amd64" {
				prefix = "XMM"
			}
			for _, reg := range registers {
				name := prefix + strconv.Itoa(int(machine.VectorNumber(reg)))
				if !seen[name] {
					seen[name] = true
					result = append(result, name)
				}
			}
			continue
		case pCallC:
		case pCallGo:
			flavor = ABIGo
		default:
			continue
		}
		effect, err := PreflightCall(CallSpec{Architecture: architecture, Kind: FunctionCall, ABI: flavor})
		if err != nil {
			return nil, err
		}
		for _, name := range effect.ClobberedFPRegisters {
			if !seen[name] {
				seen[name] = true
				result = append(result, name)
			}
		}
	}
	if physical, ok := emitter.(*physicalEmitter); ok {
		for _, reg := range physical.entryFloatSaves {
			prefix := "F"
			if architecture == "arm64" {
				prefix = "V"
			}
			delete(seen, prefix+strconv.Itoa(int(reg)))
		}
		for _, save := range physical.entryResources {
			if save.Save == machine.OpSaveCVector {
				delete(seen, "V"+strconv.Itoa(int(save.Register)))
			}
		}
	}
	filtered := result[:0]
	for _, name := range result {
		if seen[name] {
			filtered = append(filtered, name)
		}
	}
	return filtered, nil
}
