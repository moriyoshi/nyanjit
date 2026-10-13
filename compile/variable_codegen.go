package compile

import (
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

// Reserved offsets do not require storage until an instruction accesses them.
func referencedSpillSlots(insns []machine.Instruction) int {
	slots := 0
	for _, ins := range insns {
		words := 0
		switch ins.Op {
		case pSpill, pReload, pSaveLink, pRestoreLink, pSaveCFloat, pRestoreCFloat, machine.OpFloatSpill, machine.OpFloatReload, machine.OpSaveCondition, machine.OpRestoreCondition:
			words = 1
		case machine.OpVectorSpill, machine.OpVectorReload, machine.OpSaveCVector, machine.OpRestoreCVector:
			words = 2
		}
		if words != 0 {
			slots = max(slots, ins.Slot+words)
		}
	}
	return slots
}
func workspaceBytesForSlots(target machineTarget, emitter machineEmitter, slots int) int {
	if emitter.(*physicalEmitter).stack == StackNoUse {
		return slots * int(target.RegisterBits()/8)
	}
	return 0
}

// scopeTemporaryDemand measures simultaneously live ordinary values. Home
// operands and an immediately committed Set destination require no temporary.
func scopeTemporaryDemand(p *ir.Program, wordBits uint8) map[int]int {
	last := make([]int, len(p.Instructions))
	for id := range last {
		last[id] = id
	}
	for pos, i := range p.Instructions {
		for _, x := range i.Inputs() {
			last[x] = max(last[x], pos)
		}
	}
	if p.Control != nil {
		for _, step := range p.Control.Steps {
			if step.IsLabel {
				continue
			}
			for _, x := range append(append(append([]int{}, step.Values...), step.Args...), step.OtherArgs...) {
				last[x] = max(last[x], step.End)
			}
		}
	}
	demand := map[int]int{}
	live, ending := 0, map[int]int{}
	for pos, i := range p.Instructions {
		region := regionAt(p, pos)
		words := (int(instructionBits(i)) + int(wordBits) - 1) / int(wordBits)
		temporary := true
		switch i.Op {
		case ir.OpConst, ir.OpGet, ir.OpSet, ir.OpScopeEnter, ir.OpScopeLeave, ir.OpStore64, ir.OpStore8, ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv, ir.OpFMA, ir.OpFSqrt, ir.OpFConvert, ir.OpFProductError:
			temporary = false
		}
		if pos+1 < len(p.Instructions) && p.Instructions[pos+1].Op == ir.OpSet && p.Instructions[pos+1].A == pos {
			temporary = false
		}
		if temporary && last[pos] > pos {
			live += words
			ending[last[pos]] += words
		}
		demand[region] = max(demand[region], live)
		live -= ending[pos]
	}
	for scope := range demand {
		demand[scope] = max(1, demand[scope])
	}
	return demand
}

func emitHomeMove(e machineEmitter, bits uint8, dst, src physReg) {
	if dst == src {
		return
	}
	if machine.IsVectorRegister(dst) || machine.IsVectorRegister(src) {
		e.(*physicalEmitter).append(physicalInsn{Op: machine.OpFloatMove, Bits: bits, A: dst, B: src})
	} else {
		e.move(dst, src)
	}
}
func emitHomeSpill(e machineEmitter, r physReg, slot int) {
	if machine.IsVectorRegister(r) {
		e.(*physicalEmitter).append(physicalInsn{Op: machine.OpFloatSpill, Bits: 64, A: r, Slot: slot})
	} else {
		e.spill(r, slot)
	}
}
func emitHomeReload(e machineEmitter, r physReg, slot int) {
	if machine.IsVectorRegister(r) {
		e.(*physicalEmitter).append(physicalInsn{Op: machine.OpFloatReload, Bits: 64, A: r, Slot: slot})
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
	if machine.IsVectorRegister(r) {
		e.(*physicalEmitter).literal(r, bits, literalBytes(bits, value, t.NativeByteOrder()))
	} else {
		e.constant(r, value)
	}
}

// Floating arithmetic competes for its own register bank. This lookahead is
// separate from integer demand; ordinary integer operations cannot evict an FP
// home just because its scope has many integer temporaries.
func scopeFloatTemporaryDemand(p *ir.Program) map[int]int {
	last := make([]int, len(p.Instructions))
	for n := range last {
		last[n] = n
	}
	for pos, i := range p.Instructions {
		for _, x := range i.Inputs() {
			last[x] = max(last[x], pos)
		}
	}
	if p.Control != nil {
		for _, step := range p.Control.Steps {
			if step.IsLabel {
				continue
			}
			for _, x := range append(append(append([]int{}, step.Values...), step.Args...), step.OtherArgs...) {
				last[x] = max(last[x], step.End)
			}
		}
	}
	demand := map[int]int{}
	live := 0
	ending := map[int]int{}
	for pos, i := range p.Instructions {
		floating := false
		switch i.Op {
		case ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv, ir.OpFMA, ir.OpFSqrt, ir.OpFConvert, ir.OpFProductError:
			floating = true
		}
		instructionTemps := 0
		if floating {
			instructionTemps = len(i.Inputs()) + 1
		}
		if pos+1 < len(p.Instructions) && p.Instructions[pos+1].Op == ir.OpSet && p.Instructions[pos+1].A == pos {
			floating = false
		}
		if floating && last[pos] > pos {
			live++
			ending[last[pos]]++
		}
		region := regionAt(p, pos)
		demand[region] = max(demand[region], live, instructionTemps)
		live -= ending[pos]
	}
	for region := range demand {
		demand[region] = max(2, demand[region])
	}
	return demand
}
