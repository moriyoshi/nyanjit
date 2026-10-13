package compile

import (
	"fmt"
	"strconv"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	targetriscv64 "github.com/moriyoshi/nyanjit/backend/target/riscv64"
	"github.com/moriyoshi/nyanjit/ir"
)

// riscv64BaselineArch is the baseline engine's riscv64 part. It encodes
// through targetriscv64's ByteAssembler methods directly; the TextAssembler
// embeds a ByteAssembler, so listings replay the same calls.
//
// Register model. X28 and X29 are the engine's transit and cycle registers;
// X30 and X31 are the encoders' expansion temporaries, and X31 also holds an
// outbound call's target. X0-X4 (zero, ra, sp, gp, tp), X8 (the frame
// pointer), X9 (the workspace register without a platform stack) and X27
// (Go's g) are never allocated. S2-S10 (X18-X26) and FS0-FS11 are the
// callee-saved registers Vars are pinned to. F0-F2 stay free because the
// float encoders route GP operands through them, and F31 is the
// floating-point scratch.
//
// Values. RISC-V has no flags: a compare records its operand pair in the
// assembler and the following conditional branch encodes it. A value
// narrower than 64 bits is zero-extended in a general register, as the
// optimized tier keeps it, so W-form results and narrow arithmetic are
// zero-extended again. In a floating-point register a 32-bit value is a
// NaN-boxed single. Every spill slot holds the general-register form, so a
// 32-bit value crosses banks through fmv.x.w/fmv.w.x, and a 32-bit
// floating-point spill or reload goes through X31.
type riscv64BaselineArch struct{}

const (
	rvX28 int8 = 28
	rvX29 int8 = 29
	rvF31 int8 = fpBase + 31

	rvX0  backend.Register = 0
	rvX30 backend.Register = 30
	rvX31 backend.Register = 31
)

var (
	rvSavedGP = []int8{18, 19, 20, 21, 22, 23, 24, 25, 26}
	rvSavedFP = []int8{fpBase + 8, fpBase + 9, fpBase + 18, fpBase + 19, fpBase + 20, fpBase + 21,
		fpBase + 22, fpBase + 23, fpBase + 24, fpBase + 25, fpBase + 26, fpBase + 27}
	rvParams = []int8{10, 11, 12, 13, 14, 15, 16, 17}
	rvModel  = func() baselineModel {
		m := baselineModel{
			calleeSavedGP: rvSavedGP, calleeSavedFP: rvSavedFP,
			params: rvParams, results: [2]int8{10, 11},
			transit: rvX28, cycle: rvX29, scratchFP: rvF31,
			callTarget: int8(rvX31),
		}
		for _, r := range []int8{5, 6, 7, 10, 11, 12, 13, 14, 15, 16, 17} {
			m.allocatable |= regBit(r)
		}
		for r := int8(3); r <= 30; r++ {
			m.allocatable |= regBit(fpBase + r)
		}
		for _, r := range m.calleeSavedGP {
			m.allocatable |= regBit(r)
			m.saved |= regBit(r)
		}
		for _, r := range m.calleeSavedFP {
			m.saved |= regBit(r)
		}
		return m
	}()
)

func (riscv64BaselineArch) model(o compileOptions) baselineModel {
	m := rvModel
	m.goHomes = riscv64GoHomes(o)
	m.spillLimit = maxSpillSlots
	if o.Stack == abi.StackPlatform {
		m.spillLimit -= 1 + m.goHomes
	}
	return m
}

// riscv64EmitState is the engine's reusable riscv64 state: the CPU profile
// and the encoders for the final bytes and for measuring.
type riscv64EmitState struct {
	target  *resolvedTarget
	profile abi.ResolvedCPU
	zbb, fd bool
	stack   abi.StackPractice
	homes   int
	asm     *targetriscv64.ByteAssembler
	sizer   *targetriscv64.ByteAssembler
}

func rvState(e *baselineEngine) *riscv64EmitState {
	st, _ := e.emit.arch.(*riscv64EmitState)
	if st == nil || st.target != e.target {
		st = &riscv64EmitState{target: e.target, profile: resolvedFeatures(e.options)}
		st.zbb = st.profile.Has("zbb")
		st.fd = st.profile.Has("f") && st.profile.Has("d")
		e.emit.arch = st
	}
	if st.stack != e.options.Stack || st.homes != e.m.goHomes {
		st.asm, st.sizer = nil, nil
		st.stack, st.homes = e.options.Stack, e.m.goHomes
	}
	return st
}

func (st *riscv64EmitState) newAssembler() *targetriscv64.ByteAssembler {
	return targetriscv64.NewByteAssembler(st.stack, st.homes).WithCPU(st.profile)
}

func (riscv64BaselineArch) foldable(r *bRec, k int, imm uint64) bool {
	if k != 1 {
		return false
	}
	switch r.op {
	case ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor:
		return (targetriscv64.Policy{}).CanBinaryImmediate(r.op, r.bits, imm)
	}
	return false
}

func (x riscv64BaselineArch) lower(e *baselineEngine, r *bRec, dst int8, src [3]int8) error {
	switch r.op {
	case ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpMul, ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic:
		// The hot operations, written in place with their size.
		p := e.slot()
		p.k, p.sub, p.w, p.a, p.b, p.c, p.imm = pkOp, uint8(r.op), r.bits, dst, src[0], src[1], r.imm
		if src[1] < 0 && r.op != ir.OpShiftLeft && r.op != ir.OpShiftRight && r.op != ir.OpShiftRightArithmetic {
			p.k, p.imm = pkOpImm, e.recs[r.b].imm
		}
		p.sz = uint8(rvFastOpSize(p))
		return nil
	}
	p := bPhys{k: pkOp, sub: uint8(r.op), w: r.bits, a: dst, b: src[0], c: src[1], d: src[2], imm: r.imm}
	switch r.op {
	case ir.OpCastUnsigned, ir.OpFFromSigned, ir.OpFFromUnsigned, ir.OpFToSigned, ir.OpFToUnsigned, ir.OpFCompare:
		p.d = int8(e.valueBits(r.a))
	case ir.OpLoad16, ir.OpLoad32, ir.OpStore16, ir.OpStore32:
		p.imm = uint64(r.ord)
		if r.fl&rfAligned != 0 {
			p.imm |= 1 << 8
		}
	case ir.OpFMA, ir.OpFProductError:
		if !rvState(e).fd {
			return ErrBackendLimit
		}
	}
	e.put(p)
	return nil
}

// rvConstWords counts the words of targetriscv64.ConstantWords(value)
// without building them.
func rvConstWords(value int64) int {
	if value >= -2048 && value <= 2047 {
		return 1
	}
	if value >= -1<<31 && value <= 1<<31-1 {
		upper := (value + 2048) >> 12
		if value-upper<<12 != 0 {
			return 2
		}
		return 1
	}
	lower := int64(uint64(value) & 0xfff)
	if lower >= 2048 {
		lower -= 4096
	}
	upper := value >> 12
	if lower < 0 {
		upper++
	}
	n := rvConstWords(upper) + 1
	if lower != 0 {
		n++
	}
	return n
}

// rvSlotSize is the size of a load or store of slot through the assembler's
// memoryBase: one word in the 12-bit range, otherwise the offset in X30 and
// an add before it.
func rvSlotSize(e *baselineEngine, slot uint64) int {
	off := int64(slot) * 8
	if e.options.Stack == abi.StackPlatform {
		off += int64(1+e.m.goHomes) * 8
	}
	if off >= -2048 && off <= 2047 {
		return 4
	}
	return 4 * (rvConstWords(off) + 2)
}

func rvNarrowSize(w uint8) int {
	switch {
	case w >= 64:
		return 0
	case w == 8:
		return 4
	}
	return 8
}

func (x riscv64BaselineArch) size(e *baselineEngine, p *bPhys) int {
	switch p.k {
	case pkLabel, pkCmp:
		return 0
	case pkMove:
		switch {
		case p.a == p.b:
			return 0
		case p.a < fpBase && p.b >= fpBase && p.w == 32:
			return 12
		}
		return 4
	case pkConst:
		if p.a < fpBase {
			return 4 * rvConstWords(int64(p.imm))
		}
		if p.imm == 0 {
			return 4
		}
		return 4 + 4*rvConstWords(int64(p.imm))
	case pkSymbol:
		if p.a >= fpBase {
			return 64
		}
		return 60
	case pkSpill:
		n := rvSlotSize(e, p.imm)
		if p.a >= fpBase && p.w == 32 {
			n += 12
		}
		return n
	case pkReload:
		n := rvSlotSize(e, p.imm)
		if p.a >= fpBase && p.w == 32 {
			n += 4
		}
		return n
	case pkCmpImm:
		if p.imm == 0 {
			return 0
		}
		return 4 * rvConstWords(int64(p.imm))
	case pkOp, pkOpImm:
		if n := rvFastOpSize(p); n != 0 {
			return n
		}
	case pkJump:
		if p.sub != 0 {
			return 8
		}
		return 4
	case pkJumpIf, pkCall, pkSyscall, pkRet:
		return 4
	case pkJumpIfLong:
		if p.w != 0 {
			return 12
		}
		return 8
	case pkFrameEnter:
		return targetriscv64.FrameEnterSize(int(p.imm))
	case pkFrameLeave:
		return targetriscv64.FrameLeaveSize(int(p.imm))
	}
	// Measure the rest by encoding them once.
	st := rvState(e)
	if st.sizer == nil {
		st.sizer = st.newAssembler()
	}
	asm := st.sizer
	asm.Reset()
	if err := rvEncodeOne(e, st, asm, p, 0, nil); err != nil || asm.Err() != nil {
		e.emit.sizeErr = true
		return 4
	}
	return asm.AssembledSize()
}

// rvFastOpSize sizes the common operations without encoding them, or
// returns 0.
func rvFastOpSize(p *bPhys) int {
	switch ir.Opcode(p.sub) {
	case ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpShiftLeft:
		return 4 + rvNarrowSize(p.w)
	case ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpShiftRight, ir.OpLoad8, ir.OpLoad64, ir.OpStore8, ir.OpStore64:
		return 4
	case ir.OpShiftRightArithmetic:
		if p.w >= 64 {
			return 4
		}
		return 8 + rvNarrowSize(p.w)
	case ir.OpSignExtend:
		n := 8
		if p.imm == 32 {
			n = 4
		}
		return n + rvNarrowSize(p.w)
	case ir.OpCastUnsigned:
		if p.w >= uint8(p.d) {
			if p.a == p.b {
				return 0
			}
			return 4
		}
		return rvNarrowSize(p.w)
	case ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv, ir.OpFSqrt, ir.OpFConvert, ir.OpFMA,
		ir.OpFFromSigned, ir.OpFFromUnsigned:
		return 4
	}
	return 0
}

// rvJalFits reports whether a JAL reaches distance bytes.
func rvJalFits(distance int) bool { return distance >= -(1<<20) && distance < 1<<20 }

func (riscv64BaselineArch) longBranch(p *bPhys, pc, target int) bool {
	d := target - pc
	return d < -(1<<12) || d >= 1<<12
}

// relax grows a jump beyond JAL's reach to AUIPC+JALR, and a long
// conditional branch, whose JAL follows the inverted branch, to the far
// form with AUIPC+JALR.
func (riscv64BaselineArch) relax(e *baselineEngine, p *bPhys, pc, target int) bool {
	switch p.k {
	case pkJump:
		if p.sub == 0 && !rvJalFits(target-pc) {
			p.sub, p.sz = 1, 8
			return true
		}
	case pkJumpIfLong:
		if p.w == 0 && !rvJalFits(target-(pc+4)) {
			p.w, p.sz = 1, 12
			return true
		}
	}
	return false
}

// fixCallArguments sign-extends 32-bit C arguments, which LP64 passes as
// sign-extended XLEN words, as the optimized tier does.
func (riscv64BaselineArch) fixCallArguments(e *baselineEngine, r *bRec, args []uint32, dsts []int8) {
	if r.op != ir.OpCallC {
		return
	}
	for k := 1; k < len(args); k++ {
		if e.valueBits(args[k]) == 32 {
			e.put(bPhys{k: pkOp, sub: uint8(ir.OpSignExtend), w: 64, a: dsts[k], b: dsts[k], imm: 32})
		}
	}
}

func (riscv64BaselineArch) frame(e *baselineEngine, slots int, hasCall bool) (int, error) {
	// RISC-V has no flags; the expansion temporaries are reported written.
	e.alloc.flags = false
	e.alloc.used |= regBit(int8(rvX30)) | regBit(int8(rvX31))
	if e.options.Stack != abi.StackPlatform {
		return 0, nil
	}
	if slots == 0 && !hasCall {
		return 0, nil
	}
	frame := targetriscv64.FrameBytes(slots+e.m.goHomes, hasCall)
	if frame > e.options.StackBytesAvailable {
		return 0, ErrStackRequired
	}
	return frame, nil
}

func (riscv64BaselineArch) encoder(e *baselineEngine, text bool) baselineEncoder {
	st := rvState(e)
	if text {
		t := targetriscv64.NewTextAssembler(e.options.Stack, e.m.goHomes)
		t.WithCPU(st.profile)
		return t
	}
	if st.asm == nil {
		st.asm = st.newAssembler()
	} else {
		st.asm.Reset()
	}
	return st.asm
}

func (riscv64BaselineArch) registerName(r int8) string {
	if r >= fpBase {
		return "F" + strconv.Itoa(int(r-fpBase))
	}
	return "X" + strconv.Itoa(int(r))
}

func (riscv64BaselineArch) register(name string) (int8, bool) {
	if len(name) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(name[1:])
	if err != nil || n < 0 || n > 31 {
		return 0, false
	}
	switch name[0] {
	case 'X':
		return int8(n), true
	case 'F':
		return fpBase + int8(n), true
	}
	return 0, false
}

func (riscv64BaselineArch) encode(e *baselineEngine, enc baselineEncoder, ps []bPhys, pc int, labels []int32) (int, error) {
	var a *targetriscv64.ByteAssembler
	switch x := enc.(type) {
	case *targetriscv64.ByteAssembler:
		a = x
	case *targetriscv64.TextAssembler:
		a = x.ByteAssembler
	default:
		return pc, fmt.Errorf("baseline riscv64: encoder %T: %w", enc, ErrInvalidFunction)
	}
	st := rvState(e)
	for i := range ps {
		p := &ps[i]
		before := a.AssembledSize()
		if err := rvEncodeOne(e, st, a, p, pc, labels); err != nil {
			return pc, err
		}
		if n := a.AssembledSize() - before; n != int(p.sz) && a.Err() == nil {
			return pc, fmt.Errorf("baseline riscv64: record %d (kind %d, op %d) encoded %d bytes, sized %d: %w", i, p.k, p.sub, n, p.sz, ErrInvalidFunction)
		}
		pc += int(p.sz)
	}
	return pc, nil
}

// rvFloatBits is the bank-crossing width of a w-bit value: 32-bit values
// are NaN-boxed singles in floating-point registers.
func rvFloatBits(w uint8) uint8 {
	if w == 32 {
		return 32
	}
	return 64
}

// rvConstant materializes value in general register d.
func rvConstant(a *targetriscv64.ByteAssembler, d backend.Register, value uint64) {
	if v := int64(value); v >= -2048 && v <= 2047 {
		a.BinaryImmediate(op.OpAdd, 64, d, rvX0, value)
		return
	}
	a.Constant(d, value)
}

func rvMove(a *targetriscv64.ByteAssembler, dst, src int8, w uint8) {
	switch {
	case dst == src:
	case dst < fpBase && src < fpBase:
		a.Move(breg(dst), breg(src))
	case dst >= fpBase && src >= fpBase:
		a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: breg(dst), B: breg(src)})
	default:
		a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: rvFloatBits(w), A: breg(dst), B: breg(src)})
	}
}

func rvEncodeOne(e *baselineEngine, st *riscv64EmitState, a *targetriscv64.ByteAssembler, p *bPhys, pc int, labels []int32) error {
	switch p.k {
	case pkLabel:
	case pkMove:
		rvMove(a, p.a, p.b, p.w)
	case pkConst:
		if p.a < fpBase {
			rvConstant(a, breg(p.a), p.imm)
			return nil
		}
		src := rvX0
		if p.imm != 0 {
			rvConstant(a, rvX30, p.imm)
			src = rvX30
		}
		a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: rvFloatBits(p.w), A: breg(p.a), B: src})
	case pkSymbol:
		name := e.symbols[p.imm]
		if p.a < fpBase {
			a.Symbol(breg(p.a), name)
			return nil
		}
		a.Symbol(rvX30, name)
		a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: rvFloatBits(p.w), A: breg(p.a), B: rvX30})
	case pkSpill:
		switch {
		case p.a < fpBase:
			a.Spill(breg(p.a), int(p.imm))
		case p.w == 32:
			a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 32, A: rvX31, B: breg(p.a)})
			a.Spill(rvX31, int(p.imm))
		default:
			return a.FloatMemory(machine.Instruction{Op: machine.OpFloatSpill, Bits: 64, A: breg(p.a), Slot: int(p.imm)})
		}
	case pkReload:
		switch {
		case p.a < fpBase:
			a.Reload(breg(p.a), int(p.imm))
		case p.w == 32:
			a.Reload(rvX31, int(p.imm))
			a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 32, A: breg(p.a), B: rvX31})
		default:
			return a.FloatMemory(machine.Instruction{Op: machine.OpFloatReload, Bits: 64, A: breg(p.a), Slot: int(p.imm)})
		}
	case pkOp, pkOpImm:
		return rvEncodeOp(st, a, p, pc)
	case pkCmp:
		a.CompareRegister(breg(p.b), breg(p.c))
	case pkCmpImm:
		if p.imm == 0 {
			a.CompareImmediate(breg(p.b), 0)
			return nil
		}
		rvConstant(a, rvX31, p.imm)
		a.CompareRegister(breg(p.b), rvX31)
	case pkJump:
		a.SetPC(pc)
		target := pc
		if labels != nil {
			target = int(labels[p.imm])
		}
		a.JumpForm(target, p.sub != 0)
	case pkJumpIf, pkJumpIfLong:
		a.SetPC(pc)
		target := pc
		if labels != nil {
			target = int(labels[p.imm])
		}
		a.JumpIfRange(op.BranchCondition(p.sub), target, p.k == pkJumpIfLong, p.k == pkJumpIfLong && p.w != 0)
	case pkCall:
		a.CallC()
	case pkSyscall:
		a.Syscall()
	case pkFrameEnter:
		a.FrameEnter(int(p.imm))
	case pkFrameLeave:
		a.FrameLeave(int(p.imm))
	case pkRet:
		a.Ret()
	default:
		return fmt.Errorf("baseline riscv64: physical kind %d: %w", p.k, ErrInvalidFunction)
	}
	return nil
}

var rvShiftBy = [256]op.Opcode{
	ir.OpShiftLeftBy: op.OpShiftLeft, ir.OpShiftRightBy: op.OpShiftRight, ir.OpShiftRightArithmeticBy: op.OpShiftRightArithmetic,
	ir.OpRotateLeftBy: op.OpRotateLeft, ir.OpRotateRightBy: op.OpRotateRight,
}

// rvNarrow zero-extends the low w bits of s into d.
func rvNarrow(a *targetriscv64.ByteAssembler, d, s backend.Register, w uint8) {
	switch {
	case w >= 64:
		a.Move(d, s)
	case w == 8:
		a.BinaryImmediate(op.OpAnd, 64, d, s, 0xff)
	default:
		a.Narrow(d, s, w)
	}
}

// rvEncodeOp encodes one IR operation with its registers assigned.
func rvEncodeOp(st *riscv64EmitState, a *targetriscv64.ByteAssembler, p *bPhys, pc int) error {
	d, s0, s1, s2 := breg(p.a), breg(p.b), breg(p.c), breg(p.d)
	w := p.w
	o := ir.Opcode(p.sub)
	switch o {
	case ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor:
		if p.k == pkOpImm {
			a.BinaryImmediate(o, 64, d, s0, p.imm)
		} else {
			a.Binary(o, 64, d, s0, s1)
		}
		if w < 64 && (o == ir.OpAdd || o == ir.OpSub || o == ir.OpMul) {
			rvNarrow(a, d, d, w)
		}
	case ir.OpAddScaled:
		if p.imm == 0 {
			a.Move(d, s0)
			return nil
		}
		a.Scaled(d, s0, s1, p.imm, 0)
		if w < 64 {
			rvNarrow(a, d, d, w)
		}
	case ir.OpShiftLeft:
		a.Shift(o, 64, d, s0, uint8(p.imm))
		if w < 64 {
			rvNarrow(a, d, d, w)
		}
	case ir.OpShiftRight:
		a.Shift(o, 64, d, s0, uint8(p.imm))
	case ir.OpShiftRightArithmetic:
		a.Shift(o, w, d, s0, uint8(p.imm))
		if w < 64 {
			rvNarrow(a, d, d, w)
		}
	case ir.OpRotateLeft, ir.OpRotateRight:
		a.Shift(o, w, d, s0, uint8(p.imm))
		if w < 64 {
			rvNarrow(a, d, d, w)
		}
	case ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy:
		a.ShiftBy(rvShiftBy[o], w, d, s0, s1)
	case ir.OpCastUnsigned:
		if w >= uint8(p.d) {
			a.Move(d, s0)
		} else {
			rvNarrow(a, d, s0, w)
		}
	case ir.OpSignExtend:
		a.SignExtend(d, s0, uint8(p.imm))
		if w < 64 {
			rvNarrow(a, d, d, w)
		}
	case ir.OpLeadingZeros, ir.OpTrailingZeros:
		if st.zbb && (w >= 32 || o == ir.OpLeadingZeros) {
			a.CountZeros(o, w, d, s0)
		} else {
			rvCountZeros(a, o == ir.OpLeadingZeros, w, d, s0, pc)
		}
	case ir.OpMulHigh:
		a.MulHigh(w, d, s0, s1)
	case ir.OpMulHighSigned:
		a.MulHighSigned(w, d, s0, s1)
	case ir.OpCarryAdd:
		if w == 64 {
			a.WordCarry(machine.Instruction{Op: machine.OpCarryBit, Bits: 64, Arithmetic: op.OpAdd, A: d, B: s0, C: s1})
		} else {
			a.Binary(op.OpAdd, 64, rvX30, s0, s1)
			a.Shift(op.OpShiftRight, 64, d, rvX30, w)
		}
	case ir.OpBorrowSub:
		a.WordCarry(machine.Instruction{Op: machine.OpCarryBit, Bits: 64, Arithmetic: op.OpSub, A: d, B: s0, C: s1})
	case ir.OpAddWithCarry, ir.OpSubWithBorrow:
		arith := op.OpAdd
		if o == ir.OpSubWithBorrow {
			arith = op.OpSub
		}
		a.WordCarry(machine.Instruction{Op: machine.OpWordCarry, Bits: 64, Arithmetic: arith, A: d, B: s0, C: s1, D: s2, CarryMode: machine.CarryValue})
		if w < 64 {
			rvNarrow(a, d, d, w)
		}
	case ir.OpSelect:
		a.SelectValue(d, s0, s1, s2)
	case ir.OpDiv, ir.OpDivSigned, ir.OpRem, ir.OpRemSigned:
		a.Divide(machine.Instruction{Op: machine.OpDivide, Arithmetic: o, Bits: w, A: d, B: s0, C: s1, Imm: p.imm})
	case ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv:
		a.FloatBinary(o, w, d, s0, s1)
	case ir.OpFMA:
		a.FloatFMA(w, d, s0, s1, s2)
	case ir.OpFSqrt, ir.OpFConvert:
		a.FloatUnary(o, w, d, s0)
	case ir.OpFFromSigned, ir.OpFFromUnsigned:
		a.FloatConvert(machine.Instruction{Op: machine.OpIntToFloat, Arithmetic: o, Bits: w, Imm: uint64(p.d), A: d, B: s0})
	case ir.OpFToSigned, ir.OpFToUnsigned:
		a.FloatConvert(machine.Instruction{Op: machine.OpFloatToInt, Arithmetic: o, Bits: w, Disp: uint64(p.d), Imm: p.imm, A: d, B: s0})
	case ir.OpFCompare:
		a.FloatCompare(op.FloatCondition(p.imm), uint8(p.d), d, s0, s1)
	case ir.OpFProductError:
		f31 := breg(rvF31)
		a.Float(machine.Instruction{Op: machine.OpFloatBits, Arithmetic: op.OpXor, Bits: w, A: f31, B: s2})
		a.FloatFMA(w, d, s0, s1, f31)
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
		bigEndian := op.ByteOrder(p.imm&0xff) == op.ByteOrderBigEndian
		if bigEndian || p.imm&(1<<8) == 0 {
			// Byte by byte: big-endian order, or an address that may be
			// misaligned on a strict-alignment ISA.
			a.Move(rvX30, s0)
			a.Load8(d, rvX30)
			for i := uint8(1); i < n/8; i++ {
				a.BinaryImmediate(op.OpAdd, 64, rvX30, rvX30, 1)
				a.Load8(rvX31, rvX30)
				if bigEndian {
					a.Shift(op.OpShiftLeft, 64, d, d, 8)
				} else {
					a.Shift(op.OpShiftLeft, 64, rvX31, rvX31, 8*i)
				}
				a.Binary(op.OpOr, 64, d, d, rvX31)
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
		bigEndian := op.ByteOrder(p.imm&0xff) == op.ByteOrderBigEndian
		if bigEndian || p.imm&(1<<8) == 0 {
			a.Move(rvX30, s0)
			for i := uint8(0); i < n/8; i++ {
				if i != 0 {
					a.BinaryImmediate(op.OpAdd, 64, rvX30, rvX30, 1)
				}
				shift := 8 * i
				if bigEndian {
					shift = n - 8 - 8*i
				}
				if shift != 0 {
					a.Shift(op.OpShiftRight, 64, rvX31, s1, shift)
					a.Store8(rvX30, rvX31)
				} else {
					a.Store8(rvX30, s1)
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
		return fmt.Errorf("baseline riscv64: %v: %w", o, ErrUnsupported)
	}
	return nil
}

// rvCountZeros counts the leading or trailing zeros of the w-bit value in
// s without Zbb, by a binary search with short branches. It uses X30 and
// X31; pc is the PC of its first word.
func rvCountZeros(a *targetriscv64.ByteAssembler, leading bool, w uint8, d, s backend.Register, pc int) {
	start := a.AssembledSize()
	here := func() int { return pc + a.AssembledSize() - start }
	skip := func(condition op.BranchCondition, reg backend.Register, words int) {
		a.CompareImmediate(reg, 0)
		at := here()
		a.SetPC(at)
		a.JumpIfRange(condition, at+4*(words+1), false, false)
	}
	steps := 0
	for sh := uint8(32); sh >= 1; sh /= 2 {
		if sh < w {
			steps++
		}
	}
	a.Move(rvX30, s)
	if leading {
		// n = w; while the value has bits above sh, drop them; n - x.
		rvConstant(a, d, uint64(w))
		for sh := uint8(32); sh >= 1; sh /= 2 {
			if sh >= w {
				continue
			}
			a.Shift(op.OpShiftRight, 64, rvX31, rvX30, sh)
			skip(op.BranchEqual, rvX31, 2)
			a.BinaryImmediate(op.OpAdd, 64, d, d, uint64(-int64(sh)))
			a.Move(rvX30, rvX31)
		}
		a.Binary(op.OpSub, 64, d, d, rvX30)
		return
	}
	// Zero gives w; otherwise add sh while the low sh bits are zero.
	rvConstant(a, d, uint64(w))
	skip(op.BranchEqual, rvX30, 1+4*steps)
	rvConstant(a, d, 0)
	for sh := uint8(32); sh >= 1; sh /= 2 {
		if sh >= w {
			continue
		}
		a.Shift(op.OpShiftLeft, 64, rvX31, rvX30, 64-sh)
		skip(op.BranchNotEqual, rvX31, 2)
		a.BinaryImmediate(op.OpAdd, 64, d, d, uint64(sh))
		a.Shift(op.OpShiftRight, 64, rvX30, rvX30, sh)
	}
}
