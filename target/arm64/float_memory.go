package arm64

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/machine"
)

func floatMemoryPlan(ins machine.Instruction, stack abi.StackPractice, homes int) []vectorConcrete {
	base, off := uint32(8), ins.Slot*8
	if stack == abi.StackPlatform {
		base, off = 31, (ins.Slot+1+homes)*8
	}
	var out []vectorConcrete
	name := fmt.Sprintf("X%d", base)
	if base == 31 {
		name = "SP"
	}
	if off > 32760 {
		out = append(out, vectorConcrete{0x91000010 | base<<5, fmt.Sprintf("add X16, %s, #0", name)})
		base, name = 16, "X16"
		for off > 32760 {
			step := off / 4096
			if step > 4095 {
				step = 4095
			}
			out = append(out, vectorConcrete{0x91400210 | uint32(step)<<10, fmt.Sprintf("add X16, X16, #%d, lsl #12", step)})
			off -= step * 4096
		}
	}
	op, mn := uint32(0xfd000000), "str"
	if ins.Op == machine.OpFloatReload {
		op, mn = 0xfd400000, "ldr"
	}
	out = append(out, vectorConcrete{op | uint32(off/8)<<10 | base<<5 | uint32(machine.VectorNumber(ins.A)), fmt.Sprintf("%s D%d, [%s, #%d]", mn, machine.VectorNumber(ins.A), name, off)})
	return out
}
func FloatMemorySize(ins machine.Instruction, stack abi.StackPractice, homes int) int {
	return len(floatMemoryPlan(ins, stack, homes)) * 4
}
func (e *ByteAssembler) FloatMemory(ins machine.Instruction) error {
	for _, i := range floatMemoryPlan(ins, e.stack, e.goHomes) {
		e.words = append(e.words, i.word)
	}
	return nil
}
func (e *TextAssembler) FloatMemory(ins machine.Instruction) error {
	for _, i := range floatMemoryPlan(ins, e.stack, e.goHomes) {
		e.line("%s", i.text)
	}
	return nil
}
