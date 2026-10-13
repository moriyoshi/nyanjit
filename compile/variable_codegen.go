package compile

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// Reserved offsets do not require storage until an instruction accesses them.
func referencedSpillSlots(insns []machine.Instruction, wordBits ...uint8) int {
	word := 64
	if len(wordBits) != 0 {
		word = int(wordBits[0])
	}
	slots := 0
	for _, ins := range insns {
		words := 0
		switch ins.Op {
		case pSpill, pReload, pSaveLink, pRestoreLink, machine.OpSaveCondition, machine.OpRestoreCondition:
			words = 1
		case pSaveCFloat, pRestoreCFloat:
			words = 64 / word
		case machine.OpFloatSpill, machine.OpFloatReload:
			bits := int(ins.Bits)
			if bits == 0 {
				bits = 64
			}
			words = (bits + word - 1) / word
		case machine.OpVectorSpill, machine.OpVectorReload, machine.OpSaveCVector, machine.OpRestoreCVector:
			words = 128 / word
		}
		if words != 0 {
			slots = max(slots, ins.Slot+words)
		}
	}
	return slots
}
func workspaceBytesForSlots(target machineTarget, emitter machineEmitter, slots int) int {
	if emitter.(*physicalEmitter).stack == abi.StackNoUse {
		return slots * int(target.RegisterBits()/8)
	}
	return 0
}

// scopeTemporaryDemand measures simultaneously live ordinary values. Home
// operands and an immediately committed Set destination require no temporary.
func scopeTemporaryDemand(p *ir.Program, wordBits uint8, context ...*programFacts) []int {
	facts := factsFor(p, context)
	var demand, ending []int
	return scopeTemporaryDemandInto(p, wordBits, facts, &demand, &ending)
}
func scopeTemporaryDemandInto(p *ir.Program, wordBits uint8, facts *programFacts, demandBuffer, endingBuffer *[]int) []int {
	demand := resetBuffer(demandBuffer, len(facts.Scopes()))
	live, ending := 0, resetBuffer(endingBuffer, len(p.Instructions)+1)
	for pos, i := range p.Instructions {
		region := facts.ScopeIndex(facts.ScopeAt(pos))
		words := (int(instructionBits(i)) + int(wordBits) - 1) / int(wordBits)
		temporary := true
		switch i.Op {
		case ir.OpConst, ir.OpGet, ir.OpSet, ir.OpScopeEnter, ir.OpScopeLeave, ir.OpStore64, ir.OpStore8, ir.OpStore16, ir.OpStore32, ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv, ir.OpFMA, ir.OpFSqrt, ir.OpFConvert, ir.OpFProductError:
			temporary = false
		}
		if pos+1 < len(p.Instructions) && p.Instructions[pos+1].Op == ir.OpSet && p.Instructions[pos+1].A == pos {
			temporary = false
		}
		if temporary && facts.LastUse(pos) > pos {
			live += words
			ending[facts.LastUse(pos)] += words
		}
		demand[region] = max(1, demand[region], live)
		live -= ending[pos]
	}
	return demand
}

func emitHomeMove(e machineEmitter, bits uint8, dst, src physReg) {
	if dst == src {
		return
	}
	if backend.IsVectorRegister(dst) || backend.IsVectorRegister(src) {
		e.(*physicalEmitter).append(physicalInsn{Op: machine.OpFloatMove, Bits: bits, A: dst, B: src})
	} else {
		e.move(dst, src)
	}
}
func emitHomeSpill(e machineEmitter, r physReg, slot int, width ...uint8) {
	bits := uint8(64)
	if len(width) != 0 && width[0] != 0 {
		bits = width[0]
	}
	if backend.IsVectorRegister(r) {
		e.(*physicalEmitter).append(physicalInsn{Op: machine.OpFloatSpill, Bits: bits, A: r, Slot: slot})
	} else {
		e.spill(r, slot)
	}
}
func emitHomeReload(e machineEmitter, r physReg, slot int, width ...uint8) {
	bits := uint8(64)
	if len(width) != 0 && width[0] != 0 {
		bits = width[0]
	}
	if backend.IsVectorRegister(r) {
		e.(*physicalEmitter).append(physicalInsn{Op: machine.OpFloatReload, Bits: bits, A: r, Slot: slot})
	} else {
		e.reload(r, slot)
	}
}

func emitHomeConstant(e machineEmitter, t machineTarget, r physReg, value uint64) {
	emitTypedHomeConstant(e, t, 64, r, value)
}
func emitTypedHomeConstant(e machineEmitter, t machineTarget, bits uint8, r physReg, value uint64) {
	if bits == 0 {
		bits = 64
	}
	if backend.IsVectorRegister(r) {
		e.(*physicalEmitter).literalValue(r, bits, value)
	} else {
		e.constant(r, value)
	}
}

// Floating arithmetic competes for its own register bank. This lookahead is
// separate from integer demand; ordinary integer operations cannot evict an FP
// home just because its scope has many integer temporaries.
func scopeFloatTemporaryDemand(p *ir.Program, context ...*programFacts) []int {
	facts := factsFor(p, context)
	var demand, ending []int
	return scopeFloatTemporaryDemandInto(p, facts, &demand, &ending)
}
func scopeFloatTemporaryDemandInto(p *ir.Program, facts *programFacts, demandBuffer, endingBuffer *[]int) []int {
	var inputBuffer [4]int
	demand := resetBuffer(demandBuffer, len(facts.Scopes()))
	live := 0
	ending := resetBuffer(endingBuffer, len(p.Instructions)+1)
	for pos, i := range p.Instructions {
		floating := false
		switch i.Op {
		case ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv, ir.OpFMA, ir.OpFSqrt, ir.OpFConvert, ir.OpFProductError:
			floating = true
		}
		instructionTemps := 0
		if floating {
			instructionTemps = len(i.InputsInto(inputBuffer[:0])) + 1
		}
		if pos+1 < len(p.Instructions) && p.Instructions[pos+1].Op == ir.OpSet && p.Instructions[pos+1].A == pos {
			floating = false
		}
		if floating && facts.LastUse(pos) > pos {
			live++
			ending[facts.LastUse(pos)]++
		}
		region := facts.ScopeIndex(facts.ScopeAt(pos))
		demand[region] = max(2, demand[region], live, instructionTemps)
		live -= ending[pos]
	}
	return demand
}
