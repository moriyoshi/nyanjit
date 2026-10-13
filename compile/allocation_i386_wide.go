package compile

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/regalloc"
)

// The 386 expansions of a 64-bit division and of the conversions between a
// 64-bit integer and a float, on the direct walk. A division reads its pair
// operands from four scratch slots and clobbers EAX, EBX, ECX, EDX, ESI and
// EDI, which it marks written so that a C entry saves the preserved ones,
// with or without a frame; an integer-to-float conversion reads its pair from registers
// and uses two scratch slots for memory operands; a float-to-integer
// conversion leaves its pair in EDX:EAX and clobbers ECX. Spilling an input
// into its scratch slot also makes the frame cover the slot.

var (
	i386DivideClobbers  = []physReg{0, 1, 2, 3, 6, 7}
	i386ConvertClobbers = []physReg{0, 1, 2}
)

// allocate386Wide allocates and emits ins, one of those expansions.
func allocate386Wide(ins machineInstruction, alloc *regalloc.Residency, emitter *physicalEmitter, written *registerWrites, inputs []int) error {
	result := regalloc.Group{Registers: [2]physReg{0, 2}, Words: 2}
	switch ins.Op {
	case mDivide:
		base, err := alloc.AppendSlots(4)
		if err != nil {
			return err
		}
		for n, id := range inputs {
			g, err := alloc.Ensure(id, regalloc.Pins{})
			if err != nil {
				return err
			}
			emitter.spill(g.Registers[0], base+2*n)
			emitter.spill(g.Registers[1], base+2*n+1)
		}
		// The inputs are in the slots now: keep only those used later.
		for _, id := range inputs {
			if alloc.Remaining(id) == countUses(inputs, id) {
				alloc.Release(id)
			}
		}
		if err := vacate386(alloc, i386DivideClobbers); err != nil {
			return err
		}
		emitter.append(physicalInsn{Op: machine.OpDivide, Arithmetic: ins.Arithmetic, Bits: 64, A: 0, HighA: 2, Imm: ins.Imm, Slot: base})
		alloc.FreeSlots(base, 4)
		for _, r := range i386DivideClobbers {
			written[r] = true
		}
	case mIntToFloat:
		src, err := alloc.Ensure(inputs[0], regalloc.Pins{})
		if err != nil {
			return err
		}
		var pins regalloc.Pins
		pins.AddRegister(src.Registers[0])
		pins.AddRegister(src.Registers[1])
		dst, err := alloc.Choose(ins.Dst, pins)
		if err != nil {
			return err
		}
		base, err := alloc.AllocateSlots(2)
		if err != nil {
			return err
		}
		emitter.spill(src.Registers[0], base)
		emitter.spill(src.Registers[1], base+1)
		emitter.append(physicalInsn{Op: machine.OpIntToFloat, Arithmetic: ins.Arithmetic, Bits: ins.Bits, A: dst.Registers[0], B: src.Registers[0], HighB: src.Registers[1], Imm: 64, Slot: base})
		alloc.FreeSlots(base, 2)
		written[dst.Registers[0]] = true
		result = dst
	case mFloatToInt:
		src, err := alloc.Ensure(inputs[0], regalloc.Pins{})
		if err != nil {
			return err
		}
		if err := vacate386(alloc, i386ConvertClobbers); err != nil {
			return err
		}
		emitter.append(physicalInsn{Op: machine.OpFloatToInt, Arithmetic: ins.Arithmetic, Bits: 64, A: 0, HighA: 2, B: src.Registers[0], Imm: ins.Imm, Disp: ins.Disp})
		for _, r := range i386ConvertClobbers {
			written[r] = true
		}
	}
	emitter.forgetFlags()
	if err := alloc.Claim(ins.Dst, result); err != nil {
		return err
	}
	alloc.Consume(inputs)
	if alloc.Remaining(ins.Dst) == 0 {
		alloc.Release(ins.Dst)
	}
	return nil
}

// vacate386 frees regs for an expansion's fixed use, moving or spilling the
// values that live there.
func vacate386(alloc *regalloc.Residency, regs []physReg) error {
	var pins regalloc.Pins
	for _, r := range regs {
		pins.AddRegister(r)
	}
	for _, r := range regs {
		if alloc.Owner(r) < 0 {
			continue
		}
		if err := alloc.Evict(r, pins); err != nil {
			return err
		}
	}
	return nil
}

func countUses(inputs []int, id int) int {
	n := 0
	for _, input := range inputs {
		if input == id {
			n++
		}
	}
	return n
}
