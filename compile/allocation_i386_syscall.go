package compile

import (
	"github.com/moriyoshi/nyanjit/backend/regalloc"
)

// i386SyscallRegisters are the Linux INT 0x80 registers of the number and
// the six arguments, as abi.PreflightCall names them.
var i386SyscallRegisters = [7]physReg{0, 3, 1, 2, 6, 7, 5}

// allocate386Syscall allocates and emits a 386 system call on the direct
// walk. Only the low word of the number and of each argument is passed. The
// inputs are first made durable (a constant, a slot or an incoming word), so
// that the fixed registers load in any order; the values that live past the
// call leave the registers the sequence writes: EAX, EDX and the argument
// registers. A sixth argument loads into EBP last, and EBP is restored from
// a scratch slot right after INT 0x80. The result pair is EAX sign-extended
// into EDX, so a -errno result reads as on the 64-bit targets. The caller
// restores the call's saved homes and consumes the inputs.
func allocate386Syscall(ins machineInstruction, program machineProgram, alloc *regalloc.Residency, emitter *physicalEmitter, written *registerWrites, inputs []int, registers []physReg, durable func(int) error, load func(physReg, int, int)) error {
	if len(inputs) == 0 || len(inputs) > len(i386SyscallRegisters) {
		return ErrBackendLimit
	}
	for _, id := range inputs {
		if err := durable(id); err != nil {
			return err
		}
	}
	var overwritten registerWrites
	overwritten[0], overwritten[2] = true, true
	for _, r := range i386SyscallRegisters[:len(inputs)] {
		overwritten[r] = true
	}
	locations := alloc.Locations()
	for _, r := range registers {
		id := alloc.Owner(r)
		if id < 0 || !overwritten[r] {
			continue
		}
		usesHere := countUses(inputs, id)
		if alloc.Remaining(id) > usesHere && !program.Values[id].Constant && locations[id].Slot < 0 {
			if err := alloc.Spill(id, alloc.Group(id)); err != nil {
				return err
			}
		}
		alloc.Release(id)
	}
	ebp := -1
	if len(inputs) == len(i386SyscallRegisters) {
		slot, err := alloc.AllocateSlot()
		if err != nil {
			return err
		}
		ebp = slot
		emitter.spill(5, ebp)
	}
	for i, id := range inputs {
		r := i386SyscallRegisters[i]
		if v := program.Values[id]; v.Constant && v.Symbol != "" {
			emitter.symbol(r, v.Symbol)
			written[r] = true
			continue
		}
		load(r, id, 0)
	}
	emitter.syscall()
	if ebp >= 0 {
		emitter.reload(5, ebp)
		alloc.FreeSlots(ebp, 1)
	}
	written[0], written[2] = true, true
	if program.Values[ins.Dst].Words < 2 {
		// A raw IR result narrower than 64 bits is EAX.
		return alloc.Claim(ins.Dst, regalloc.SingleRegister(0))
	}
	emitter.shift(opShiftRightArithmetic, 32, 2, 0, 31)
	emitter.forgetFlags()
	return alloc.Claim(ins.Dst, regalloc.Group{Registers: [2]physReg{0, 2}, Words: 2})
}
