package compile

import (
	"fmt"
	"math/bits"
	"strconv"
	"sync"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	targetloong64 "github.com/moriyoshi/nyanjit/backend/target/loong64"
	"github.com/moriyoshi/nyanjit/ir"
)

// loong64BaselineArch is the baseline engine's loong64 part. It encodes
// through targetloong64's ByteAssembler methods directly; the listing uses
// the same assembler, whose plan keeps each instruction's text.
//
// LA64 has no integer flags: a pkCmp or pkCmpImm only records the operands
// of the following compare-and-branch (pkCmpImm materializes a nonzero
// immediate in R20), so the pair encodes as one beq/bne/blt/bge/bltu/bgeu.
// A conditional branch reaches ±128 KiB; the long form is the inverted
// branch over a b (±128 MiB).
//
// Registers: R12 is the engine's transit register and R18 its cycle
// breaker; R19 and R20 are the assembler's expansion temporaries (R20 is
// also the call target), F0-F2 its floating-point temporaries, and F2 the
// engine's floating-point scratch. R0-R3, R21 and R22 keep their ABI roles.
// R23 is the workspace register without a platform stack.
//
// Values narrower than 64 bits stay zero-extended in general registers. A
// binary32 result in a floating-point register is canonicalized with zero
// upper bits, so 64-bit bank moves and spills carry its exact bit pattern:
// LA64 leaves those bits undefined (QEMU sets them to ones).
type loong64BaselineArch struct{}

// loong64EmitState holds the reusable loong64 encoders in the engine's
// architecture-private emission state.
type loong64EmitState struct {
	asm, sizer           *targetloong64.ByteAssembler
	asmStack, sizerStack abi.StackPractice
	asmHomes, sizerHomes int
}

func loong64State(e *baselineEngine) *loong64EmitState {
	st, _ := e.emit.arch.(*loong64EmitState)
	if st == nil {
		st = &loong64EmitState{}
		e.emit.arch = st
	}
	return st
}

const (
	loong64Transit int8 = 12
	loong64Cycle   int8 = 18
	loong64R19     int8 = 19
	loong64R20     int8 = 20
	loong64F0      int8 = fpBase
	loong64F2      int8 = fpBase + 2
	loong64Disp    int  = 2047
)

var (
	loong64SavedGPPlatform = []int8{24, 25, 26, 27, 28, 31, 29, 30, 23}
	loong64SavedGPNoUse    = []int8{24, 25, 26, 27, 28, 31, 29, 30}
	loong64SavedFP         = []int8{fpBase + 24, fpBase + 25, fpBase + 26, fpBase + 27, fpBase + 28, fpBase + 29, fpBase + 30, fpBase + 31}
	// Entries take at most eight arguments (abi.MaxArgumentsFor), which
	// both ABIs pass in R4-R11.
	loong64Params    = []int8{4, 5, 6, 7, 8, 9, 10, 11}
	loong64Models    [2][2]baselineModel // by stack practice and Go entry
	loong64ModelOnce sync.Once
)

func (loong64BaselineArch) model(o compileOptions) baselineModel {
	loong64ModelOnce.Do(func() {
		for stack := range 2 {
			for goEntry := range 2 {
				m := baselineModel{
					calleeSavedGP: loong64SavedGPNoUse, calleeSavedFP: loong64SavedFP,
					params: loong64Params, results: [2]int8{4, 5},
					transit: loong64Transit, cycle: loong64Cycle, scratchFP: loong64F2,
					callTarget: loong64R20,
				}
				if abi.StackPractice(stack) == abi.StackPlatform {
					m.calleeSavedGP = loong64SavedGPPlatform
				}
				for r := int8(4); r <= 17; r++ {
					if r != loong64Transit {
						m.allocatable |= regBit(r)
					}
				}
				for _, r := range m.calleeSavedGP {
					m.allocatable |= regBit(r)
					m.saved |= regBit(r)
				}
				for r := int8(3); r < 32; r++ {
					m.allocatable |= regBit(fpBase + r)
				}
				for _, r := range m.calleeSavedFP {
					m.saved |= regBit(r)
				}
				loong64Models[stack][goEntry] = m
			}
		}
	})
	stack, goEntry := 0, 0
	if o.Stack == abi.StackPlatform {
		stack = 1
	}
	if o.ABI == abi.Go {
		goEntry = 1
	}
	m := loong64Models[stack][goEntry]
	m.goHomes = loong64GoHomes(o)
	m.spillLimit = maxSpillSlots
	if o.Stack == abi.StackPlatform {
		m.spillLimit -= 1 + m.goHomes
	}
	return m
}

func (loong64BaselineArch) foldable(r *bRec, k int, imm uint64) bool {
	if k != 1 {
		return false
	}
	switch r.op {
	case ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor:
		return (targetloong64.Policy{}).CanBinaryImmediate(r.op, r.bits, imm)
	}
	return false
}

func (loong64BaselineArch) lower(e *baselineEngine, r *bRec, dst int8, src [3]int8) error {
	switch r.op {
	case ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpMul, ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic:
		// The hot operations, written in place with their size.
		p := e.slot()
		p.k, p.sub, p.w, p.a, p.b, p.c, p.imm = pkOp, uint8(r.op), r.bits, dst, src[0], src[1], r.imm
		if src[1] < 0 && r.op != ir.OpShiftLeft && r.op != ir.OpShiftRight && r.op != ir.OpShiftRightArithmetic {
			p.k, p.imm = pkOpImm, e.recs[r.b].imm
		}
		p.sz = uint8(loong64OpSize(p))
		return nil
	}
	p := bPhys{k: pkOp, sub: uint8(r.op), w: r.bits, a: dst, b: src[0], c: src[1], d: src[2], imm: r.imm}
	switch r.op {
	case ir.OpFToSigned, ir.OpFToUnsigned:
		// ftintrz converts into F0.
		e.alloc.used |= regBit(loong64F0)
		p.d = int8(e.valueBits(r.a))
	case ir.OpCastUnsigned, ir.OpFFromSigned, ir.OpFFromUnsigned, ir.OpFCompare:
		p.d = int8(e.valueBits(r.a))
	case ir.OpLoad16, ir.OpLoad32, ir.OpStore16, ir.OpStore32:
		p.imm = uint64(r.ord)
	}
	e.put(p)
	return nil
}

// loong64Offset returns the base register and displacement of spill slot
// slot, as the assembler's spillAddress computes them.
func loong64Offset(e *baselineEngine, slot uint64) uint64 {
	if e.options.Stack == abi.StackPlatform {
		return (slot + 1 + uint64(e.m.goHomes)) * 8
	}
	return slot * 8
}

// loong64ConstSize is the size of the assembler's Constant expansion of v.
func loong64ConstSize(v uint64) int {
	if int64(v) >= -2048 && int64(v) <= 2047 || v <= 4095 || v&(1<<52-1) == 0 {
		return 4
	}
	n := 1
	low := uint64(int64(int32(v)))
	seed := uint64(int64(v<<52) >> 52)
	switch {
	case v&0xffffffff == seed&0xffffffff:
		low = seed
	case v&0xffffffff <= 4095:
		low = v & 4095
	default:
		if v&4095 != 0 {
			n++
		}
	}
	if low&(1<<52-1) != v&(1<<52-1) {
		n++
		low = uint64(int64(v<<12) >> 12)
	}
	if low != v {
		n++
	}
	return 4 * n
}

// loong64MemorySize is the size of a load or store at displacement d.
func loong64MemorySize(d uint64) int {
	if d <= uint64(loong64Disp) {
		return 4
	}
	return loong64ConstSize(d) + 8
}

func loong64AdjustSize(n int) int { return 4 * ((n + 2031) / 2032) }

func (loong64BaselineArch) size(e *baselineEngine, p *bPhys) int {
	switch p.k {
	case pkLabel, pkCmp:
		return 0
	case pkMove:
		if p.a == p.b {
			return 0
		}
		return 4
	case pkConst:
		if p.a >= fpBase {
			if p.imm == 0 {
				return 4
			}
			return loong64ConstSize(p.imm) + 4
		}
		return loong64ConstSize(p.imm)
	case pkSymbol:
		if p.a >= fpBase {
			return 20
		}
		return 16
	case pkSpill, pkReload:
		return loong64MemorySize(loong64Offset(e, p.imm))
	case pkCmpImm:
		if p.imm == 0 {
			return 0
		}
		return loong64ConstSize(p.imm)
	case pkOp, pkOpImm:
		if n := loong64OpSize(p); n >= 0 {
			return n
		}
	case pkJump, pkJumpIf, pkCall, pkSyscall, pkRet:
		return 4
	case pkJumpIfLong:
		return 8
	case pkFrameEnter, pkFrameLeave:
		if !e.alloc.hasCall {
			// R1 stays live without calls: the record is marked to only
			// move SP, and the link word stays reserved.
			p.sub = 1
			return loong64AdjustSize(int(p.imm))
		}
		return loong64AdjustSize(int(p.imm)) + 4
	}
	// Measure the rest by encoding them once.
	asm := loong64State(e).scratch(e)
	asm.Reset()
	if err := loong64EncodeOne(e, asm, p, 0, nil); err != nil || asm.Err() != nil {
		e.emit.sizeErr = true
		return 4
	}
	return asm.AssembledSize()
}

// loong64OpSize sizes an operation without encoding it, or returns -1 for
// one that must be measured.
func loong64OpSize(p *bPhys) int {
	w := p.w
	narrow := 0
	if w < 64 {
		narrow = 4
	}
	canonical := 0
	if w == 32 {
		canonical = 12
	}
	switch o := ir.Opcode(p.sub); o {
	case ir.OpAdd, ir.OpSub, ir.OpMul:
		return 4 + narrow
	case ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpLoad8, ir.OpLoad64, ir.OpStore8, ir.OpStore64, ir.OpShiftRight, ir.OpBorrowSub:
		return 4
	case ir.OpShiftLeft:
		return 4 + narrow
	case ir.OpShiftRightArithmetic:
		if w == 64 {
			return 4
		}
		return 12
	case ir.OpRotateLeft, ir.OpRotateRight:
		switch {
		case p.imm == 0 && p.a == p.b:
			return 0
		case p.imm == 0 || w == 64:
			return 4
		case w == 32:
			return 8
		}
		return 16
	case ir.OpShiftLeftBy, ir.OpShiftRightBy:
		return 12 + narrow
	case ir.OpShiftRightArithmeticBy:
		return 16 + 2*narrow
	case ir.OpCastUnsigned:
		if w >= uint8(p.d) {
			if p.a == p.b {
				return 0
			}
		}
		return 4
	case ir.OpSignExtend:
		return 4 + narrow
	case ir.OpLeadingZeros:
		if w < 32 {
			return 8
		}
		return 4
	case ir.OpTrailingZeros:
		switch w {
		case 8:
			return 8
		case 16:
			return 8 + loong64ConstSize(1<<16)
		}
		return 4
	case ir.OpMulHigh:
		return 4 + narrow
	case ir.OpMulHighSigned:
		switch w {
		case 64:
			return 4
		case 32:
			return 8
		}
		return 16
	case ir.OpCarryAdd:
		return 8
	case ir.OpAddWithCarry, ir.OpSubWithBorrow:
		return 12 + narrow
	case ir.OpSelect:
		return 12
	case ir.OpAddScaled:
		switch s := p.imm; {
		case s == 0:
			if p.a == p.b {
				return 0
			}
			return 4
		case s == 1 || s == 2 || s == 4 || s == 8 || s == 16:
			return 4 + narrow
		case s&(s-1) == 0:
			return 8 + narrow
		default:
			return loong64ConstSize(s) + 8 + narrow
		}
	case ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv, ir.OpFSqrt, ir.OpFMA, ir.OpFConvert:
		return 4 + canonical
	case ir.OpFFromSigned, ir.OpFFromUnsigned:
		if o == ir.OpFFromUnsigned && p.d == 64 {
			return -1
		}
		return 8 + canonical
	case ir.OpFCompare, ir.OpFProductError:
		return 8
	case ir.OpLoad16, ir.OpLoad32, ir.OpStore16, ir.OpStore32:
		if op.ByteOrder(p.imm) != op.ByteOrderBigEndian {
			return 4
		}
	}
	return -1
}

func (loong64BaselineArch) longBranch(p *bPhys, pc, target int) bool {
	d := target - pc
	return d < -(1<<17) || d >= 1<<17
}

func (loong64BaselineArch) frame(e *baselineEngine, slots int, hasCall bool) (int, error) {
	if e.options.Stack != abi.StackPlatform {
		return 0, nil
	}
	if slots == 0 && !hasCall {
		return 0, nil
	}
	frame := targetloong64.FrameBytes(slots+e.m.goHomes, hasCall)
	if frame > e.options.StackBytesAvailable {
		return 0, ErrStackRequired
	}
	return frame, nil
}

func (loong64BaselineArch) encoder(e *baselineEngine, text bool) baselineEncoder {
	homes := e.m.goHomes
	if text {
		return targetloong64.NewTextAssembler(e.options.Stack, homes)
	}
	c := loong64State(e)
	if c.asm == nil || c.asmStack != e.options.Stack || c.asmHomes != homes {
		c.asm = targetloong64.NewByteAssembler(e.options.Stack, homes)
		c.asmStack, c.asmHomes = e.options.Stack, homes
	} else {
		c.asm.Reset()
	}
	return c.asm
}

func (c *loong64EmitState) scratch(e *baselineEngine) *targetloong64.ByteAssembler {
	homes := e.m.goHomes
	if c.sizer == nil || c.sizerStack != e.options.Stack || c.sizerHomes != homes {
		c.sizer = targetloong64.NewByteAssembler(e.options.Stack, homes)
		c.sizerStack, c.sizerHomes = e.options.Stack, homes
	}
	return c.sizer
}

func (loong64BaselineArch) registerName(r int8) string {
	if r >= fpBase {
		return "F" + strconv.Itoa(int(r-fpBase))
	}
	return "R" + strconv.Itoa(int(r))
}

func (loong64BaselineArch) register(name string) (int8, bool) {
	if len(name) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(name[1:])
	if err != nil || n < 0 || n > 31 {
		return 0, false
	}
	switch name[0] {
	case 'R':
		return int8(n), true
	case 'F':
		return fpBase + int8(n), true
	}
	return 0, false
}

// adjustReport replaces the engine's arm64 defaults: LA64 has no flags, the
// workspace register is R23, the link register R1 is saved by the frame,
// expansions write R19 and R20, and floating-point status lives in FCSR0.
func (x loong64BaselineArch) adjustReport(u *baselineUnit, r *abi.EmissionReport) {
	f := &u.facts
	r.FlagsClobbered = false
	// R1 is written only by calls, which need a frame that restores it.
	written := (f.used | f.callers) &^ regBit(1)
	if f.used&(regBit(u.m.transit)|regBit(u.m.cycle)) != 0 || r.StackBytes != 0 || r.WorkspaceBytes != 0 {
		written |= regBit(loong64R19) | regBit(loong64R20)
	}
	r.ClobberedRegisters = r.ClobberedRegisters[:0]
	for m := written &^ u.m.saved & bankMaskGP; m != 0; m &= m - 1 {
		r.ClobberedRegisters = append(r.ClobberedRegisters, x.registerName(int8(bits.TrailingZeros64(m))))
	}
	r.ClobberedSpecialRegisters = nil
	if r.FPStatusModified {
		r.ClobberedSpecialRegisters = []string{"FCSR0"}
	}
}

// loong64Assembler returns the ByteAssembler behind enc.
func loong64Assembler(enc baselineEncoder) *targetloong64.ByteAssembler {
	if t, ok := enc.(*targetloong64.TextAssembler); ok {
		return t.ByteAssembler
	}
	return enc.(*targetloong64.ByteAssembler)
}

func (loong64BaselineArch) encode(e *baselineEngine, enc baselineEncoder, ps []bPhys, pc int, labels []int32) (int, error) {
	a := loong64Assembler(enc)
	for i := range ps {
		p := &ps[i]
		if err := loong64EncodeFast(e, a, p, pc, labels); err != nil {
			return pc, err
		}
		pc += int(p.sz)
	}
	return pc, nil
}

// loong64EncodeFast encodes the most frequent records with direct calls and
// defers the rest to loong64EncodeOne.
func loong64EncodeFast(e *baselineEngine, a *targetloong64.ByteAssembler, p *bPhys, pc int, labels []int32) error {
	switch p.k {
	case pkLabel, pkCmp:
		if p.k == pkCmp {
			a.CompareRegister(breg(p.b), breg(p.c))
		}
		return nil
	case pkMove:
		if p.a < fpBase && p.b < fpBase {
			a.Move(breg(p.a), breg(p.b))
			return nil
		}
	case pkConst:
		if p.a < fpBase {
			a.Constant(breg(p.a), p.imm)
			return nil
		}
	case pkSpill:
		if p.a < fpBase {
			a.Spill(breg(p.a), int(p.imm))
			return nil
		}
	case pkReload:
		if p.a < fpBase {
			a.Reload(breg(p.a), int(p.imm))
			return nil
		}
	case pkOp:
		if p.w == 64 {
			switch o := ir.Opcode(p.sub); o {
			case ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor:
				a.Binary(o, 64, breg(p.a), breg(p.b), breg(p.c))
				return nil
			case ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic:
				a.Shift(o, 64, breg(p.a), breg(p.b), uint8(p.imm))
				return nil
			}
		}
	case pkOpImm:
		if p.w == 64 {
			switch o := ir.Opcode(p.sub); o {
			case ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor:
				a.BinaryImmediate(o, 64, breg(p.a), breg(p.b), p.imm)
				return nil
			}
		}
	}
	return loong64EncodeOne(e, a, p, pc, labels)
}

func loong64FloatMove(a *targetloong64.ByteAssembler, d, s int8) {
	a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: breg(d), B: breg(s)})
}

func loong64EncodeOne(e *baselineEngine, a *targetloong64.ByteAssembler, p *bPhys, pc int, labels []int32) error {
	switch p.k {
	case pkLabel:
	case pkMove:
		if p.a == p.b {
			return nil
		}
		if p.a < fpBase && p.b < fpBase {
			a.Move(breg(p.a), breg(p.b))
		} else {
			loong64FloatMove(a, p.a, p.b)
		}
	case pkConst:
		switch {
		case p.a < fpBase:
			a.Constant(breg(p.a), p.imm)
		case p.imm == 0:
			loong64FloatMove(a, p.a, 0)
		default:
			a.Constant(breg(loong64Transit), p.imm)
			loong64FloatMove(a, p.a, loong64Transit)
		}
	case pkSymbol:
		name := e.symbols[p.imm]
		if p.a >= fpBase {
			a.Symbol(breg(loong64Transit), name)
			loong64FloatMove(a, p.a, loong64Transit)
		} else {
			a.Symbol(breg(p.a), name)
		}
	case pkSpill:
		if p.a >= fpBase {
			return a.FloatMemory(machine.Instruction{Op: machine.OpFloatSpill, Bits: 64, A: breg(p.a), Slot: int(p.imm)})
		}
		a.Spill(breg(p.a), int(p.imm))
	case pkReload:
		if p.a >= fpBase {
			return a.FloatMemory(machine.Instruction{Op: machine.OpFloatReload, Bits: 64, A: breg(p.a), Slot: int(p.imm)})
		}
		a.Reload(breg(p.a), int(p.imm))
	case pkOp, pkOpImm:
		a.SetPC(pc)
		return loong64EncodeOp(a, p)
	case pkCmp:
		a.CompareRegister(breg(p.b), breg(p.c))
	case pkCmpImm:
		a.CompareImmediate(breg(p.b), int64(p.imm))
	case pkJump:
		a.SetPC(pc)
		if labels != nil {
			a.Jump(int(labels[p.imm]))
		} else {
			a.Jump(pc)
		}
	case pkJumpIf, pkJumpIfLong:
		a.SetPC(pc)
		target := pc
		if labels != nil {
			target = int(labels[p.imm])
		}
		a.JumpIfRange(op.BranchCondition(p.sub), target, p.k == pkJumpIfLong, false)
	case pkCall:
		a.CallC()
	case pkSyscall:
		a.Syscall()
	case pkFrameEnter, pkFrameLeave:
		if p.sub == 0 {
			if p.k == pkFrameEnter {
				a.FrameEnter(int(p.imm))
			} else {
				a.FrameLeave(int(p.imm))
			}
			return nil
		}
		sign := int64(1)
		if p.k == pkFrameEnter {
			sign = -1
		}
		for n := int64(p.imm); n > 0; n -= 2032 {
			a.BinaryImmediate(op.OpAdd, 64, 3, 3, uint64(sign*min(n, 2032)))
		}
	case pkRet:
		a.Ret()
	default:
		return fmt.Errorf("baseline loong64: physical kind %d: %w", p.k, ErrInvalidFunction)
	}
	return nil
}

var loong64ShiftBy = [256]op.Opcode{
	ir.OpShiftLeftBy: op.OpShiftLeft, ir.OpShiftRightBy: op.OpShiftRight, ir.OpShiftRightArithmeticBy: op.OpShiftRightArithmetic,
	ir.OpRotateLeftBy: op.OpRotateLeft, ir.OpRotateRightBy: op.OpRotateRight,
}

// loong64Canonical clears the upper half of the floating-point register
// holding a binary32 result.
func loong64Canonical(a *targetloong64.ByteAssembler, d backend.Register) {
	r19 := breg(loong64R19)
	a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 32, A: r19, B: d})
	a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: d, B: r19})
}

// loong64EncodeOp encodes one IR operation with its registers assigned.
// Values narrower than 64 bits stay zero-extended, so results that may carry
// out of their width are narrowed again.
func loong64EncodeOp(a *targetloong64.ByteAssembler, p *bPhys) error {
	d, s0, s1, s2 := breg(p.a), breg(p.b), breg(p.c), breg(p.d)
	w := p.w
	o := ir.Opcode(p.sub)
	r19, r20 := breg(loong64R19), breg(loong64R20)
	switch o {
	case ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor:
		if p.k == pkOpImm {
			a.BinaryImmediate(o, 64, d, s0, p.imm)
		} else {
			a.Binary(o, 64, d, s0, s1)
		}
		if w < 64 && (o == ir.OpAdd || o == ir.OpSub || o == ir.OpMul) {
			a.Narrow(d, d, w)
		}
	case ir.OpAddScaled:
		if p.imm == 0 {
			a.Move(d, s0)
			return nil
		}
		a.Scaled(d, s0, s1, p.imm, 0)
		if w < 64 {
			a.Narrow(d, d, w)
		}
	case ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic:
		a.Shift(o, w, d, s0, uint8(p.imm))
		if w < 64 && o != ir.OpShiftRight {
			a.Narrow(d, d, w)
		}
	case ir.OpRotateLeft, ir.OpRotateRight:
		count := uint8(p.imm)
		a.Shift(o, w, d, s0, count)
		if count != 0 && w < 64 {
			a.Narrow(d, d, w)
		}
	case ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy:
		a.ShiftBy(loong64ShiftBy[o], w, d, s0, s1)
	case ir.OpCastUnsigned:
		if w >= uint8(p.d) {
			a.Move(d, s0)
		} else {
			a.Narrow(d, s0, w)
		}
	case ir.OpSignExtend:
		a.SignExtend(d, s0, uint8(p.imm))
		if w < 64 {
			a.Narrow(d, d, w)
		}
	case ir.OpLeadingZeros, ir.OpTrailingZeros:
		a.CountZeros(o, w, d, s0)
	case ir.OpMulHigh:
		a.MulHigh(w, d, s0, s1)
	case ir.OpMulHighSigned:
		a.MulHighSigned(w, d, s0, s1)
	case ir.OpCarryAdd:
		if w == 64 {
			a.WordCarry(machine.Instruction{Op: machine.OpCarryBit, Bits: 64, Arithmetic: op.OpAdd, A: d, B: s0, C: s1, CarryMode: machine.CarryOperands})
		} else {
			a.Binary(op.OpAdd, 64, r20, s0, s1)
			a.Shift(op.OpShiftRight, 64, d, r20, w)
		}
	case ir.OpBorrowSub:
		a.WordCarry(machine.Instruction{Op: machine.OpCarryBit, Bits: 64, Arithmetic: op.OpSub, A: d, B: s0, C: s1, CarryMode: machine.CarryOperands})
	case ir.OpAddWithCarry, ir.OpSubWithBorrow:
		arith := op.OpAdd
		if o == ir.OpSubWithBorrow {
			arith = op.OpSub
		}
		a.WordCarry(machine.Instruction{Op: machine.OpWordCarry, Bits: 64, Arithmetic: arith, A: d, B: s0, C: s1, D: s2, CarryMode: machine.CarryValue})
		if w < 64 {
			a.Narrow(d, d, w)
		}
	case ir.OpSelect:
		a.SelectValue(d, s0, s1, s2)
	case ir.OpDiv, ir.OpDivSigned, ir.OpRem, ir.OpRemSigned:
		a.Divide(machine.Instruction{Op: machine.OpDivide, Arithmetic: o, Bits: w, A: d, B: s0, C: s1, Imm: p.imm})
	case ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv:
		a.FloatBinary(o, w, d, s0, s1)
		if w == 32 {
			loong64Canonical(a, d)
		}
	case ir.OpFMA:
		a.FloatFMA(w, d, s0, s1, s2)
		if w == 32 {
			loong64Canonical(a, d)
		}
	case ir.OpFSqrt, ir.OpFConvert:
		a.FloatUnary(o, w, d, s0)
		if w == 32 {
			loong64Canonical(a, d)
		}
	case ir.OpFFromSigned, ir.OpFFromUnsigned:
		a.FloatConvert(machine.Instruction{Op: machine.OpIntToFloat, Arithmetic: o, Bits: w, Imm: uint64(p.d), A: d, B: s0})
		if w == 32 {
			loong64Canonical(a, d)
		}
	case ir.OpFToSigned, ir.OpFToUnsigned:
		a.FloatConvert(machine.Instruction{Op: machine.OpFloatToInt, Arithmetic: o, Bits: w, Disp: uint64(p.d), Imm: p.imm, A: d, B: s0})
	case ir.OpFCompare:
		a.FloatCompare(op.FloatCondition(p.imm), uint8(p.d), d, s0, s1)
	case ir.OpFProductError:
		f2 := breg(loong64F2)
		a.Float(machine.Instruction{Op: machine.OpFloatBits, Arithmetic: op.OpXor, Bits: 64, A: f2, B: s2})
		a.FloatFMA(64, d, s0, s1, f2)
	case ir.OpLoad64:
		a.Load64(d, s0)
	case ir.OpLoad8:
		a.Load8(d, s0)
	case ir.OpStore64:
		a.Store64(s0, s1)
	case ir.OpStore8:
		a.Store8(s0, s1)
	case ir.OpLoad16, ir.OpLoad32:
		n := uint8(16)
		if o == ir.OpLoad32 {
			n = 32
		}
		if op.ByteOrder(p.imm) == op.ByteOrderBigEndian {
			// Bytes in address order, most significant first.
			a.Move(r20, s0)
			a.Load8(d, r20)
			for i := uint8(1); i < n/8; i++ {
				a.BinaryImmediate(op.OpAdd, 64, r20, r20, 1)
				a.Load8(r19, r20)
				a.Shift(op.OpShiftLeft, 64, d, d, 8)
				a.Binary(op.OpOr, 64, d, d, r19)
			}
			return nil
		}
		mop := machine.OpLoad16
		if o == ir.OpLoad32 {
			mop = machine.OpLoad32
		}
		return a.NarrowMemory(machine.Instruction{Op: mop, A: d, B: s0})
	case ir.OpStore16, ir.OpStore32:
		n := uint8(16)
		if o == ir.OpStore32 {
			n = 32
		}
		if op.ByteOrder(p.imm) == op.ByteOrderBigEndian {
			a.Move(r20, s0)
			for i := uint8(0); i < n/8; i++ {
				if i != 0 {
					a.BinaryImmediate(op.OpAdd, 64, r20, r20, 1)
				}
				if shift := n - 8 - 8*i; shift != 0 {
					a.Shift(op.OpShiftRight, 64, r19, s1, shift)
					a.Store8(r20, r19)
				} else {
					a.Store8(r20, s1)
				}
			}
			return nil
		}
		mop := machine.OpStore16
		if o == ir.OpStore32 {
			mop = machine.OpStore32
		}
		return a.NarrowMemory(machine.Instruction{Op: mop, A: s0, B: s1})
	default:
		return fmt.Errorf("baseline loong64: %v: %w", o, ErrUnsupported)
	}
	return nil
}
