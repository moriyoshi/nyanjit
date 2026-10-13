package arm64

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

func floatMemoryPlan(ins machine.Instruction, stack abi.StackPractice, homes int, text bool) concretePlan {
	if !validInstructionRegisters(&ins) || !validFloatSlot(ins.Slot, stack, homes) {
		return concretePlan{}
	}

	format := func(f string, args ...any) string {
		if !text {
			return ""
		}
		return fmt.Sprintf(f, args...)
	}
	base, off := uint32(8), ins.Slot*8
	if stack == abi.StackPlatform {
		base, off = 31, (ins.Slot+1+homes)*8
	}
	var out concretePlan
	name := ""
	if text {
		name = format("X%d", base)
	}
	if base == 31 {
		name = "SP"
	}
	if off > 32760 {
		out.parts[out.n].word = 0x91000010 | base<<5
		if text {
			out.parts[out.n].text = format("add X16, %s, #0", name)
		}
		out.n++
		base, name = 16, "X16"
		for off > 32760 {
			step := off / 4096
			if step > 4095 {
				step = 4095
			}
			out.parts[out.n].word = 0x91400210 | uint32(step)<<10
			if text {
				out.parts[out.n].text = format("add X16, X16, #%d, lsl #12", step)
			}
			out.n++
			off -= step * 4096
		}
	}
	op, mn := uint32(0xfd000000), "str"
	if ins.Op == machine.OpFloatReload {
		op, mn = 0xfd400000, "ldr"
	}
	out.parts[out.n].word = op | uint32(off/8)<<10 | base<<5 | uint32(backend.VectorNumber(ins.A))
	if text {
		out.parts[out.n].text = format("%s D%d, [%s, #%d]", mn, backend.VectorNumber(ins.A), name, off)
	}
	out.n++
	return out
}
func FloatMemorySize(ins machine.Instruction, stack abi.StackPractice, homes int) int {
	if ins.Op != machine.OpFloatSpill && ins.Op != machine.OpFloatReload {
		return 0
	}
	if !validInstructionRegisters(&ins) || !validSlot(ins.Slot, homes) {
		return 0
	}
	return floatMemoryPlan(ins, stack, homes, false).n * 4
}
func (e *ByteAssembler) FloatMemory(ins machine.Instruction) error {
	if !e.require(ins.Op == machine.OpFloatSpill || ins.Op == machine.OpFloatReload) {
		return e.err
	}
	if !e.require((validInstructionRegisters(&ins))) {
		return e.err
	}

	if !e.require(validFloatSlot(ins.Slot, e.stack, e.goHomes)) {
		return e.err
	}

	p := floatMemoryPlan(ins, e.stack, e.goHomes, false)
	for _, i := range p.parts[:p.n] {
		e.words = append(e.words, i.word)
	}
	return nil
}
func (e *TextAssembler) FloatMemory(ins machine.Instruction) error {
	if err := e.validErr(func(enc *ByteAssembler) error { return enc.FloatMemory(ins) }); err != nil {
		return err
	}
	if !validFloatSlot(ins.Slot, e.stack, e.goHomes) {
		return machine.ErrInvalidInstruction
	}

	p := floatMemoryPlan(ins, e.stack, e.goHomes, true)
	for _, i := range p.parts[:p.n] {
		e.line("%s", i.text)
	}
	return nil
}

// The fixed plan holds an initial base move, at most ten ADD expansions,
// and the memory operation. Bound arithmetic before multiplying slot by eight.
func validFloatSlot(slot int, stack abi.StackPractice, homes int) bool {
	if slot < 0 || homes < 0 || homes > 4094 {
		return false
	}
	extra := uint64(0)
	if stack == abi.StackPlatform {
		extra = uint64(1 + homes)
	}
	return uint64(slot)+extra <= uint64(32760+10*4095*4096)/8
}
