package ppc64

import (
	"fmt"
	"math"
	"math/bits"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/regalloc"
)

// Emitter expands allocated operations into concrete POWER instructions.
// Its immutable configuration is shared by size planning and final expansion.
type Emitter struct {
	profile abi.PPC64Profile
	stack   abi.StackPractice
	managed bool
	cpu     abi.PPC64CPU
}

func NewEmitter(profile abi.PPC64Profile, stack abi.StackPractice) (*Emitter, error) {
	return NewEmitterWithContext(profile, stack, false)
}

// NewEmitterWithContext selects Go-unwindable frames independently of the
// entry convention. Managed C entries require a Go runtime call chain.
func NewEmitterWithContext(profile abi.PPC64Profile, stack abi.StackPractice, managed bool) (*Emitter, error) {
	return NewEmitterForCPU(profile, stack, managed, abi.PPC64Power8)
}

// NewEmitterForCPU selects instruction expansions for an explicit POWER level.
// Callers of this low-level emitter must ensure the execution CPU supports it.
func NewEmitterForCPU(profile abi.PPC64Profile, stack abi.StackPractice, managed bool, cpu abi.PPC64CPU) (*Emitter, error) {
	policy, err := NewPolicyForCPU(profile, stack, cpu)
	if err != nil {
		return nil, err
	}
	return &Emitter{profile: profile, stack: stack, managed: managed, cpu: policy.CPU()}, nil
}

func (e *Emitter) frameLayout() abi.PPC64FrameLayout {
	layout := e.profile.Frame()
	layout.Alignment = 16
	if e.managed {
		layout.BackChain = false
		layout.ReturnAddressOffset = 0
	}
	return layout
}

// FrameBytes reserves linkage, outgoing register homes, and spill words.
// Leaf functions without spills keep SP unchanged.
func (e *Emitter) FrameBytes(slots int, hasCall bool) int {
	if slots == 0 && !hasCall {
		return 0
	}
	layout := e.frameLayout()
	return (e.SpillOffset() + slots*8 + layout.Alignment - 1) &^ (layout.Alignment - 1)
}

// SpillOffset keeps allocator storage above either C linkage/argument homes
// or all twelve Go register argument homes, regardless of the callee ABI.
func (e *Emitter) SpillOffset() int { return 128 }

// Size measures the expansion before labels have been resolved. Branch
// displacement validation belongs to final encoding, after emitter layout.
func (e *Emitter) Size(ins machine.Instruction) int {
	if ins.Op == machine.OpLiteralData {
		return len(ins.Data)
	}
	sequence, err := e.Expand(ins, 0)
	if err != nil {
		return 0
	}
	return len(sequence) * 4
}

// Expand assigns concrete instruction PCs to the expansion selected for ins.
func (e *Emitter) Expand(ins machine.Instruction, pc int) ([]Instruction, error) {
	if pc < 0 || pc%4 != 0 || !ins.ByteOrder.Valid() {
		return nil, machine.ErrUnsupportedOperation
	}
	if ins.ByteOrder != ir.ByteOrderNative {
		switch ins.Op {
		case machine.OpLoad64, machine.OpStore64, machine.OpLoad8, machine.OpStore8,
			machine.OpLoadOffset, machine.OpStoreOffset, machine.OpLoadAddress, machine.OpStoreAddress:
		default:
			return nil, machine.ErrUnsupportedOperation
		}
	}
	p := expansion{emitter: e}
	if err := p.operation(ins, pc); err != nil {
		return nil, err
	}
	for i := range p.instructions {
		p.instructions[i].PC = pc + i*4
	}
	return p.instructions, nil
}

type expansion struct {
	emitter      *Emitter
	instructions []Instruction
}

func (p *expansion) emit(ins Instruction) { p.instructions = append(p.instructions, ins) }
func (p *expansion) move(dst, source machine.Register) {
	if dst != source {
		p.emit(Instruction{Op: Or, A: dst, B: source, C: source})
	}
}
func (p *expansion) constant(dst machine.Register, value uint64, fixed bool) {
	if !fixed && int64(value) >= -32768 && int64(value) <= 32767 {
		p.emit(Instruction{Op: Addi, A: dst, Imm: int64(value)})
		return
	}
	if !fixed && int64(value) >= -1<<31 && int64(value) <= 1<<31-1 {
		p.emit(Instruction{Op: Addis, A: dst, Imm: int64(int16(value >> 16))})
		p.emit(Instruction{Op: Ori, A: dst, B: dst, Imm: int64(uint16(value))})
		return
	}
	p.emit(Instruction{Op: Addis, A: dst, Imm: int64(int16(value >> 48))})
	p.emit(Instruction{Op: Ori, A: dst, B: dst, Imm: int64(uint16(value >> 32))})
	p.emit(Instruction{Op: Rldicr, A: dst, B: dst, Shift: 32, Mask: 31})
	p.emit(Instruction{Op: Oris, A: dst, B: dst, Imm: int64(uint16(value >> 16))})
	p.emit(Instruction{Op: Ori, A: dst, B: dst, Imm: int64(uint16(value))})
}
func (p *expansion) narrow(dst, source machine.Register, width uint8) error {
	if width == 64 {
		p.move(dst, source)
		return nil
	}
	if width != 8 && width != 16 && width != 32 {
		return machine.ErrUnsupportedOperation
	}
	p.emit(Instruction{Op: Rldicl, A: dst, B: source, Mask: 64 - width})
	return nil
}
func (p *expansion) signExtend(dst, source machine.Register, width uint8) error {
	op := Extsb
	switch width {
	case 8:
	case 16:
		op = Extsh
	case 32:
		op = Extsw
	case 64:
		p.move(dst, source)
		return nil
	default:
		return machine.ErrUnsupportedOperation
	}
	p.emit(Instruction{Op: op, A: dst, B: source})
	return nil
}
func (p *expansion) shift(operation ir.Opcode, width uint8, dst, source machine.Register, count uint8) error {
	if width == 0 {
		width = 64
	}
	if count >= width || width != 8 && width != 16 && width != 32 && width != 64 {
		return machine.ErrUnsupportedOperation
	}
	if count == 0 {
		if operation == ir.OpShiftRightArithmetic && width < 64 {
			return p.signExtend(dst, source, width)
		}
		p.move(dst, source)
		return nil
	}
	switch operation {
	case ir.OpShiftLeft:
		p.emit(Instruction{Op: Rldicr, A: dst, B: source, Shift: count, Mask: 63 - count})
	case ir.OpShiftRight:
		p.emit(Instruction{Op: Rldicl, A: dst, B: source, Shift: 64 - count, Mask: count})
	case ir.OpShiftRightArithmetic:
		if width < 64 {
			if err := p.signExtend(11, source, width); err != nil {
				return err
			}
			source = 11
		}
		p.emit(Instruction{Op: Sradi, A: dst, B: source, Shift: count})
	case ir.OpRotateLeft, ir.OpRotateRight:
		if operation == ir.OpRotateRight {
			count = width - count
		}
		if width == 64 {
			p.emit(Instruction{Op: Rldicl, A: dst, B: source, Shift: count})
		} else if width == 32 {
			p.emit(Instruction{Op: Rlwinm, A: dst, B: source, Shift: count, MaskEnd: 31})
		} else {
			p.emit(Instruction{Op: Rldicr, A: 11, B: source, Shift: count, Mask: 63 - count})
			p.emit(Instruction{Op: Rldicl, A: 12, B: source, Shift: 64 - (width - count), Mask: width - count})
			p.emit(Instruction{Op: Or, A: dst, B: 11, C: 12})
			return p.narrow(dst, dst, width)
		}
	default:
		return machine.ErrUnsupportedOperation
	}
	return nil
}

func (p *expansion) memory(load bool, data, base machine.Register, displacement uint64, index int, order ir.ByteOrder) error {
	native := ir.ByteOrderBigEndian
	if p.emitter.profile.Architecture() == "ppc64le" {
		native = ir.ByteOrderLittleEndian
	}
	if !order.Valid() {
		return machine.ErrUnsupportedOperation
	}
	reversed := order.Resolve(native) != native
	if index < 0 && !reversed && int64(displacement) >= -32768 && int64(displacement) <= 32764 && displacement&3 == 0 {
		op := Std
		if load {
			op = Ld
		}
		p.emit(Instruction{Op: op, A: data, B: base, Imm: int64(displacement)})
		return nil
	}
	if index < 0 {
		if displacement == 0 {
			index, base = int(base), 0 // RA=0 is the indexed form's literal zero.
		} else {
			scratch := machine.Register(11)
			if base == scratch || !load && data == scratch {
				scratch = 12
			}
			if base == scratch || !load && data == scratch {
				return machine.ErrUnsupportedOperation
			}
			p.constant(scratch, displacement, false)
			index = int(scratch)
		}
	}
	op := Stdx
	if load {
		op = Ldx
	}
	if reversed {
		op = Stdbrx
		if load {
			op = Ldbrx
		}
	}
	p.emit(Instruction{Op: op, A: data, B: base, C: machine.Register(index)})
	return nil
}

func branchBits(condition ir.BranchCondition) (bo, bi uint8, err error) {
	switch condition {
	case ir.BranchEqual:
		return 12, 2, nil
	case ir.BranchNotEqual:
		return 4, 2, nil
	case machine.BranchCarry:
		return 12, 30, nil
	case machine.BranchNotCarry:
		return 4, 30, nil
	case machine.BranchCarry32:
		return 12, 31, nil
	case machine.BranchNotCarry32:
		return 4, 31, nil
	case machine.BranchSign, ir.BranchLess, ir.BranchBelow:
		return 12, 0, nil
	case machine.BranchNotSign, ir.BranchGreaterEqual, ir.BranchAboveEqual:
		return 4, 0, nil
	case ir.BranchLessEqual, ir.BranchBelowEqual:
		return 4, 1, nil
	case ir.BranchGreater, ir.BranchAbove:
		return 12, 1, nil
	default:
		return 0, 0, machine.ErrUnsupportedOperation
	}
}

func (p *expansion) operation(ins machine.Instruction, pc int) error {
	switch ins.Op {
	case machine.OpLiteralLoad:
		if pc < 0 || pc > math.MaxInt-8 || ins.Disp > math.MaxInt64 {
			return regalloc.ErrBackendLimit
		}
		delta := int64(ins.Disp) - int64(pc+8)
		if delta < -(1<<31) || delta > (1<<31)-32769 {
			return fmt.Errorf("ppc64 literal offset exceeds ADDIS reach: %w", regalloc.ErrBackendLimit)
		}
		hi := (delta + 32768) >> 16
		lo := delta - hi*65536
		op := Lfd
		if ins.Bits == 32 {
			op = Lfs
		}
		p.emit(Instruction{Op: Mflr, A: 12})
		p.emit(Instruction{Op: Bc, BO: 20, BI: 31, Imm: 4, Link: true})
		p.emit(Instruction{Op: Mflr, A: 11})
		p.emit(Instruction{Op: Addis, A: 11, B: 11, Imm: hi})
		p.emit(Instruction{Op: op, A: machine.Register(machine.VectorNumber(ins.A)), B: 11, Imm: lo})
		p.emit(Instruction{Op: Mtlr, A: 12})
		return nil

	case machine.OpConst:
		p.constant(ins.A, ins.Imm, false)
	case machine.OpSymbol:
		if ins.Symbol == "" {
			return machine.ErrUnsupportedOperation
		}
		p.constant(ins.A, 0, true)
		p.instructions[0].Symbol = ins.Symbol
	case machine.OpMove:
		p.move(ins.A, ins.B)
	case machine.OpFloatMove, machine.OpFloatBits, machine.OpFloatUnary, machine.OpFloatBinary, machine.OpFloatFMA:
		return p.floatOperation(ins)
	case machine.OpBinary:
		op := Add
		switch ins.Arithmetic {
		case ir.OpAdd:
			if !ins.PreserveFlags && (ins.Bits == 64 || ins.Bits == 32 && p.emitter.cpu >= abi.PPC64Power9) {
				op = Addc
			}
		case ir.OpSub:
			op = Subfc
			if ins.PreserveFlags {
				op = Subf
			}
		case ir.OpMul:
			op = Mulld
		case ir.OpAnd:
			op = And
		case ir.OpOr:
			op = Or
		case ir.OpXor:
			op = Xor
		default:
			return machine.ErrUnsupportedOperation
		}
		p.emit(Instruction{Op: op, A: ins.A, B: ins.B, C: ins.C, Record: !ins.PreserveFlags})
	case machine.OpBinaryImmediate:
		op, immediate := Addic, int64(ins.Imm)
		switch ins.Arithmetic {
		case ir.OpAdd:
		case ir.OpSub:
			immediate = -immediate
		case ir.OpAnd:
			op = Andi
		case ir.OpOr:
			op = Ori
		case ir.OpXor:
			op = Xori
		default:
			return machine.ErrUnsupportedOperation
		}
		if ins.PreserveFlags && op == Addic && ins.B != 0 {
			op = Addi
		}
		p.emit(Instruction{Op: op, A: ins.A, B: ins.B, Imm: immediate, Record: op == Addic && !ins.PreserveFlags})
	case machine.OpInc, machine.OpDec:
		immediate := int64(1)
		if ins.Op == machine.OpDec {
			immediate = -1
		}
		op := Addic
		if ins.PreserveFlags && ins.B != 0 {
			op = Addi
		}
		p.emit(Instruction{Op: op, A: ins.A, B: ins.B, Imm: immediate, Record: !ins.PreserveFlags})
	case machine.OpNarrow:
		if ins.Imm > 64 {
			return machine.ErrUnsupportedOperation
		}
		if err := p.narrow(ins.A, ins.B, uint8(ins.Imm)); err != nil {
			return err
		}
		if ins.FlagRequest != 0 && len(p.instructions) != 0 && ins.Imm < 64 {
			p.instructions[len(p.instructions)-1].Record = true
		}
	case machine.OpSignExtend:
		if ins.Imm > 64 {
			return machine.ErrUnsupportedOperation
		}
		if err := p.signExtend(ins.A, ins.B, uint8(ins.Imm)); err != nil {
			return err
		}
		if ins.FlagRequest != 0 {
			p.instructions[len(p.instructions)-1].Record = true
		}
	case machine.OpShift:
		if ins.Imm >= 64 {
			return machine.ErrUnsupportedOperation
		}
		return p.shift(ins.Arithmetic, ins.Bits, ins.A, ins.B, uint8(ins.Imm))
	case machine.OpCarryBit, machine.OpWordCarry:
		return p.wordCarry(ins)
	case machine.OpMulHigh:
		width := ins.Bits
		if width == 0 {
			width = 64
		}
		switch width {
		case 64:
			p.emit(Instruction{Op: Mulhdu, A: ins.A, B: ins.B, C: ins.C})
		case 32:
			p.emit(Instruction{Op: Mulhwu, A: ins.A, B: ins.B, C: ins.C})
		case 8, 16:
			p.emit(Instruction{Op: Mulld, A: ins.A, B: ins.B, C: ins.C})
			p.emit(Instruction{Op: Rldicl, A: ins.A, B: ins.A, Shift: 64 - width, Mask: width})
			return p.narrow(ins.A, ins.A, width)
		default:
			return machine.ErrUnsupportedOperation
		}
	case machine.OpShiftBy:
		return p.shiftBy(ins.Arithmetic, ins.Bits, ins.A, ins.B, ins.C)
	case machine.OpScaled:
		index := ins.C
		if ins.Imm != 1 {
			if ins.Imm != 0 && ins.Imm&(ins.Imm-1) == 0 {
				count := uint8(bits.TrailingZeros64(ins.Imm))
				p.emit(Instruction{Op: Rldicr, A: 11, B: index, Shift: count, Mask: 63 - count})
			} else {
				p.constant(11, ins.Imm, false)
				p.emit(Instruction{Op: Mulld, A: 11, B: index, C: 11})
			}
			index = 11
		}
		p.emit(Instruction{Op: Add, A: ins.A, B: ins.B, C: index})
		if ins.Disp != 0 {
			p.constant(12, ins.Disp, false)
			p.emit(Instruction{Op: Add, A: ins.A, B: ins.A, C: 12})
		}
	case machine.OpMulAdd:
		if p.emitter.cpu >= abi.PPC64Power9 {
			p.emit(Instruction{Op: Maddld, A: ins.A, B: ins.C, C: ins.D, D: ins.B})
		} else {
			p.emit(Instruction{Op: Mulld, A: 11, B: ins.C, C: ins.D})
			p.emit(Instruction{Op: Add, A: ins.A, B: ins.B, C: 11})
		}
	case machine.OpSelect:
		p.emit(Instruction{Op: Cmpli, B: ins.B, Bits: 64})
		p.emit(Instruction{Op: Isel, A: ins.A, B: ins.D, C: ins.C, BI: 2})
	case machine.OpLoad64:
		return p.memory(true, ins.A, ins.B, 0, -1, ins.ByteOrder)
	case machine.OpStore64:
		return p.memory(false, ins.B, ins.A, 0, -1, ins.ByteOrder)
	case machine.OpLoadOffset:
		return p.memory(true, ins.A, ins.B, ins.Disp, -1, ins.ByteOrder)
	case machine.OpStoreOffset:
		return p.memory(false, ins.B, ins.A, ins.Disp, -1, ins.ByteOrder)
	case machine.OpLoadAddress, machine.OpStoreAddress:
		if ins.Imm != 1 || ins.Disp != 0 {
			return machine.ErrUnsupportedOperation
		}
		if ins.Op == machine.OpLoadAddress {
			return p.memory(true, ins.A, ins.B, 0, int(ins.C), ins.ByteOrder)
		}
		return p.memory(false, ins.C, ins.A, 0, int(ins.B), ins.ByteOrder)
	case machine.OpLoad8:
		p.emit(Instruction{Op: Lbz, A: ins.A, B: ins.B})
	case machine.OpStore8:
		p.emit(Instruction{Op: Stb, A: ins.B, B: ins.A})
	case machine.OpFloatSpill, machine.OpFloatReload:
		if ins.Slot < 0 {
			return machine.ErrUnsupportedOperation
		}
		base, offset := machine.Register(29), ins.Slot*8
		if p.emitter.stack == abi.StackPlatform {
			base, offset = 1, p.emitter.SpillOffset()+offset
		}
		if offset > 32767 {
			p.constant(11, uint64(offset), false)
			p.emit(Instruction{Op: Add, A: 11, B: base, C: 11})
			base, offset = 11, 0
		}
		op := Stfd
		if ins.Op == machine.OpFloatReload {
			op = Lfd
		}
		p.emit(Instruction{Op: op, A: machine.Register(machine.VectorNumber(ins.A)), B: base, Imm: int64(offset)})
	case machine.OpSpill, machine.OpReload:
		if ins.Slot < 0 {
			return machine.ErrUnsupportedOperation
		}
		base, offset := machine.Register(29), ins.Slot*8
		if p.emitter.stack == abi.StackPlatform {
			base, offset = 1, p.emitter.SpillOffset()+offset
		}
		return p.memory(ins.Op == machine.OpReload, ins.A, base, uint64(offset), -1, ir.ByteOrderNative)
	case machine.OpSaveCFloat, machine.OpRestoreCFloat, machine.OpSaveCVector, machine.OpRestoreCVector, machine.OpSaveCondition, machine.OpRestoreCondition:
		if p.emitter.stack != abi.StackPlatform || ins.Slot < 0 {
			return machine.ErrUnsupportedOperation
		}
		offset := p.emitter.SpillOffset() + ins.Slot*8
		switch ins.Op {
		case machine.OpSaveCFloat, machine.OpRestoreCFloat:
			if offset > 32767 {
				return machine.ErrUnsupportedOperation
			}
			op := Stfd
			if ins.Op == machine.OpRestoreCFloat {
				op = Lfd
			}
			p.emit(Instruction{Op: op, A: ins.A, B: 1, Imm: int64(offset)})
		case machine.OpSaveCVector, machine.OpRestoreCVector:
			if offset%16 != 0 {
				return machine.ErrUnsupportedOperation
			}
			p.constant(11, uint64(offset), false)
			op := Stvx
			if ins.Op == machine.OpRestoreCVector {
				op = Lvx
			}
			p.emit(Instruction{Op: op, A: ins.A, B: 1, C: 11})
		case machine.OpSaveCondition:
			p.emit(Instruction{Op: Mfcr, A: 11})
			return p.memory(false, 11, 1, uint64(offset), -1, ir.ByteOrderNative)
		case machine.OpRestoreCondition:
			if err := p.memory(true, 11, 1, uint64(offset), -1, ir.ByteOrderNative); err != nil {
				return err
			}
			p.emit(Instruction{Op: Mtcrf, A: 11, Imm: 0x38}) // Only the C nonvolatile CR2-CR4.
		}
	case machine.OpLoadIncoming:
		return p.memory(true, ins.A, 1, ins.Imm, -1, ir.ByteOrderNative)
	case machine.OpFrameEnter, machine.OpFrameLeave:
		if p.emitter.stack != abi.StackPlatform || ins.Imm == 0 || ins.Imm%uint64(p.emitter.frameLayout().Alignment) != 0 {
			return machine.ErrUnsupportedOperation
		}
		amount := int64(ins.Imm)
		if amount < 0 {
			return machine.ErrUnsupportedOperation
		}
		if ins.Op == machine.OpFrameEnter {
			p.emit(Instruction{Op: Mflr, A: 11})
			lrOffset := int64(16)
			if !p.emitter.frameLayout().BackChain {
				lrOffset = -amount
			}
			if err := p.memory(false, 11, 1, uint64(lrOffset), -1, ir.ByteOrderNative); err != nil {
				return err
			}
			amount = -amount
			if p.emitter.frameLayout().BackChain {
				if amount >= -32768 {
					p.emit(Instruction{Op: Stdu, A: 1, B: 1, Imm: amount})
				} else {
					p.constant(11, uint64(amount), false)
					p.emit(Instruction{Op: Stdux, A: 1, B: 1, C: 11})
				}
				p.emit(Instruction{Op: Std, A: 2, B: 1, Imm: 24})
				return nil
			}
		} else {
			lrOffset := int64(0)
			if p.emitter.frameLayout().BackChain {
				lrOffset = amount + 16
			}
			if err := p.memory(true, 11, 1, uint64(lrOffset), -1, ir.ByteOrderNative); err != nil {
				return err
			}
			p.emit(Instruction{Op: Ld, A: 2, B: 1, Imm: 24})
			p.emit(Instruction{Op: Mtlr, A: 11})
		}
		if amount >= -32768 && amount <= 32767 {
			p.emit(Instruction{Op: Addi, A: 1, B: 1, Imm: amount})
		} else {
			p.constant(11, uint64(amount), false)
			p.emit(Instruction{Op: Add, A: 1, B: 1, C: 11})
		}
		if ins.Op == machine.OpFrameEnter {
			p.emit(Instruction{Op: Std, A: 2, B: 1, Imm: 24})
		}
	case machine.OpCallC:
		if p.emitter.stack != abi.StackPlatform {
			return machine.ErrUnsupportedOperation
		}
		convention := ins.PPC64CABI
		if convention == abi.PPC64CABIUnspecified {
			convention = p.emitter.profile.CABI()
		}
		profile, err := abi.NewPPC64Profile(p.emitter.profile.Architecture(), abi.C, convention)
		if err != nil {
			return err
		}
		p.emit(Instruction{Op: Std, A: 2, B: 1, Imm: 24})
		if profile.FunctionPointer() == abi.FunctionPointerDescriptor {
			p.emit(Instruction{Op: Ld, A: 2, B: 12, Imm: 8})
			p.emit(Instruction{Op: Ld, A: 11, B: 12, Imm: 16})
			p.emit(Instruction{Op: Ld, A: 12, B: 12})
		}
		p.emit(Instruction{Op: Mtctr, A: 12})
		p.emit(Instruction{Op: Bctrl})
		p.emit(Instruction{Op: Ld, A: 2, B: 1, Imm: 24})
		p.emit(Instruction{Op: Xor, A: 0, B: 0, C: 0})
	case machine.OpCallGo:
		if p.emitter.stack != abi.StackPlatform {
			return machine.ErrUnsupportedOperation
		}
		p.emit(Instruction{Op: Xor, A: 0, B: 0, C: 0})
		p.emit(Instruction{Op: Mtctr, A: 12})
		p.emit(Instruction{Op: Bctrl})
		p.emit(Instruction{Op: Ld, A: 2, B: 1, Imm: 24})
	case machine.OpSyscall:
		p.emit(Instruction{Op: Sc})
		// Linux sc returns positive errno with CR0.SO set. Normalize to
		// the same negative errno representation used by other backends.
		p.emit(Instruction{Op: Bc, BO: 4, BI: 3, Imm: 8})
		p.emit(Instruction{Op: Neg, A: 3, B: 3})
		p.emit(Instruction{Op: Xor, A: 0, B: 0, C: 0})
	case machine.OpSaveLink, machine.OpRestoreLink:
		if ins.Slot < 0 {
			return machine.ErrUnsupportedOperation
		}
		base, offset := machine.Register(29), ins.Slot*8
		if p.emitter.stack == abi.StackPlatform {
			base, offset = 1, p.emitter.SpillOffset()+offset
		}
		if ins.Op == machine.OpSaveLink {
			p.emit(Instruction{Op: Mflr, A: 11})
			return p.memory(false, 11, base, uint64(offset), -1, ir.ByteOrderNative)
		}
		if err := p.memory(true, 11, base, uint64(offset), -1, ir.ByteOrderNative); err != nil {
			return err
		}
		p.emit(Instruction{Op: Mtlr, A: 11})
	case machine.OpReturn:
		p.emit(Instruction{Op: Blr})
	case machine.OpCompareImmediate, machine.OpCompareRegister:
		width := ins.Bits
		if width == 0 {
			width = 64
		}
		if width != 32 && width != 64 || ins.Comparison > machine.CompareUnsigned {
			return machine.ErrUnsupportedOperation
		}
		unsigned := ins.Comparison == machine.CompareUnsigned
		op := Cmp
		if unsigned {
			op = Cmpl
		}
		if ins.Op == machine.OpCompareImmediate {
			immediate := int64(ins.Imm)
			fits := immediate >= -32768 && immediate <= 32767
			if unsigned {
				fits = ins.Imm <= 65535
			}
			if fits {
				if unsigned {
					op = Cmpli
				} else {
					op = Cmpi
				}
				p.emit(Instruction{Op: op, B: ins.A, Imm: immediate, Bits: width})
			} else {
				p.constant(12, ins.Imm, false)
				p.emit(Instruction{Op: op, B: ins.A, C: 12, Bits: width})
			}
		} else {
			p.emit(Instruction{Op: op, B: ins.A, C: ins.B, Bits: width})
		}
	case machine.OpSetCount:
		p.emit(Instruction{Op: Mtctr, A: ins.A})
	case machine.OpLoop:
		if ins.Bits != 0 && ins.Bits != 64 {
			return machine.ErrUnsupportedOperation
		}
		if ins.Long {
			p.emit(Instruction{Op: Bc, BO: 18, Imm: 8}) // bdz skips the long backedge
			p.emit(Instruction{Op: B, Imm: int64(ins.Imm) - int64(pc) - 4})
		} else {
			p.emit(Instruction{Op: Bc, BO: 16, Imm: int64(ins.Imm) - int64(pc)})
		}
	case machine.OpJump:
		p.emit(Instruction{Op: B, Imm: int64(ins.Imm) - int64(pc)})
	case machine.OpJumpIf:
		if isCarryBranch(ins.Condition) {
			if p.emitter.cpu >= abi.PPC64Power9 {
				p.emit(Instruction{Op: Mcrxrx, Imm: 7})
			} else {
				if ins.Condition == machine.BranchCarry32 || ins.Condition == machine.BranchNotCarry32 {
					return machine.ErrUnsupportedOperation
				}
				// Move CA to CR7.EQ without disturbing CR0 or XER.
				p.emit(Instruction{Op: Mfxer, A: 12})
				p.emit(Instruction{Op: Rldicl, A: 12, B: 12, Shift: 36, Mask: 28})
				p.emit(Instruction{Op: Mtcrf, A: 12, Imm: 1})
			}
			pc += len(p.instructions) * 4
		}
		condition := ins.Condition
		if ins.Long {
			condition ^= 1
		}
		bo, bi, err := branchBits(condition)
		if err != nil {
			return err
		}
		if ins.Long {
			p.emit(Instruction{Op: Bc, BO: bo, BI: bi, Imm: 8})
			p.emit(Instruction{Op: B, Imm: int64(ins.Imm) - int64(pc) - 4})
		} else {
			p.emit(Instruction{Op: Bc, BO: bo, BI: bi, Imm: int64(ins.Imm) - int64(pc)})
		}
	default:
		return fmt.Errorf("ppc64 allocated operation %d: %w", ins.Op, machine.ErrUnsupportedOperation)
	}
	return nil
}

// ImplicitWrites includes expansion temporaries, independently of allocation.
// Balanced platform frame adjustments do not destroy the caller's R1 value.
func (e *Emitter) ImplicitWrites(ins machine.Instruction) []machine.Register {
	sequence, err := e.Expand(ins, 0)
	if err != nil {
		return nil
	}
	var result []machine.Register
	seen := make(map[machine.Register]bool)
	for _, native := range sequence {
		if native.writesGeneral() && native.A != 1 && !seen[native.A] {
			seen[native.A] = true
			result = append(result, native.A)
		}
	}
	return result
}

func (ins Instruction) writesGeneral() bool {
	switch ins.Op {
	case Std, Stdx, Stdbrx, Stb, Stdu, Stdux, Cmp, Cmpl, Cmpi, Cmpli, B, Bc, Blr, Mtlr, Mtctr, Bctrl, Bctr, Sc, Mcrxrx, Lfd, Stfd, Lfs, Fmr, Fneg, Fabs, Lvx, Stvx, Mtcrf, Mtvsrd, Xscvspdpn, Xscvdpspn, Xxsldwi, Fadd, Fsub, Fmul, Fdiv, Fmadd, Fsqrt, Frsp:
		return false
	default:
		return true
	}
}

// SpecialWrites reports the resources actually modified by each expansion.
func (e *Emitter) SpecialWrites(ins machine.Instruction) []machine.Resource {
	sequence, err := e.Expand(ins, 0)
	if err != nil {
		return nil
	}
	var result []machine.Resource
	seen := make(map[machine.Resource]bool)
	add := func(class machine.ResourceClass, number uint8) {
		resource := machine.Resource{Class: class, Number: number}
		if !seen[resource] {
			result = append(result, resource)
			seen[resource] = true
		}
	}
	for _, native := range sequence {
		if native.Record || native.Op == Cmp || native.Op == Cmpl || native.Op == Cmpi || native.Op == Cmpli || native.Op == Andi || native.Op == Sc {
			add(machine.ResourceConditionField, 0)
		}
		if native.Op == Mtcrf {
			for field := uint8(0); field < 8; field++ {
				if native.Imm&(1<<(7-field)) != 0 {
					add(machine.ResourceConditionField, field)
				}
			}
		}
		if native.Op == Mcrxrx {
			add(machine.ResourceConditionField, uint8(native.Imm))
		}
		if native.Op == Sradi || native.Op == Srad || native.Op == Sraw || native.Op == Adde || native.Op == Subfe || native.Op == Addze || native.Op == Subfze || native.Op == Subfic || native.Op == Addc || native.Op == Subfc || native.Op == Addic {
			add(machine.ResourceIntegerStatus, 0)
		}
	}
	if ins.Op == machine.OpSetCount || ins.Op == machine.OpLoop {
		add(machine.ResourceCountRegister, 0)
	}
	if ins.Op == machine.OpFloatUnary || ins.Op == machine.OpFloatBinary || ins.Op == machine.OpFloatFMA {
		add(machine.ResourceFloatingStatus, 0)
	}
	if ins.Op == machine.OpCallC || ins.Op == machine.OpCallGo {
		add(machine.ResourceLinkRegister, 0)
		add(machine.ResourceCountRegister, 0)
		add(machine.ResourceIntegerStatus, 0)
		add(machine.ResourceFloatingStatus, 0)
		for field := uint8(0); field < 8; field++ {
			if ins.Op == machine.OpCallC && field >= 2 && field <= 4 {
				continue
			}
			add(machine.ResourceConditionField, field)
		}
	}
	return result
}

// FlagEffect reports modifications of condition/status resources, including
// auxiliary CR fields used to branch on carry.
func (e *Emitter) FlagEffect(ins machine.Instruction) bool {
	for _, resource := range e.SpecialWrites(ins) {
		if resource.Class == machine.ResourceConditionField || resource.Class == machine.ResourceIntegerStatus {
			return true
		}
	}
	return false
}
