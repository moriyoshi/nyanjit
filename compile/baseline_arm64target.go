package compile

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	targetarm64 "github.com/moriyoshi/nyanjit/backend/target/arm64"
	"github.com/moriyoshi/nyanjit/ir"
)

// arm64BaselineArch is the baseline engine's arm64 part. It encodes through
// targetarm64's ByteAssembler and TextAssembler methods directly.
type arm64BaselineArch struct{}

// arm64Encoder is the method set the baseline uses from both arm64
// assemblers.
type arm64Encoder interface {
	SetPC(int)
	AssembledSize() int
	Err() error
	Constant(backend.Register, uint64)
	Symbol(backend.Register, string)
	Move(backend.Register, backend.Register)
	Binary(op.Opcode, uint8, backend.Register, backend.Register, backend.Register)
	BinaryImmediate(op.Opcode, uint8, backend.Register, backend.Register, uint64)
	BinaryNoFlags(op.Opcode, uint8, backend.Register, backend.Register, backend.Register)
	BinaryImmediateNoFlags(op.Opcode, uint8, backend.Register, backend.Register, uint64)
	Narrow(backend.Register, backend.Register, uint8)
	SignExtend(backend.Register, backend.Register, uint8)
	Shift(op.Opcode, uint8, backend.Register, backend.Register, uint8)
	Scaled(backend.Register, backend.Register, backend.Register, uint64, uint64)
	MulAdd(backend.Register, backend.Register, backend.Register, backend.Register)
	SelectValue(backend.Register, backend.Register, backend.Register, backend.Register)
	Load64(backend.Register, backend.Register)
	Store64(backend.Register, backend.Register)
	Load8(backend.Register, backend.Register)
	Store8(backend.Register, backend.Register)
	NarrowMemory(machine.Instruction) error
	Spill(backend.Register, int)
	Reload(backend.Register, int)
	FloatMemory(machine.Instruction) error
	Float(machine.Instruction)
	FloatUnary(op.Opcode, uint8, backend.Register, backend.Register)
	FloatBinary(op.Opcode, uint8, backend.Register, backend.Register, backend.Register)
	FloatFMA(uint8, backend.Register, backend.Register, backend.Register, backend.Register)
	FloatConvert(machine.Instruction)
	FloatCompare(op.FloatCondition, uint8, backend.Register, backend.Register, backend.Register)
	Divide(machine.Instruction)
	MulHigh(uint8, backend.Register, backend.Register, backend.Register)
	MulHighSigned(uint8, backend.Register, backend.Register, backend.Register)
	WordCarry(machine.Instruction)
	ShiftBy(op.Opcode, uint8, backend.Register, backend.Register, backend.Register)
	CountZeros(op.Opcode, uint8, backend.Register, backend.Register)
	CompareImmediate(backend.Register, int64)
	CompareRegister(backend.Register, backend.Register)
	Literal(machine.Instruction) error
	Jump(int)
	JumpIfForm(op.BranchCondition, int, bool)
	CallC()
	Syscall()
	FrameEnter(int)
	FrameLeave(int)
	Ret()
}

var (
	_ arm64Encoder = (*targetarm64.ByteAssembler)(nil)
	_ arm64Encoder = (*targetarm64.TextAssembler)(nil)
)

const (
	arm64X16 int8 = 16
	arm64X17 int8 = 17
	arm64V31 int8 = fpBase + 31
)

var (
	arm64SavedGP   = []int8{19, 20, 21, 22, 23, 24, 25, 26, 27}
	arm64SavedFP   = []int8{fpBase + 8, fpBase + 9, fpBase + 10, fpBase + 11, fpBase + 12, fpBase + 13, fpBase + 14, fpBase + 15}
	arm64ParamsC   = []int8{0, 1, 2, 3, 4, 5, 6, 7}
	arm64ParamsGo  = []int8{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	arm64Models    [2][2]baselineModel // by stack practice and Go entry
	arm64ModelOnce sync.Once
)

// model returns the arm64 register model. X16 and X17 are the engine's
// scratch registers, X18 is the platform register, X28 is Go's g, X29 and
// X30 are the frame and link registers; X8 is the workspace pointer without
// a platform stack. V31 is the floating-point scratch.
func (arm64BaselineArch) model(o compileOptions) baselineModel {
	arm64ModelOnce.Do(func() {
		for stack := range 2 {
			for goEntry := range 2 {
				m := baselineModel{
					calleeSavedGP: arm64SavedGP, calleeSavedFP: arm64SavedFP,
					params: arm64ParamsC, results: [2]int8{0, 1},
					transit: arm64X16, cycle: arm64X17, scratchFP: arm64V31,
					callTarget: arm64X16,
				}
				if goEntry == 1 {
					m.params = arm64ParamsGo
				}
				for r := int8(0); r <= 15; r++ {
					if r != 8 || abi.StackPractice(stack) == abi.StackPlatform {
						m.allocatable |= regBit(r)
					}
				}
				for _, r := range m.calleeSavedGP {
					m.allocatable |= regBit(r)
					m.saved |= regBit(r)
				}
				for r := int8(0); r < 31; r++ {
					m.allocatable |= regBit(fpBase + r)
				}
				for _, r := range m.calleeSavedFP {
					m.saved |= regBit(r)
				}
				arm64Models[stack][goEntry] = m
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
	m := arm64Models[stack][goEntry]
	m.goHomes = baselineGoHomes(o)
	m.spillLimit = maxSpillSlots
	if o.Stack == abi.StackPlatform {
		m.spillLimit -= 1 + m.goHomes
	}
	return m
}

func (arm64BaselineArch) foldable(r *bRec, k int, imm uint64) bool {
	if k != 1 {
		return false
	}
	switch r.op {
	case ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor:
		return (targetarm64.Policy{}).CanBinaryImmediate(r.op, arm64OpWidth(r.bits), imm)
	}
	return false
}

func arm64OpWidth(w uint8) uint8 {
	if w <= 32 {
		return 32
	}
	return 64
}

func (x arm64BaselineArch) lower(e *baselineEngine, r *bRec, dst int8, src [3]int8) error {
	switch r.op {
	case ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpMul, ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic:
		// The hot operations, written in place with their size.
		p := e.slot()
		p.k, p.sub, p.w, p.a, p.b, p.c, p.imm = pkOp, uint8(r.op), r.bits, dst, src[0], src[1], r.imm
		if src[1] < 0 && r.op != ir.OpShiftLeft && r.op != ir.OpShiftRight && r.op != ir.OpShiftRightArithmetic {
			p.k, p.imm = pkOpImm, e.recs[r.b].imm
		}
		p.sz = uint8(arm64FastOpSize(p))
		return nil
	}
	p := bPhys{k: pkOp, sub: uint8(r.op), w: r.bits, a: dst, b: src[0], c: src[1], d: src[2], imm: r.imm}
	switch r.op {
	case ir.OpCastUnsigned, ir.OpFFromSigned, ir.OpFFromUnsigned, ir.OpFToSigned, ir.OpFToUnsigned, ir.OpFCompare:
		p.d = int8(e.valueBits(r.a))
	case ir.OpLoad16, ir.OpLoad32, ir.OpStore16, ir.OpStore32:
		p.imm = uint64(r.ord)
		if (r.op == ir.OpStore16 || r.op == ir.OpStore32) && op.ByteOrder(r.ord) == op.ByteOrderBigEndian {
			p.k = pkOpImm
		}
	case ir.OpFMA:
		if !(targetarm64.Policy{}).SupportsFloatFMA() {
			return ErrBackendLimit
		}
	}
	e.put(p)
	return nil
}

// breg converts an engine register to the backend's numbering.
func breg(r int8) backend.Register {
	if r >= fpBase {
		return backend.VectorRegister(uint8(r - fpBase))
	}
	return backend.Register(r)
}

func arm64ImmediateWords(value uint64) int {
	zeros, ones := 0, 0
	for i := 0; i < 64; i += 16 {
		h := uint16(value >> i)
		if h != 0 {
			zeros++
		}
		if h != 0xffff {
			ones++
		}
	}
	return max(1, min(zeros, ones))
}

func (arm64BaselineArch) slotFits(e *baselineEngine, slot uint64) bool {
	off := slot * 8
	if e.options.Stack == abi.StackPlatform {
		off += uint64(1+e.m.goHomes) * 8
	}
	return off <= 32760
}

func (x arm64BaselineArch) size(e *baselineEngine, p *bPhys) int {
	switch p.k {
	case pkLabel:
		return 0
	case pkMove:
		if p.a == p.b {
			return 0
		}
		return 4
	case pkConst:
		n := 4 * arm64ImmediateWords(p.imm)
		if p.a >= fpBase {
			n += 4
		}
		return n
	case pkSymbol:
		if p.a >= fpBase {
			return 20
		}
		return 16
	case pkSpill, pkReload:
		if p.a < fpBase || x.slotFits(e, p.imm) {
			return 4
		}
	case pkOp, pkOpImm:
		if n := arm64FastOpSize(p); n != 0 {
			return n
		}
	case pkCmp, pkCmpImm, pkJump, pkJumpIf, pkCall, pkSyscall, pkRet:
		return 4
	case pkJumpIfLong:
		return 8
	case pkLiteral:
		if p.sub != 0 {
			return 20
		}
		return 4
	case pkFrameEnter:
		return targetarm64.FrameEnterSize(int(p.imm))
	case pkFrameLeave:
		return targetarm64.FrameLeaveSize(int(p.imm))
	}
	// Measure the rest by encoding them once.
	asm := e.emit.scratch(e)
	asm.Reset()
	if err := arm64EncodeOne(e, asm, p, 0, nil); err != nil || asm.Err() != nil {
		e.emit.sizeErr = true
		return 4
	}
	return asm.AssembledSize()
}

// arm64FastOpSize sizes the common operations without encoding them.
func arm64FastOpSize(p *bPhys) int {
	switch ir.Opcode(p.sub) {
	case ir.OpAdd, ir.OpSub, ir.OpMul:
		if p.w < 32 {
			return 8
		}
		return 4
	case ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpLoad8, ir.OpLoad64, ir.OpStore8, ir.OpStore64:
		return 4
	case ir.OpShiftLeft, ir.OpShiftRightArithmetic:
		if p.w < 32 {
			return 8
		}
		return 4
	case ir.OpShiftRight:
		return 4
	case ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv, ir.OpFSqrt, ir.OpFConvert, ir.OpFMA,
		ir.OpFFromSigned, ir.OpFFromUnsigned, ir.OpFToSigned, ir.OpFToUnsigned:
		return 4
	}
	return 0
}

func (arm64BaselineArch) longBranch(p *bPhys, pc, target int) bool {
	d := (target - pc) / 4
	return d < -(1<<18) || d >= 1<<18
}

func (arm64BaselineArch) frame(e *baselineEngine, slots int, hasCall bool) (int, error) {
	if e.options.Stack != abi.StackPlatform {
		return 0, nil
	}
	if slots == 0 && !hasCall {
		return 0, nil
	}
	frame := targetarm64.FrameBytes(slots+e.m.goHomes, hasCall)
	if frame > e.options.StackBytesAvailable {
		return 0, ErrStackRequired
	}
	return frame, nil
}

func (arm64BaselineArch) encoder(e *baselineEngine, text bool) baselineEncoder {
	homes := e.m.goHomes
	if text {
		return targetarm64.NewTextAssembler(e.options.Stack, homes)
	}
	if e.emit.asm == nil || e.emit.asmStack != e.options.Stack || e.emit.asmHomes != homes {
		e.emit.asm = targetarm64.NewByteAssembler(e.options.Stack, homes)
		e.emit.asmStack, e.emit.asmHomes = e.options.Stack, homes
	} else {
		e.emit.asm.Reset()
	}
	return e.emit.asm
}

func (s *baselineEmitState) scratch(e *baselineEngine) *targetarm64.ByteAssembler {
	homes := e.m.goHomes
	if s.sizer == nil || s.sizerStack != e.options.Stack || s.sizerHomes != homes {
		s.sizer = targetarm64.NewByteAssembler(e.options.Stack, homes)
		s.sizerStack, s.sizerHomes = e.options.Stack, homes
	}
	return s.sizer
}

func (arm64BaselineArch) registerName(r int8) string {
	if r >= fpBase {
		return "V" + strconv.Itoa(int(r-fpBase))
	}
	return "X" + strconv.Itoa(int(r))
}

func (arm64BaselineArch) register(name string) (int8, bool) {
	if len(name) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(name[1:])
	if err != nil || n < 0 {
		return 0, false
	}
	switch name[0] {
	case 'X':
		if n <= 30 {
			return int8(n), true
		}
	case 'V', 'D':
		if n <= 31 {
			return fpBase + int8(n), true
		}
	}
	return 0, false
}

func (arm64BaselineArch) encode(e *baselineEngine, enc baselineEncoder, ps []bPhys, pc int, labels []int32) (int, error) {
	if asm, ok := enc.(*targetarm64.ByteAssembler); ok {
		// The common path: concrete calls the compiler can inline.
		for i := range ps {
			p := &ps[i]
			if err := arm64EncodeFast(e, asm, p, pc, labels); err != nil {
				return pc, err
			}
			pc += int(p.sz)
		}
		return pc, nil
	}
	a := enc.(arm64Encoder)
	for i := range ps {
		p := &ps[i]
		if err := arm64EncodeOne(e, a, p, pc, labels); err != nil {
			return pc, err
		}
		pc += int(p.sz)
	}
	return pc, nil
}

// arm64EncodeFast encodes the most frequent records with direct calls and
// defers the rest to arm64EncodeOne.
func arm64EncodeFast(e *baselineEngine, a *targetarm64.ByteAssembler, p *bPhys, pc int, labels []int32) error {
	switch p.k {
	case pkLabel:
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
		w := p.w
		if w >= 32 {
			switch o := ir.Opcode(p.sub); o {
			case ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor:
				// The engine emits every compare right before its branch, so an
				// operation's flags are never read: use the flag-free forms.
				a.BinaryNoFlags(o, w, breg(p.a), breg(p.b), breg(p.c))
				return nil
			case ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic:
				a.Shift(o, w, breg(p.a), breg(p.b), uint8(p.imm))
				return nil
			}
		}
	case pkOpImm:
		if w := p.w; w >= 32 {
			switch o := ir.Opcode(p.sub); o {
			case ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor:
				arm64BinaryImmediate(a, o, w, breg(p.a), breg(p.b), p.imm)
				return nil
			}
		}
	}
	return arm64EncodeOne(e, a, p, pc, labels)
}

func arm64EncodeOne(e *baselineEngine, a arm64Encoder, p *bPhys, pc int, labels []int32) error {
	switch p.k {
	case pkLabel:
	case pkMove:
		if p.a == p.b {
			return nil
		}
		if p.a < fpBase && p.b < fpBase {
			a.Move(breg(p.a), breg(p.b))
		} else {
			a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: breg(p.a), B: breg(p.b)})
		}
	case pkConst:
		if p.a >= fpBase {
			a.Constant(breg(arm64X16), p.imm)
			a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: breg(p.a), B: breg(arm64X16)})
		} else {
			a.Constant(breg(p.a), p.imm)
		}
	case pkSymbol:
		name := e.symbols[p.imm]
		if p.a >= fpBase {
			a.Symbol(breg(arm64X16), name)
			a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: breg(p.a), B: breg(arm64X16)})
		} else {
			a.Symbol(breg(p.a), name)
		}
	case pkSpill:
		if p.a >= fpBase {
			return a.FloatMemory(machine.Instruction{Op: machine.OpFloatSpill, A: breg(p.a), Slot: int(p.imm)})
		}
		a.Spill(breg(p.a), int(p.imm))
	case pkReload:
		if p.a >= fpBase {
			return a.FloatMemory(machine.Instruction{Op: machine.OpFloatReload, A: breg(p.a), Slot: int(p.imm)})
		}
		a.Reload(breg(p.a), int(p.imm))
	case pkOp, pkOpImm:
		return arm64EncodeOp(a, p)
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
		a.JumpIfForm(op.BranchCondition(p.sub), target, p.k == pkJumpIfLong)
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
	case pkLiteral:
		a.SetPC(pc)
		return a.Literal(machine.Instruction{Op: machine.OpLiteralLoad, A: breg(p.a), Bits: 64,
			Disp: uint64(e.emit.poolBase + 8*int(p.imm)), Long: p.sub != 0})
	default:
		return fmt.Errorf("baseline arm64: physical kind %d: %w", p.k, ErrInvalidFunction)
	}
	return nil
}

var arm64ShiftBy = [256]op.Opcode{
	ir.OpShiftLeftBy: op.OpShiftLeft, ir.OpShiftRightBy: op.OpShiftRight, ir.OpShiftRightArithmeticBy: op.OpShiftRightArithmetic,
	ir.OpRotateLeftBy: op.OpRotateLeft, ir.OpRotateRightBy: op.OpRotateRight,
}

// arm64EncodeOp encodes one IR operation with its registers assigned. Values
// narrower than 64 bits stay zero-extended, so 8- and 16-bit results that
// may carry out are narrowed again.
func arm64EncodeOp(a arm64Encoder, p *bPhys) error {
	d, s0, s1, s2 := breg(p.a), breg(p.b), breg(p.c), breg(p.d)
	w := p.w
	o := ir.Opcode(p.sub)
	bw := arm64OpWidth(w)
	x16, x17 := breg(arm64X16), breg(arm64X17)
	switch o {
	case ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor:
		if p.k == pkOpImm {
			arm64BinaryImmediate(a, o, bw, d, s0, p.imm)
		} else {
			a.BinaryNoFlags(o, bw, d, s0, s1)
		}
		if w < 32 && (o == ir.OpAdd || o == ir.OpSub || o == ir.OpMul) {
			a.Narrow(d, d, w)
		}
	case ir.OpAddScaled:
		switch scale := p.imm; {
		case scale == 0:
			a.Move(d, s0)
			return nil
		case scale&(scale-1) == 0:
			a.Scaled(d, s0, s1, scale, 0)
		default:
			a.Constant(x16, scale)
			a.MulAdd(d, s0, s1, x16)
		}
		if w < 64 {
			a.Narrow(d, d, w)
		}
	case ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic:
		count := uint8(p.imm)
		switch {
		case w >= 32:
			a.Shift(o, w, d, s0, count)
		case o == ir.OpShiftRight:
			a.Shift(o, 32, d, s0, count)
		case o == ir.OpShiftLeft:
			a.Shift(o, 32, d, s0, count)
			a.Narrow(d, d, w)
		default:
			a.Shift(o, w, d, s0, count)
			a.Narrow(d, d, w)
		}
	case ir.OpRotateLeft, ir.OpRotateRight:
		count := uint8(p.imm)
		if w >= 32 {
			a.Shift(o, w, d, s0, count)
			return nil
		}
		k := count
		if o == ir.OpRotateLeft {
			k = (w - count) % w
		}
		if k == 0 {
			a.Move(d, s0)
			return nil
		}
		a.Shift(op.OpShiftRight, 32, x17, s0, k)
		a.Shift(op.OpShiftLeft, 32, d, s0, w-k)
		a.Binary(op.OpOr, 32, d, d, x17)
		a.Narrow(d, d, w)
	case ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy:
		a.ShiftBy(arm64ShiftBy[o], w, d, s0, s1)
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
			a.Binary(op.OpAdd, 64, x16, s0, s1)
			a.Shift(op.OpShiftRight, 64, d, x16, w)
		}
	case ir.OpBorrowSub:
		a.WordCarry(machine.Instruction{Op: machine.OpCarryBit, Bits: 64, Arithmetic: op.OpSub, A: d, B: s0, C: s1, CarryMode: machine.CarryOperands})
	case ir.OpAddWithCarry, ir.OpSubWithBorrow:
		arith := op.OpAdd
		if o == ir.OpSubWithBorrow {
			arith = op.OpSub
		}
		a.WordCarry(machine.Instruction{Op: machine.OpWordCarry, Bits: bw, Arithmetic: arith, A: d, B: s0, C: s1, D: s2, CarryMode: machine.CarryValue})
		if w < 32 {
			a.Narrow(d, d, w)
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
		v31 := breg(arm64V31)
		a.Float(machine.Instruction{Op: machine.OpFloatBits, Arithmetic: op.OpXor, Bits: 64, A: v31, B: s2})
		a.FloatFMA(64, d, s0, s1, v31)
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
			a.Move(x16, s0)
			a.Load8(d, x16)
			for i := uint8(1); i < n/8; i++ {
				a.BinaryImmediate(op.OpAdd, 64, x16, x16, 1)
				a.Load8(x17, x16)
				a.Shift(op.OpShiftLeft, 64, d, d, 8)
				a.Binary(op.OpOr, 64, d, d, x17)
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
			a.Move(x16, s0)
			for i := uint8(0); i < n/8; i++ {
				if i != 0 {
					a.BinaryImmediate(op.OpAdd, 64, x16, x16, 1)
				}
				if shift := n - 8 - 8*i; shift != 0 {
					a.Shift(op.OpShiftRight, 64, x17, s1, shift)
					a.Store8(x16, x17)
				} else {
					a.Store8(x16, s1)
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
		return fmt.Errorf("baseline arm64: %v: %w", o, ErrUnsupported)
	}
	return nil
}

// arm64BinaryImmediate encodes an operation whose flags are never read. A
// flag-free ADD, SUB or AND immediate cannot write XZR (register 31 names SP
// there), so that case keeps the flag-setting form.
func arm64BinaryImmediate(a arm64Encoder, o ir.Opcode, w uint8, d, s backend.Register, imm uint64) {
	if d == 31 {
		a.BinaryImmediate(o, w, d, s, imm)
		return
	}
	a.BinaryImmediateNoFlags(o, w, d, s, imm)
}

// literalsLong reports whether a load in code of size bytes may be beyond
// LDR (literal)'s 1 MiB reach.
func (arm64BaselineArch) literalsLong(size int) bool { return size >= 1<<20-4 }

func (arm64BaselineArch) encodePool(e *baselineEngine, enc baselineEncoder, pc, base int, pool []uint64) error {
	return enc.(arm64Encoder).Literal(machine.Instruction{Op: machine.OpLiteralData, Data: baselinePoolBytes(e, pc, base, pool)})
}

// arm64FloatBranch maps a floating-point condition to the integer branch
// condition that holds after FCMP exactly when it does (MI and PL are the
// encodings 10 and 11). FCMP sets NZCV to 0110 when equal, 1000 when less,
// 0010 when greater and 0011 when unordered.
var arm64FloatBranch = [...]struct {
	c  op.BranchCondition
	ok bool
}{
	op.FloatEqual:                 {op.BranchEqual, true},
	op.FloatNotEqual:              {op.BranchNotEqual, true},
	op.FloatLess:                  {10, true}, // MI
	op.FloatUnorderedGreaterEqual: {11, true}, // PL
	op.FloatLessEqual:             {op.BranchBelowEqual, true},
	op.FloatUnorderedGreater:      {op.BranchAbove, true},
	op.FloatGreater:               {op.BranchGreater, true},
	op.FloatUnorderedLessEqual:    {op.BranchLessEqual, true},
	op.FloatGreaterEqual:          {op.BranchGreaterEqual, true},
	op.FloatUnorderedLess:         {op.BranchLess, true},
	op.FloatOrdered:               {},
	op.FloatUnordered:             {},
	op.FloatOrderedNotEqual:       {},
	op.FloatUnorderedEqual:        {},
}

// floatBranch: FloatCompare ends with FCMP and CSET, and moves, spills,
// reloads and constant and literal loads leave the flags.
func (arm64BaselineArch) floatBranch(e *baselineEngine, r *bRec, between []bPhys) (op.BranchCondition, bool) {
	if r.imm >= uint64(len(arm64FloatBranch)) || !arm64FloatBranch[r.imm].ok {
		return 0, false
	}
	for i := range between {
		switch p := &between[i]; p.k {
		case pkMove, pkConst, pkLiteral:
		case pkSpill, pkReload:
			if p.sz != 4 {
				return 0, false
			}
		default:
			return 0, false
		}
	}
	return arm64FloatBranch[r.imm].c, true
}
