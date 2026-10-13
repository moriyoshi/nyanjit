package compile

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"sync"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	targetamd64 "github.com/moriyoshi/nyanjit/backend/target/amd64"
	"github.com/moriyoshi/nyanjit/ir"
)

// amd64BaselineArch is the baseline engine's amd64 part. It encodes through
// targetamd64's ByteAssembler and TextAssembler methods directly.
//
// Register model. R11 is the transit register and the call target; the
// assembler's expansions also use it as their temporary, which is safe
// because the engine holds a value in R11 only inside a move sequence. R10
// is the cycle-breaking register with a platform stack and the workspace
// pointer without one; there the cycle register is RAX (C) or R12 (Go). RSP
// is never allocated, nor RBP, R14 (Go's g) or R15 (Go's GOT register).
// XMM0 is the floating-point scratch and XMM15, Go's zero register, is never
// written; XMM1-XMM14 are allocated.
//
// Two-address forms copy and then operate; an operand that aliases the
// destination in the wrong position is swapped when the operation commutes
// or copied to a scratch register. Fixed registers (RDX:RAX for DIV, IDIV and
// one-operand MUL/IMUL, CL for variable shifts without BMI2) are vacated
// before the operation: a live value there moves to a free register or to
// memory. Flags never survive an operation; the engine emits each compare
// immediately before its branch.
type amd64BaselineArch struct{}

// amd64Encoder is the method set the baseline uses from both amd64
// assemblers.
type amd64Encoder interface {
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
	JumpIf(op.BranchCondition, int)
	CallC()
	CallGo()
	Syscall()
	FrameEnter(int)
	FrameLeave(int)
	Ret()
}

var (
	_ amd64Encoder = (*targetamd64.ByteAssembler)(nil)
	_ amd64Encoder = (*targetamd64.TextAssembler)(nil)
)

const (
	amd64RAX int8 = 0
	amd64RCX int8 = 1
	amd64RDX int8 = 2
	amd64R10 int8 = 10
	amd64R11 int8 = 11
	amd64R12 int8 = 12
	amd64X0  int8 = fpBase
)

var (
	amd64SavedC    = []int8{3, 12, 13, 14, 15}
	amd64SavedGo   = []int8{12, 13}
	amd64SavedGoWS = []int8{13}
	amd64ParamsC   = []int8{7, 6, 2, 1, 8, 9}
	amd64ParamsGo  = []int8{0, 3, 1, 7, 6, 8}
	amd64Models    [2][2]baselineModel // by platform stack and Go entry
	amd64ModelOnce sync.Once
)

// amd64ByteOut is the native encoder. Its Bytes copies, because the
// assembler is reset and reused for the engine's next function.
type amd64ByteOut struct{ *targetamd64.ByteAssembler }

func (o *amd64ByteOut) Bytes() []byte {
	if b := o.ByteAssembler.Bytes(); b != nil {
		return slices.Clone(b)
	}
	return nil
}

type amd64AsmKey struct {
	stack abi.StackPractice
	homes int
	cpu   abi.ResolvedCPU
}

// amd64BaselineEmit holds the amd64 encoders an engine reuses and the CPU
// features of its Target.
type amd64BaselineEmit struct {
	out      amd64ByteOut
	outKey   amd64AsmKey
	sizer    *targetamd64.ByteAssembler
	sizerKey amd64AsmKey

	cpuFor                 *resolvedTarget
	cpuOK                  bool
	cpu                    abi.ResolvedCPU
	fma, bmi2, lzcnt, bmi1 bool
}

// features returns the state with the CPU features of e's Target.
func (s *amd64BaselineEmit) features(e *baselineEngine) *amd64BaselineEmit {
	if !s.cpuOK || s.cpuFor != e.options.target || e.options.target == nil {
		s.cpu = resolvedFeatures(e.options)
		s.cpuFor, s.cpuOK = e.options.target, true
		s.fma, s.bmi2, s.lzcnt, s.bmi1 = s.cpu.Has("fma"), s.cpu.Has("bmi2"), s.cpu.Has("lzcnt"), s.cpu.Has("bmi1")
	}
	return s
}

func (s *amd64BaselineEmit) scratch(e *baselineEngine) *targetamd64.ByteAssembler {
	s.features(e)
	key := amd64AsmKey{e.options.Stack, e.m.goHomes, s.cpu}
	if s.sizer == nil || s.sizerKey != key {
		s.sizer = targetamd64.NewByteAssembler(key.stack, key.homes).WithCPU(key.cpu)
		s.sizerKey = key
	}
	return s.sizer
}

// model returns the amd64 register model.
func (amd64BaselineArch) model(o compileOptions) baselineModel {
	amd64ModelOnce.Do(func() {
		for platform := range 2 {
			for goEntry := range 2 {
				m := baselineModel{
					results: [2]int8{amd64RAX, amd64RDX},
					transit: amd64R11, cycle: amd64R10, scratchFP: amd64X0,
					callTarget: amd64R11, callPush: 8,
					// DIV, IDIV, MUL and shifts by CL vacate these.
					pinAvoid: regBit(amd64RAX) | regBit(amd64RCX) | regBit(amd64RDX),
				}
				var general []int8
				if goEntry == 1 {
					m.params = amd64ParamsGo
					general = []int8{0, 3, 1, 2, 7, 6, 8, 9, 12, 13}
					m.calleeSavedGP = amd64SavedGo
					if platform == 0 {
						m.cycle, m.calleeSavedGP = amd64R12, amd64SavedGoWS
					}
					// Go's internal ABI preserves no general register,
					// so nothing is saved; R12 and R13 still survive C
					// calls and so hold pinned Vars.
				} else {
					m.params = amd64ParamsC
					general = []int8{0, 1, 2, 6, 7, 8, 9, 3, 12, 13, 14, 15}
					m.calleeSavedGP = amd64SavedC
					if platform == 0 {
						m.cycle = amd64RAX
					}
					for _, r := range m.calleeSavedGP {
						m.saved |= regBit(r)
					}
				}
				for _, r := range general {
					if r != m.cycle {
						m.allocatable |= regBit(r)
					}
				}
				for r := int8(1); r < 15; r++ {
					m.allocatable |= regBit(fpBase + r)
				}
				amd64Models[platform][goEntry] = m
			}
		}
	})
	platform, goEntry := 0, 0
	if o.Stack == abi.StackPlatform {
		platform = 1
	}
	if o.ABI == abi.Go {
		goEntry = 1
	}
	m := amd64Models[platform][goEntry]
	m.spillLimit = maxSpillSlots
	if platform == 1 && goEntry == 1 {
		// Go argument homes below the spill slots and the frame's top
		// word, as the optimized tier reserves them.
		m.goHomes = 7
		m.spillLimit -= m.goHomes + 1
	}
	return m
}

func (amd64BaselineArch) foldable(r *bRec, k int, imm uint64) bool {
	if k != 1 {
		return false
	}
	switch r.op {
	case ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor:
		return (targetamd64.Policy{}).CanBinaryImmediate(r.op, amd64OpWidth(r.bits), imm)
	}
	return false
}

func amd64OpWidth(w uint8) uint8 {
	if w <= 32 {
		return 32
	}
	return 64
}

var amd64ShiftBy = [256]op.Opcode{
	ir.OpShiftLeftBy: op.OpShiftLeft, ir.OpShiftRightBy: op.OpShiftRight, ir.OpShiftRightArithmeticBy: op.OpShiftRightArithmetic,
	ir.OpRotateLeftBy: op.OpRotateLeft, ir.OpRotateRightBy: op.OpRotateRight,
}

// amd64BMIShift reports whether the variable shift uses SHLX, SHRX or SARX,
// which need no CL.
func amd64BMIShift(s *amd64BaselineEmit, operation op.Opcode, w uint8) bool {
	return s.bmi2 && (operation == op.OpShiftLeft || operation == op.OpShiftRight || operation == op.OpShiftRightArithmetic && w == 64)
}

func (x amd64BaselineArch) lower(e *baselineEngine, r *bRec, dst int8, src [3]int8) error {
	switch r.op {
	case ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpMul, ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic:
		// The hot operations, written in place with their size.
		p := e.slot()
		p.k, p.sub, p.w, p.a, p.b, p.c, p.imm = pkOp, uint8(r.op), r.bits, dst, src[0], src[1], r.imm
		if src[1] < 0 && r.op != ir.OpShiftLeft && r.op != ir.OpShiftRight && r.op != ir.OpShiftRightArithmetic {
			p.k, p.imm = pkOpImm, e.recs[r.b].imm
		}
		p.sz = uint8(x.size(e, p))
		return nil
	}
	s := e.emit.amd64.features(e)
	p := bPhys{k: pkOp, sub: uint8(r.op), w: r.bits, a: dst, b: src[0], c: src[1], d: src[2], imm: r.imm}
	switch r.op {
	case ir.OpCastUnsigned, ir.OpFFromSigned, ir.OpFFromUnsigned, ir.OpFToSigned, ir.OpFToUnsigned, ir.OpFCompare:
		p.d = int8(e.valueBits(r.a))
	case ir.OpLoad16, ir.OpLoad32, ir.OpStore16, ir.OpStore32:
		p.imm = uint64(r.ord)
	case ir.OpFMA:
		if !s.fma {
			return fmt.Errorf("baseline amd64: FMA without the fma CPU feature: %w", ErrBackendLimit)
		}
	case ir.OpDiv, ir.OpDivSigned, ir.OpRem, ir.OpRemSigned:
		amd64Vacate(e, regBit(amd64RAX)|regBit(amd64RDX), dst, src)
	case ir.OpMulHigh:
		if r.bits == 64 {
			if s.bmi2 {
				amd64Vacate(e, regBit(amd64RDX), dst, src)
			} else {
				amd64Vacate(e, regBit(amd64RAX)|regBit(amd64RDX), dst, src)
			}
		}
	case ir.OpMulHighSigned:
		if r.bits == 64 {
			amd64Vacate(e, regBit(amd64RAX)|regBit(amd64RDX), dst, src)
		}
	case ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy:
		if !amd64BMIShift(s, amd64ShiftBy[r.op], r.bits) {
			amd64Vacate(e, regBit(amd64RCX), dst, src)
		}
	case ir.OpFProductError:
		if !s.fma {
			amd64ProductError(e, dst, src)
			return nil
		}
	}
	e.put(p)
	return nil
}

// amd64Vacate frees the fixed registers in mask, which the expansion of the
// record being lowered writes. A value there that is live after the record
// moves to a free register outside mask and the record's registers, or to
// memory; the record still reads its operands from where they were. The
// destination keeps its register: expansions write it last.
func amd64Vacate(e *baselineEngine, mask uint64, dst int8, src [3]int8) {
	a := &e.alloc
	avoid := mask
	for _, r := range src {
		if r >= 0 {
			avoid |= regBit(r)
		}
	}
	if dst >= 0 {
		avoid |= regBit(dst)
	}
	for m := mask; m != 0; m &= m - 1 {
		r := int8(trailingZeros(m))
		v := a.regVal[r]
		if r == dst || v == noValue {
			continue
		}
		if t := a.free & e.m.allocatable &^ a.pinned &^ avoid & bankMaskGP; t != 0 {
			to := int8(trailingZeros(t))
			e.put(bPhys{k: pkMove, a: to, b: r})
			e.unbind(r)
			e.bind(v, to)
			continue
		}
		e.evict(r)
	}
	a.used |= mask
}

// amd64ProductError lowers OpFProductError without FMA as the optimized tier
// does: Dekker's split of both factors and the exact sum of the partial
// products, in four temporary registers and the FP scratch. d is written
// last, so it may alias an operand.
func amd64ProductError(e *baselineEngine, d int8, src [3]int8) {
	lock := regBit(d)
	for _, r := range src {
		lock |= regBit(r)
	}
	var t [4]int8
	for k := range t {
		t[k] = e.allocReg(bankFP, lock, -1, false)
		lock |= regBit(t[k])
		e.alloc.used |= regBit(t[k])
	}
	x, y, p := src[0], src[1], src[2]
	x0 := e.m.scratchFP
	op := func(o ir.Opcode, d, l, r int8) {
		e.put(bPhys{k: pkOp, sub: uint8(o), w: 64, a: d, b: l, c: r})
	}
	e.put(bPhys{k: pkConst, a: x0, imm: math.Float64bits(134217729)})
	op(ir.OpFMul, t[0], x0, x)
	op(ir.OpFSub, t[1], t[0], x)
	op(ir.OpFSub, t[0], t[0], t[1]) // high part of x
	op(ir.OpFSub, t[1], x, t[0])    // low part of x
	op(ir.OpFMul, t[2], x0, y)
	op(ir.OpFSub, t[3], t[2], y)
	op(ir.OpFSub, t[2], t[2], t[3]) // high part of y
	op(ir.OpFSub, t[3], y, t[2])    // low part of y
	op(ir.OpFMul, x0, t[0], t[2])
	op(ir.OpFSub, x0, x0, p)
	op(ir.OpFMul, t[0], t[0], t[3])
	op(ir.OpFAdd, x0, x0, t[0])
	op(ir.OpFMul, t[2], t[2], t[1])
	op(ir.OpFAdd, x0, x0, t[2])
	op(ir.OpFMul, t[1], t[1], t[3])
	op(ir.OpFAdd, d, x0, t[1])
}

// amd64ConstSize is the size of MOV r, imm in its shortest form.
func amd64ConstSize(r int8, v uint64) int {
	switch {
	case v <= 0xffffffff:
		if r >= 8 {
			return 6
		}
		return 5
	case int64(v) >= -1<<31 && int64(v) <= 1<<31-1:
		return 7
	}
	return 10
}

func amd64MoveSize(d, s int8) int {
	switch {
	case d == s:
		return 0
	case d < fpBase && s < fpBase:
		return 3
	case d >= fpBase && s >= fpBase:
		if d >= fpBase+8 || s >= fpBase+8 {
			return 5
		}
		return 4
	}
	return 5
}

func (x amd64BaselineArch) size(e *baselineEngine, p *bPhys) int {
	switch p.k {
	case pkLabel:
		return 0
	case pkMove:
		return amd64MoveSize(p.a, p.b)
	case pkConst:
		if p.a < fpBase {
			return amd64ConstSize(p.a, p.imm)
		}
		return amd64ConstSize(amd64R11, p.imm) + 5
	case pkSymbol:
		if p.a < fpBase {
			return 10
		}
		return 15
	case pkSpill, pkReload:
		if p.a < fpBase {
			if e.options.Stack == abi.StackPlatform {
				return 8
			}
			return 7
		}
		return targetamd64.FloatMemorySize(machine.Instruction{Op: machine.OpFloatSpill, A: breg(p.a), Slot: int(p.imm)}, e.options.Stack, e.m.goHomes)
	case pkCmp:
		return 3
	case pkCmpImm, pkFrameEnter, pkFrameLeave:
		return 7
	case pkJump:
		return 5
	case pkJumpIf, pkJumpIfLong:
		return 6
	case pkCall:
		if ir.Opcode(p.sub) == ir.OpCallGo {
			return 3
		}
		return 5
	case pkSyscall:
		return 2
	case pkRet:
		return 1
	case pkLiteral:
		if p.a >= fpBase+8 {
			return 9
		}
		return 8
	case pkOp, pkOpImm:
		if n := amd64FastOpSize(p); n != 0 {
			return n
		}
	}
	// Measure the rest by encoding them once.
	asm := e.emit.amd64.scratch(e)
	asm.Reset()
	if err := amd64EncodeOne(e, asm, p, 0, nil); err != nil || asm.Err() != nil {
		e.emit.sizeErr = true
		return 1
	}
	return asm.AssembledSize()
}

// amd64LeaRegSize sizes LEA d, [l + r], with the operand order the
// assembler picks.
func amd64LeaRegSize(w uint8, d, l, r int8) int {
	base, index := l, r
	if index == 4 || base&7 == 5 && index&7 != 5 {
		base, index = index, base
	}
	n := 3 // opcode, ModRM, SIB
	if w == 64 || d >= 8 || base >= 8 || index >= 8 {
		n++
	}
	if base&7 == 5 {
		n++
	}
	return n
}

// amd64LeaDisp returns the displacement of the LEA that adds or subtracts
// imm at width w (32 or 64), and whether one exists.
func amd64LeaDisp(o ir.Opcode, w uint8, imm uint64) (uint64, bool) {
	disp := imm
	if o == ir.OpSub {
		disp = -disp
	}
	if w != 64 {
		return uint64(int64(int32(uint32(disp)))), true
	}
	return disp, int64(disp) >= -1<<31 && int64(disp) <= 1<<31-1
}

func amd64LeaImmSize(w uint8, d, base int8, disp uint64) int {
	n := 2
	if w == 64 || d >= 8 || base >= 8 {
		n++
	}
	if base&7 == 4 {
		n++
	}
	switch {
	case disp == 0 && base&7 != 5:
	case int64(disp) >= -128 && int64(disp) <= 127:
		n++
	default:
		n += 4
	}
	return n
}

func amd64MemSize(base int8, extra int) int {
	n := 3 + extra
	if base&7 == 4 || base&7 == 5 {
		n++
	}
	return n
}

// amd64FastOpSize sizes the common operations without encoding them; it
// mirrors amd64EncodeOp and returns 0 for the rest.
func amd64FastOpSize(p *bPhys) int {
	d, l, r, w := p.a, p.b, p.c, p.w
	mov := func(d, s int8) int {
		if d == s {
			return 0
		}
		return 3
	}
	switch o := ir.Opcode(p.sub); o {
	case ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpMul:
		if p.k == pkOpImm {
			if w >= 32 && d != l && (o == ir.OpAdd || o == ir.OpSub) {
				if disp, ok := amd64LeaDisp(o, w, p.imm); ok {
					return amd64LeaImmSize(w, d, l, disp)
				}
			}
			n := mov(d, l) + 7
			if int64(p.imm) >= -128 && int64(p.imm) <= 127 {
				n = mov(d, l) + 4
			}
			if w < 32 && o != ir.OpAnd {
				n += 4
			}
			return n
		}
		if o == ir.OpAdd && w >= 32 && d != l && d != r {
			return amd64LeaRegSize(w, d, l, r)
		}
		n := 0
		if d == r && d != l {
			if o == ir.OpSub {
				n, r = 3, amd64R11
			} else {
				l, r = r, l
			}
		}
		n += mov(d, l)
		switch {
		case o != ir.OpMul && w == 16:
			return n + 4
		case o != ir.OpMul:
			return n + 3
		case w == 8:
			return n + 8
		case w == 16:
			return n + 5
		}
		return n + 4
	case ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic:
		n := mov(d, l) + 4
		if w == 16 {
			n++
		}
		return n
	case ir.OpLoad64, ir.OpStore64:
		return amd64MemSize(l, 0)
	case ir.OpLoad8:
		return amd64MemSize(l, 1)
	case ir.OpStore8:
		return amd64MemSize(l, 0)
	case ir.OpCastUnsigned:
		switch {
		case w >= uint8(p.d):
			return mov(d, l)
		case w == 32:
			return 3
		}
		return 4
	}
	return 0
}

// longBranch is never true: every amd64 branch uses its rel32 form.
func (amd64BaselineArch) longBranch(p *bPhys, pc, target int) bool { return false }

func (amd64BaselineArch) frame(e *baselineEngine, slots int, hasCall bool) (int, error) {
	if e.options.Stack != abi.StackPlatform || slots == 0 && !hasCall {
		return 0, nil
	}
	words := slots
	if e.m.goHomes != 0 {
		words += e.m.goHomes + 1
	}
	frame := targetamd64.FrameBytes(words, hasCall)
	call := 0
	if hasCall {
		call = 8
	}
	if frame+call > e.options.StackBytesAvailable {
		return 0, ErrStackRequired
	}
	return frame, nil
}

// adjustReport leaves the report as built: amd64 names its registers
// itself, and R8 is never the workspace pointer that arm64's X8 is.
func (amd64BaselineArch) adjustReport(*baselineUnit, *abi.EmissionReport) {}

func (amd64BaselineArch) encoder(e *baselineEngine, text bool) baselineEncoder {
	s := e.emit.amd64.features(e)
	key := amd64AsmKey{e.options.Stack, e.m.goHomes, s.cpu}
	if text {
		return targetamd64.NewTextAssembler(key.stack, key.homes).WithCPU(key.cpu)
	}
	if s.out.ByteAssembler == nil || s.outKey != key {
		s.out.ByteAssembler = targetamd64.NewByteAssembler(key.stack, key.homes).WithCPU(key.cpu)
		s.outKey = key
	} else {
		s.out.Reset()
	}
	return &s.out
}

func (amd64BaselineArch) registerName(r int8) string {
	return (targetamd64.Policy{}).RegisterName(breg(r))
}

func (amd64BaselineArch) register(name string) (int8, bool) {
	switch name {
	case "RAX":
		return 0, true
	case "RCX":
		return 1, true
	case "RDX":
		return 2, true
	case "RBX":
		return 3, true
	case "RSP":
		return 4, true
	case "RBP":
		return 5, true
	case "RSI":
		return 6, true
	case "RDI":
		return 7, true
	}
	if len(name) > 3 && name[:3] == "XMM" {
		if n, err := strconv.Atoi(name[3:]); err == nil && n >= 0 && n < 16 {
			return fpBase + int8(n), true
		}
		return 0, false
	}
	if len(name) >= 2 && name[0] == 'R' {
		if n, err := strconv.Atoi(name[1:]); err == nil && n >= 8 && n < 16 {
			return int8(n), true
		}
	}
	return 0, false
}

func (amd64BaselineArch) encode(e *baselineEngine, enc baselineEncoder, ps []bPhys, pc int, labels []int32) (int, error) {
	if out, ok := enc.(*amd64ByteOut); ok {
		// The common path: concrete calls the compiler can inline.
		asm := out.ByteAssembler
		for i := range ps {
			p := &ps[i]
			if err := amd64EncodeFast(e, asm, p, pc, labels); err != nil {
				return pc, err
			}
			pc += int(p.sz)
		}
		return pc, nil
	}
	a := enc.(amd64Encoder)
	for i := range ps {
		p := &ps[i]
		if err := amd64EncodeOne(e, a, p, pc, labels); err != nil {
			return pc, err
		}
		pc += int(p.sz)
	}
	return pc, nil
}

// amd64EncodeFast encodes the most frequent records with direct calls and
// defers the rest to amd64EncodeOne. Each case mirrors amd64EncodeOp.
func amd64EncodeFast(e *baselineEngine, a *targetamd64.ByteAssembler, p *bPhys, pc int, labels []int32) error {
	switch p.k {
	case pkLabel:
		return nil
	case pkMove:
		if p.a < fpBase && p.b < fpBase {
			if p.a != p.b {
				a.Move(breg(p.a), breg(p.b))
			}
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
	case pkJump:
		a.SetPC(pc)
		a.Jump(int(labels[p.imm]))
		return nil
	case pkJumpIf:
		a.SetPC(pc)
		a.JumpIf(op.BranchCondition(p.sub), int(labels[p.imm]))
		return nil
	case pkCmp:
		a.CompareRegister(breg(p.b), breg(p.c))
		return nil
	case pkCmpImm:
		a.CompareImmediate(breg(p.b), int64(p.imm))
		return nil
	case pkOp:
		d, l, r, w := breg(p.a), breg(p.b), breg(p.c), p.w
		switch o := ir.Opcode(p.sub); o {
		case ir.OpAdd, ir.OpAnd, ir.OpOr, ir.OpXor:
			if w < 32 {
				break
			}
			if o == ir.OpAdd && d != l && d != r {
				a.BinaryNoFlags(o, w, d, l, r)
				return nil
			}
			if d == r {
				l, r = r, l
			}
			a.Binary(o, w, d, l, r)
			return nil
		case ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic:
			a.Shift(o, w, d, l, uint8(p.imm))
			return nil
		}
	case pkOpImm:
		if w := p.w; w >= 32 {
			d, l := breg(p.a), breg(p.b)
			switch o := ir.Opcode(p.sub); o {
			case ir.OpAdd, ir.OpSub:
				if d != l {
					if _, ok := amd64LeaDisp(o, w, p.imm); ok {
						a.BinaryImmediateNoFlags(o, w, d, l, p.imm)
						return nil
					}
				}
				a.BinaryImmediate(o, w, d, l, p.imm)
				return nil
			case ir.OpAnd, ir.OpOr, ir.OpXor:
				a.BinaryImmediate(o, w, d, l, p.imm)
				return nil
			}
		}
	}
	return amd64EncodeOne(e, a, p, pc, labels)
}

func amd64EncodeOne(e *baselineEngine, a amd64Encoder, p *bPhys, pc int, labels []int32) error {
	r11 := breg(amd64R11)
	switch p.k {
	case pkLabel:
	case pkMove:
		amd64Move(a, p.a, p.b)
	case pkConst:
		if p.a >= fpBase {
			a.Constant(r11, p.imm)
			a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: breg(p.a), B: r11})
		} else {
			a.Constant(breg(p.a), p.imm)
		}
	case pkSymbol:
		name := e.symbols[p.imm]
		if p.a >= fpBase {
			a.Symbol(r11, name)
			a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: breg(p.a), B: r11})
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
		return amd64EncodeOp(e, a, p)
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
		a.JumpIf(op.BranchCondition(p.sub), target)
	case pkCall:
		if ir.Opcode(p.sub) == ir.OpCallGo {
			a.CallGo()
		} else {
			a.CallC()
		}
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
		return a.Literal(machine.Instruction{Op: machine.OpLiteralLoad, A: breg(p.a), Bits: 64, Disp: uint64(e.emit.poolBase + 8*int(p.imm))})
	default:
		return fmt.Errorf("baseline amd64: physical kind %d: %w", p.k, ErrInvalidFunction)
	}
	return nil
}

// amd64Move copies engine register s to d in any banks.
func amd64Move(a amd64Encoder, d, s int8) {
	switch {
	case d == s:
	case d < fpBase && s < fpBase:
		a.Move(breg(d), breg(s))
	default:
		a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: breg(d), B: breg(s)})
	}
}

// amd64EncodeOp encodes one IR operation with its registers assigned.
// Values narrower than 64 bits stay zero-extended in general registers, and
// 32-bit floats keep bits 32-63 of their XMM register clear, so a 64-bit
// transfer to a general register zero-extends them too.
func amd64EncodeOp(e *baselineEngine, a amd64Encoder, p *bPhys) error {
	d, s0, s1, s2 := breg(p.a), breg(p.b), breg(p.c), breg(p.d)
	w := p.w
	o := ir.Opcode(p.sub)
	r11, cyc, x0 := breg(amd64R11), breg(e.m.cycle), breg(e.m.scratchFP)
	rax, rcx, rdx := breg(amd64RAX), breg(amd64RCX), breg(amd64RDX)
	switch o {
	case ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpMul:
		if p.k == pkOpImm {
			if w >= 32 && d != s0 && (o == ir.OpAdd || o == ir.OpSub) {
				if _, ok := amd64LeaDisp(o, w, p.imm); ok {
					a.BinaryImmediateNoFlags(o, w, d, s0, p.imm)
					return nil
				}
			}
			a.BinaryImmediate(o, amd64OpWidth(w), d, s0, p.imm)
			if w < 32 && o != ir.OpAnd {
				a.Narrow(d, d, w)
			}
			return nil
		}
		if o == ir.OpAdd && w >= 32 && d != s0 && d != s1 {
			a.BinaryNoFlags(o, w, d, s0, s1)
			return nil
		}
		if d == s1 && d != s0 {
			if o == ir.OpSub {
				a.Move(r11, s1)
				s1 = r11
			} else {
				s0, s1 = s1, s0
			}
		}
		if o == ir.OpMul && w == 8 {
			a.Binary(o, 64, d, s0, s1)
			a.Narrow(d, d, 8)
			return nil
		}
		a.Binary(o, w, d, s0, s1)
	case ir.OpAddScaled:
		switch scale := p.imm; scale {
		case 0:
			amd64Move(a, p.a, p.b)
			return nil
		case 1, 2, 4, 8:
			a.Scaled(d, s0, s1, scale, 0)
		default:
			a.Constant(r11, scale)
			a.Binary(op.OpMul, 64, r11, r11, s1)
			a.Scaled(d, s0, r11, 1, 0)
		}
		if w < 64 {
			a.Narrow(d, d, w)
		}
	case ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight:
		a.Shift(o, w, d, s0, uint8(p.imm))
	case ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy:
		so := amd64ShiftBy[o]
		if amd64BMIShift(e.emit.amd64.features(e), so, w) {
			a.ShiftBy(so, w, d, s0, s1)
			return nil
		}
		// The count goes to CL first, so the source must not be RCX
		// unless it is the count, and the result must not be RCX.
		src, out := s0, d
		if s0 == rcx && s1 != rcx {
			a.Move(cyc, s0)
			src = cyc
		}
		if d == rcx {
			out = cyc
		}
		a.ShiftBy(so, w, out, src, s1)
		if out != d {
			a.Move(d, out)
		}
	case ir.OpCastUnsigned:
		if w >= uint8(p.d) {
			amd64Move(a, p.a, p.b)
		} else {
			a.Narrow(d, s0, w)
		}
	case ir.OpSignExtend:
		a.SignExtend(d, s0, uint8(p.imm))
		if w < 64 {
			a.Narrow(d, d, w)
		}
	case ir.OpLeadingZeros, ir.OpTrailingZeros:
		if d == s0 && o == ir.OpLeadingZeros && !e.emit.amd64.features(e).lzcnt {
			// BSR's zero test rereads the source after writing d.
			a.Move(cyc, s0)
			s0 = cyc
		}
		a.CountZeros(o, w, d, s0)
	case ir.OpMulHigh:
		switch {
		case w < 64:
			if d == s1 && d != s0 {
				s0, s1 = s1, s0
			}
		case e.emit.amd64.features(e).bmi2:
			if s1 == rdx && s0 != rdx {
				s0, s1 = s1, s0
			}
		case s1 == rax && s0 != rax:
			s0, s1 = s1, s0
		}
		a.MulHigh(w, d, s0, s1)
	case ir.OpMulHighSigned:
		if w == 64 && s1 == rax && s0 != rax {
			s0, s1 = s1, s0
		}
		a.MulHighSigned(w, d, s0, s1)
	case ir.OpCarryAdd:
		if w == 64 {
			a.WordCarry(machine.Instruction{Op: machine.OpCarryBit, Bits: 64, Arithmetic: op.OpAdd, A: d, B: s0, C: s1, CarryMode: machine.CarryOperands})
		} else {
			a.Binary(op.OpAdd, 64, r11, s0, s1)
			a.Shift(op.OpShiftRight, 64, d, r11, w)
		}
	case ir.OpBorrowSub:
		a.WordCarry(machine.Instruction{Op: machine.OpCarryBit, Bits: 64, Arithmetic: op.OpSub, A: d, B: s0, C: s1, CarryMode: machine.CarryOperands})
	case ir.OpAddWithCarry, ir.OpSubWithBorrow:
		arith := op.OpAdd
		if o == ir.OpSubWithBorrow {
			arith = op.OpSub
		}
		if d == s1 && d != s0 {
			if arith == op.OpAdd {
				s0, s1 = s1, s0
			} else {
				a.Move(cyc, s1)
				s1 = cyc
			}
		}
		a.WordCarry(machine.Instruction{Op: machine.OpWordCarry, Bits: 64, Arithmetic: arith, A: d, B: s0, C: s1, D: s2, CarryMode: machine.CarryValue})
		if w < 64 {
			a.Narrow(d, d, w)
		}
	case ir.OpSelect:
		if d == s0 || d == s1 && d != s2 {
			a.SelectValue(cyc, s0, s1, s2)
			a.Move(d, cyc)
		} else {
			a.SelectValue(d, s0, s1, s2)
		}
	case ir.OpDiv, ir.OpDivSigned, ir.OpRem, ir.OpRemSigned:
		c := s1
		if c == rax || c == rdx {
			a.Move(r11, c)
			c = r11
		}
		a.Divide(machine.Instruction{Op: machine.OpDivide, Arithmetic: o, Bits: w, A: d, B: s0, C: c, Imm: p.imm})
	case ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv:
		if d == s1 && d != s0 {
			if o == ir.OpFAdd || o == ir.OpFMul {
				s0, s1 = s1, s0
			} else {
				a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: x0, B: s1})
				s1 = x0
			}
		}
		a.FloatBinary(o, w, d, s0, s1)
	case ir.OpFMA:
		a.FloatFMA(w, d, s0, s1, s2)
	case ir.OpFSqrt, ir.OpFConvert:
		a.FloatUnary(o, w, d, s0)
		if w == 32 && (o == ir.OpFConvert || d != s0) {
			// The scalar form kept d's old bits 32-63.
			a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 32, A: r11, B: d})
			a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: d, B: r11})
		}
	case ir.OpFFromSigned, ir.OpFFromUnsigned:
		a.FloatConvert(machine.Instruction{Op: machine.OpIntToFloat, Arithmetic: o, Bits: w, Imm: uint64(p.d), A: d, B: s0})
	case ir.OpFToSigned, ir.OpFToUnsigned:
		a.FloatConvert(machine.Instruction{Op: machine.OpFloatToInt, Arithmetic: o, Bits: w, Disp: uint64(p.d), Imm: p.imm, A: d, B: s0})
	case ir.OpFCompare:
		a.FloatCompare(op.FloatCondition(p.imm), uint8(p.d), d, s0, s1)
	case ir.OpFProductError:
		// x*y - p as FMA(x, y, -p).
		a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: r11, B: s2})
		a.Constant(cyc, 1<<63)
		a.Binary(op.OpXor, 64, r11, r11, cyc)
		a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: x0, B: r11})
		a.FloatFMA(64, d, s0, s1, x0)
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
			a.Move(r11, s0)
			a.Load8(d, r11)
			for i := uint8(1); i < n/8; i++ {
				a.BinaryImmediate(op.OpAdd, 64, r11, r11, 1)
				a.Load8(cyc, r11)
				a.Shift(op.OpShiftLeft, 64, d, d, 8)
				a.Binary(op.OpOr, 64, d, d, cyc)
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
			a.Move(r11, s0)
			for i := uint8(0); i < n/8; i++ {
				if i != 0 {
					a.BinaryImmediate(op.OpAdd, 64, r11, r11, 1)
				}
				if shift := n - 8 - 8*i; shift != 0 {
					a.Shift(op.OpShiftRight, 64, cyc, s1, shift)
					a.Store8(r11, cyc)
				} else {
					a.Store8(r11, s1)
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
		return fmt.Errorf("baseline amd64: %v: %w", o, ErrUnsupported)
	}
	return nil
}

// literalsLong: RIP-relative loads reach the pool from any baseline code.
func (amd64BaselineArch) literalsLong(int) bool { return false }

func (amd64BaselineArch) encodePool(e *baselineEngine, enc baselineEncoder, pc, base int, pool []uint64) error {
	return enc.(amd64Encoder).Literal(machine.Instruction{Op: machine.OpLiteralData, Data: baselinePoolBytes(e, pc, base, pool)})
}

// amd64FloatBranch maps a floating-point condition to the integer branch
// condition that holds after FloatCompare's UCOMISx, for the conditions
// whose SETcc reads the compare's flags with no fixup after it (Equal and
// NotEqual adjust with ADC or SBB, Ordered and Unordered test parity).
var amd64FloatBranch = [...]struct {
	c  op.BranchCondition
	ok bool
}{
	op.FloatLess:                  {op.BranchAbove, true}, // operands swapped
	op.FloatUnorderedGreaterEqual: {op.BranchBelowEqual, true},
	op.FloatLessEqual:             {op.BranchAboveEqual, true},
	op.FloatUnorderedGreater:      {op.BranchBelow, true},
	op.FloatGreater:               {op.BranchAbove, true},
	op.FloatUnorderedLessEqual:    {op.BranchBelowEqual, true},
	op.FloatGreaterEqual:          {op.BranchAboveEqual, true},
	op.FloatUnorderedLess:         {op.BranchBelow, true},
	op.FloatOrderedNotEqual:       {op.BranchNotEqual, true},
	op.FloatUnorderedEqual:        {op.BranchEqual, true},
}

// floatBranch: moves, spills, reloads and literal loads leave the flags;
// constants may be XOR-cleared.
func (amd64BaselineArch) floatBranch(e *baselineEngine, r *bRec, between []bPhys) (op.BranchCondition, bool) {
	if r.imm >= uint64(len(amd64FloatBranch)) || !amd64FloatBranch[r.imm].ok {
		return 0, false
	}
	for i := range between {
		switch between[i].k {
		case pkMove, pkSpill, pkReload, pkLiteral:
		default:
			return 0, false
		}
	}
	return amd64FloatBranch[r.imm].c, true
}
