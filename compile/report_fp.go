package compile

import (
	"strconv"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	targetloong64 "github.com/moriyoshi/nyanjit/backend/target/loong64"
	"github.com/moriyoshi/nyanjit/ir"
)

// vmxRegisterNames names the ppc64 VMX registers that C entries save. They
// are a register file of their own, outside backend's vector registers, so
// the target does not name them.
var vmxRegisterNames = func() (names [256]string) {
	for n := range names {
		names[n] = "V" + strconv.Itoa(n)
	}
	return
}()

// floatWriteKey holds every Instruction field machine.FloatWrites reads, so
// instructions with equal keys write the same FP registers. Its field names
// match Instruction's; TestFloatWriteKeyCoversInputs checks that no other
// field changes the result.
type floatWriteKey struct {
	Op         machine.Opcode
	Bits       uint8
	Arithmetic ir.Opcode
	A, B, C, D backend.Register
}

// cachesFloatWrites reports the opcodes worth caching: those FloatWrites can
// report. Others still call it, so a missing opcode costs only the cache.
func cachesFloatWrites(op machine.Opcode) bool {
	switch op {
	case machine.OpLiteralLoad, machine.OpFloatReload, machine.OpFloatMove,
		machine.OpFloatBinary, machine.OpFloatUnary, machine.OpFloatFMA, machine.OpFloatBits:
		return true
	}
	return false
}

func fpStatusModifiedFromPhysical(program physicalProgram) bool {
	for _, located := range program.insns {
		switch located.ins.Op {
		case pFloatUnary, pFloatBinary, pFloatFMA, pIntToFloat, pFloatToInt, pFloatCompare, pCallC, pCallGo:
			return true
		}
	}
	return false
}

// fpClobbersFromPhysical reports floating-point registers modified by calls
// and by floating arithmetic in the generated body. C entry saves are removed
// because their original values are restored on every return path.
func fpClobbersFromPhysical(program physicalProgram, emitter machineEmitter, target machineTarget) ([]string, error) {
	architecture := target.Architecture()
	seen := make(map[string]bool)
	var result []string
	// Register assignments repeat, so a body has few distinct keys. A repeated
	// key adds no new name, and first-seen order is unchanged by skipping it.
	// The value records whether the key writes any FP register.
	floatWritten := make(map[floatWriteKey]bool)
	for _, located := range program.insns {
		ins := &located.ins
		key := floatWriteKey{Op: ins.Op, Bits: ins.Bits, Arithmetic: ins.Arithmetic, A: ins.A, B: ins.B, C: ins.C, D: ins.D}
		cacheable := cachesFloatWrites(ins.Op)
		writes, known := false, false
		if cacheable {
			writes, known = floatWritten[key]
		}
		if !known {
			regs := machine.FloatWrites(*ins, architecture)
			writes = len(regs) > 0
			if cacheable {
				floatWritten[key] = writes
			}
			for _, reg := range regs {
				name := target.RegisterName(reg)
				if !seen[name] {
					seen[name] = true
					result = append(result, name)
				}
			}
		}
		if writes {
			continue
		}
		flavor := abi.C
		switch located.ins.Op {
		case machine.OpLiteralLoad, machine.OpVector, machine.OpVectorMove, machine.OpVectorSpill, machine.OpVectorReload:
			ins := located.ins
			var registerBuffer [4]backend.Register
			registers := registerBuffer[:0]
			if backend.IsVectorRegister(ins.A) && ins.Op != machine.OpVectorSpill && !(ins.Op == machine.OpVector && ins.Arithmetic == ir.OpVStore) {
				registers = append(registers, ins.A)
			}
			rotate := ins.Op == machine.OpVector && (ins.Arithmetic == ir.OpRotateLeft || ins.Arithmetic == ir.OpRotateRight) && ins.Imm%32 != 0
			if (architecture == "amd64" || architecture == "386") && (ins.Arithmetic == ir.OpVExtract || ins.Arithmetic == ir.OpVInsertZero || rotate) {
				registers = append(registers, backend.VectorRegister(0))
			}
			if architecture == "386" && ins.Op == machine.OpVector {
				if ins.Bits == 64 && (ins.Arithmetic == ir.OpVSplat || ins.Arithmetic == ir.OpVExtract || ins.Arithmetic == ir.OpVInsertZero) {
					registers = append(registers, backend.VectorRegister(2))
				}
				switch ins.Arithmetic {
				case ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpVCompare, ir.OpVCarrylessMul:
					if ins.A == ins.C && ins.A != ins.B {
						registers = append(registers, backend.VectorRegister(1))
					}
				}
			}
			if architecture == "arm64" && ins.Arithmetic == ir.OpVMaskBits {
				registers = append(registers, backend.VectorRegister(30), backend.VectorRegister(31))
			} else if architecture == "arm64" && rotate && ins.A == ins.B {
				registers = append(registers, backend.VectorRegister(30))
			}
			if architecture == "loong64" && ins.Op != machine.OpLiteralLoad {
				registers = targetloong64.VectorWrites(ins)
			}
			for _, reg := range registers {
				name := target.RegisterName(reg)
				if !seen[name] {
					seen[name] = true
					result = append(result, name)
				}
			}
			continue
		case pCallC:
		case pCallGo:
			flavor = abi.Go
		default:
			continue
		}
		effect, err := cachedCallEffect(abi.CallSpec{Architecture: architecture, Kind: abi.FunctionCall, ABI: flavor})
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
		// Entry float saves number the FP registers that the vector
		// registers alias.
		for _, reg := range physical.entryFloatSaves {
			delete(seen, target.RegisterName(backend.VectorRegister(uint8(reg))))
		}
		for _, save := range physical.entryResources {
			if save.Save == machine.OpSaveCVector {
				delete(seen, vmxRegisterNames[save.Register])
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
