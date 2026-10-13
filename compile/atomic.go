package compile

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// Atomic operations select to one SelectedAtomic each, and OpFence to a
// SelectedBarrier for machine.BarrierMemory. Each atomic is one machine.OpAtomic
// expansion: its retry loops are inside it, so no spill or reload can land
// between a load-reserve and its store-conditional. The registers an expansion
// needs beyond its operands are the target's never-allocated temporaries plus
// the fixed registers below, which the allocators keep free of operands and
// Var homes (FixedClobbers and ImplicitWrites).

func isAtomic(operation ir.Opcode) bool {
	return operation >= ir.OpAtomicLoad && operation <= ir.OpAtomicCompareSwap
}

// Fixed registers of the OpAtomic expansions, by architecture. 386 pair forms
// (Bits 64) take allocate386Wide's path instead.
var (
	amd64AtomicR11      = []physReg{11}
	amd64AtomicRAX      = []physReg{0}
	amd64AtomicRAXR11   = []physReg{0, 11}
	i386AtomicEDX       = []physReg{2}
	i386AtomicEAXEDX    = []physReg{0, 2}
	i386AtomicPair      = []physReg{0, 1, 2, 3, 6}
	riscv64AtomicMasked = []physReg{5, 6, 7}
	riscv64AtomicCAS    = []physReg{5, 6, 7, 28}
	loong64AtomicMasked = []physReg{13}
	loong64AtomicCAS    = []physReg{13, 14}
)

// atomicFixedRegisters lists the allocatable registers an atomic expansion
// writes besides its destination.
func atomicFixedRegisters(architecture string, operation ir.Opcode, bits uint8) []physReg {
	switch architecture {
	case "amd64":
		switch operation {
		case ir.OpAtomicStore, ir.OpAtomicAdd, ir.OpAtomicSwap:
			return amd64AtomicR11
		case ir.OpAtomicAnd, ir.OpAtomicOr, ir.OpAtomicXor:
			return amd64AtomicRAXR11
		case ir.OpAtomicCompareSwap:
			return amd64AtomicRAX
		}
	case "386":
		if bits == 64 {
			return i386AtomicPair
		}
		switch operation {
		case ir.OpAtomicStore, ir.OpAtomicAdd, ir.OpAtomicSwap:
			return i386AtomicEDX
		case ir.OpAtomicAnd, ir.OpAtomicOr, ir.OpAtomicXor, ir.OpAtomicCompareSwap:
			return i386AtomicEAXEDX
		}
	case "riscv64":
		if bits < 32 {
			switch operation {
			case ir.OpAtomicAdd, ir.OpAtomicSwap:
				return riscv64AtomicMasked
			case ir.OpAtomicCompareSwap:
				return riscv64AtomicCAS
			}
		}
	case "loong64":
		if bits < 32 {
			switch operation {
			case ir.OpAtomicAdd, ir.OpAtomicSwap:
				return loong64AtomicMasked
			case ir.OpAtomicCompareSwap:
				return loong64AtomicCAS
			}
		}
	}
	return nil
}

// CanReuse lets a 386 atomic's result take a dying operand's register: every
// x86 atomic expansion writes A last.
func (t *i386Target) CanReuse(ins machineInstruction, position int) bool {
	return ins.Op == mAtomic || t.Policy.CanReuse(ins, position)
}

func (t *riscv64Target) FixedClobbers(ins machineInstruction) []physReg {
	if ins.Op == mAtomic {
		return atomicFixedRegisters("riscv64", ins.Arithmetic, ins.Bits)
	}
	return nil
}

func (t *loong64Target) FixedClobbers(ins machineInstruction) []physReg {
	if ins.Op == mAtomic {
		return atomicFixedRegisters("loong64", ins.Arithmetic, ins.Bits)
	}
	return nil
}

// emitAtomic emits a selected atomic operation, with dst its result register
// (ignored for AtomicStore), or a selected barrier. The expansions write
// flags on some targets, so no flag fact survives them.
func emitAtomic(e *physicalEmitter, ins *machineInstruction, dst physReg, inputs []physReg) {
	if ins.Op == mBarrier {
		e.append(physicalInsn{Op: machine.OpBarrier, Barrier: machine.Barrier(ins.Imm)})
		return
	}
	physical := physicalInsn{Op: machine.OpAtomic, Arithmetic: ins.Arithmetic, Bits: ins.Bits, B: inputs[0], Imm: ins.Imm}
	if ins.Dst >= 0 {
		physical.A = dst
	}
	if len(inputs) > 1 {
		physical.C = inputs[1]
	}
	if len(inputs) > 2 {
		physical.D = inputs[2]
	}
	e.append(physical)
	e.forgetFlags()
}

// homeAtomicExpansion is the template homeExpansion reports for an atomic, so
// that Var homes avoid its fixed registers.
func homeAtomicExpansion(i *ir.Instruction) machine.Instruction {
	return machine.Instruction{Op: machine.OpAtomic, Arithmetic: i.Op, Bits: instructionBits(*i), Imm: i.Imm}
}

// checkAtomicSelection checks atomic operation or fence id for selection: the
// shape Validate requires, and a target that encodes it. Atomics have no
// software fallback.
func checkAtomicSelection(ops []ir.Instruction, id int, target selectionTarget) error {
	ins := &ops[id]
	if ins.Imm > 0xff || !ir.MemoryOrder(ins.Imm).Valid() {
		return backend.ErrInvalidFunction
	}
	if ins.Op == ir.OpFence {
		if ins.Bits != 0 {
			return backend.ErrInvalidFunction
		}
		barriers, ok := target.(interface {
			Barrier(machine.Barrier) machine.BarrierContract
		})
		if !ok || barriers.Barrier(machine.BarrierMemory) == machine.BarrierUnavailable {
			return fmt.Errorf("Fence at value %d is unavailable on %s: %w", id, target.Architecture(), backend.ErrBackendLimit)
		}
		return nil
	}
	width := selectedInstructionBits(*ins)
	if ins.A < 0 || ins.A >= id {
		return backend.ErrInvalidFunction
	}
	if bits := selectedInstructionBits(ops[ins.A]); bits != 32 && bits != 64 {
		return backend.ErrInvalidFunction
	}
	var buffer [3]int
	for _, input := range ins.InputsInto(buffer[:0])[1:] {
		if input < 0 || input >= id || selectedInstructionBits(ops[input]) != width {
			return backend.ErrInvalidFunction
		}
	}
	capability, ok := target.(interface {
		CanAtomic(ir.Opcode, uint8) bool
	})
	if !ok || !capability.CanAtomic(ins.Op, width) {
		return fmt.Errorf("%v at value %d (%d bits) is unavailable on %s: %w", ins.Op, id, width, target.Architecture(), backend.ErrBackendLimit)
	}
	return nil
}

// selectAtomic selects atomic operation or fence id.
func selectAtomic(ins ir.Instruction, id int) machine.SelectedInstruction {
	if ins.Op == ir.OpFence {
		return machine.SelectedInstruction{Op: machine.SelectedBarrier, Dst: -1, A: -1, B: -1, C: -1, Imm: uint64(machine.BarrierMemory)}
	}
	selected := machine.SelectedInstruction{Op: machine.SelectedAtomic, Arithmetic: ins.Op, Bits: selectedInstructionBits(ins), Dst: id, A: ins.A, B: -1, C: -1, Imm: ins.Imm}
	switch ins.Op {
	case ir.OpAtomicLoad:
	case ir.OpAtomicCompareSwap:
		selected.B, selected.C = ins.B, ins.C
	default:
		selected.B = ins.B
	}
	if ins.Op == ir.OpAtomicStore {
		selected.Dst = -1
	}
	return selected
}

// reportAtomic records an atomic operation's memory effects: loads read,
// stores write, and read-modify-writes and compare-and-swap do both.
func reportAtomic(report *abi.EmissionReport, operation ir.Opcode) {
	if operation != ir.OpAtomicStore {
		report.ExplicitMemoryReads, report.MayReadMemory = true, true
	}
	if operation != ir.OpAtomicLoad {
		report.ExplicitMemoryWrites, report.MayWriteMemory = true, true
	}
}
