package compile

import (
	"fmt"
	"math/bits"
	"slices"
	"strconv"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	target386 "github.com/moriyoshi/nyanjit/backend/target/i386"
	"github.com/moriyoshi/nyanjit/ir"
)

// i386BaselineArch is the baseline engine's 386 part. It encodes through
// target386's ByteAssembler and TextAssembler methods directly.
//
// Register model. General registers hold at most 32 bits, so the model is
// wide: every 64-bit value, integer or float, lives in an XMM register (or an
// 8-byte slot) and never in a general register. Narrow values live in either
// bank, zero-extended; a move between the banks is MOVD, which is lossless for
// them and truncates a 64-bit value to its low word where an operation reads
// only that word. Operations on 64-bit integers that SSE2 has (add, sub, and,
// or, xor, moves, spills) stay in XMM; the rest move the value to EDX:EAX, use
// the pair forms of the assembler and move the result back.
//
// EAX, ECX and EDX are never allocated: they are the expansions' temporaries,
// the fixed registers of MUL, DIV and the shift-by-CL expansion, the result
// pair and the call target. EBX, ESI, EDI and, with a platform stack, EBP are
// allocated; without one EBP points at the workspace. XMM0, XMM1 and XMM2 are
// the engine's transit, cycle and floating-point scratch registers, and also
// the temporaries of the assembler's SSE2 expansions, which never run while
// the engine holds a value in them; XMM3-XMM7 are allocated. EBX is borrowed
// (saved and restored around the use) by the shift-by-register expansion and,
// with ESI and EDI, by the 64-bit division.
//
// Engine slots are 8 bytes, two assembler words. Slots from i386Incoming name
// entry argument words: a 32- or 64-bit parameter stays in the caller's
// argument area, read in place; an 8- or 16-bit one is zero-extended there at
// entry. Calls store their arguments to an outgoing area at the bottom of the
// frame, as the optimized tier does.
type i386BaselineArch struct{}

const (
	x86EAX int8 = 0
	x86ECX int8 = 1
	x86EDX int8 = 2
	x86EBX int8 = 3
	x86ESP int8 = 4
	x86EBP int8 = 5
	x86ESI int8 = 6
	x86EDI int8 = 7
	x86X0  int8 = fpBase + 0
	x86X1  int8 = fpBase + 1
	x86X2  int8 = fpBase + 2

	// i386Incoming is the first engine slot that names an entry argument
	// word; slot i386Incoming+k is the k-th argument word.
	i386Incoming = 16384
)

// Private pkOp operations of the 386 records.
const (
	i386ParamNarrow uint8 = 230 + iota // zero-extend the w-bit argument at entry offset imm in place
	i386ArgStore                       // store a (w bits) to the call area at offset imm
	i386GoResult                       // load EAX:EDX from the call area at offset imm
	i386PairToX                        // a <- EDX:EAX
	i386XToPair                        // EDX:EAX <- b
	i386Filler                         // nothing: the bytes of the long record before it
)

// i386SaveMark marks the engine's prologue and epilogue saves of callee-saved
// registers, which the 386 frame's pushes already do.
const i386SaveMark uint8 = 1

var (
	i386PinGP = []int8{x86ESI}
	i386PinFP = []int8{fpBase + 7, fpBase + 6}
	// i386Temps are the general registers every expansion may write.
	i386Temps = regBit(x86EAX) | regBit(x86ECX) | regBit(x86EDX)
)

func (i386BaselineArch) model(o compileOptions) baselineModel {
	m := baselineModel{
		calleeSavedFP: i386PinFP,
		results:       [2]int8{x86EAX, x86EDX},
		transit:       x86X0, cycle: x86X1, scratchFP: x86X2,
		callTarget: x86EAX,
		spillLimit: maxSpillSlots,
		wide:       true,
	}
	m.allocatable = regBit(x86EBX) | regBit(x86ESI) | regBit(x86EDI)
	for r := int8(3); r < 8; r++ {
		m.allocatable |= regBit(fpBase + r)
	}
	if o.Stack == abi.StackPlatform {
		// The frame pushes EBX, ESI, EDI and EBP. One Var may be pinned
		// to ESI: three general registers stay for the operands.
		m.allocatable |= regBit(x86EBP)
		m.calleeSavedGP = i386PinGP
		m.saved = regBit(x86EBX) | regBit(x86ESI) | regBit(x86EDI) | regBit(x86EBP)
	}
	return m
}

func (i386BaselineArch) foldable(r *bRec, k int, imm uint64) bool {
	if k != 1 || r.bits > 32 {
		return false
	}
	switch r.op {
	case ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor:
		return true
	}
	return false
}

// i386Words is the number of stack words value v takes as an argument.
func i386Words(e *baselineEngine, v uint32) int {
	if e.valueBits(v) == 64 {
		return 2
	}
	return 1
}

func (x i386BaselineArch) lower(e *baselineEngine, r *bRec, dst int8, src [3]int8) error {
	a := &e.alloc
	a.used |= i386Temps | regBit(x86X0) | regBit(x86X1) | regBit(x86X2)
	p := bPhys{k: pkOp, sub: uint8(r.op), w: r.bits, a: dst, b: src[0], c: src[1], d: src[2], imm: r.imm}
	var pool [4]int32
	n := 0
	scratch := func() uint64 {
		s := e.poolSlot()
		pool[n] = s
		n++
		return uint64(s)
	}
	pair := int32(-1)
	switch r.op {
	case ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor:
		if src[1] < 0 {
			p.k, p.imm = pkOpImm, e.recs[r.b].imm
		}
	case ir.OpCastUnsigned, ir.OpFFromSigned, ir.OpFFromUnsigned, ir.OpFToSigned, ir.OpFToUnsigned, ir.OpFCompare:
		p.d = int8(e.valueBits(r.a))
		if (r.op == ir.OpFFromSigned || r.op == ir.OpFFromUnsigned) && p.d == 64 {
			p.imm = scratch()
		}
	case ir.OpLoad16, ir.OpLoad32, ir.OpStore16, ir.OpStore32:
		p.imm = uint64(r.ord)
	case ir.OpFMA:
		return ErrBackendLimit
	case ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy:
		p.imm = scratch()
	case ir.OpMul:
		if r.bits == 64 {
			p.imm = scratch() | scratch()<<16
		}
	case ir.OpMulHigh, ir.OpMulHighSigned:
		if r.bits == 64 {
			p.imm = scratch() | scratch()<<16 | scratch()<<32 | scratch()<<48
		}
	case ir.OpCarryAdd, ir.OpBorrowSub, ir.OpAddWithCarry, ir.OpSubWithBorrow:
		if r.bits == 64 {
			p.imm = scratch()
		}
	case ir.OpFProductError:
		p.imm = scratch() | scratch()<<16
	case ir.OpDiv, ir.OpDivSigned, ir.OpRem, ir.OpRemSigned:
		if r.bits == 64 {
			pair = i386PoolPair(e)
			p.imm = r.imm | uint64(pair)<<8 | scratch()<<24 | scratch()<<40
		}
	case ir.OpAddScaled:
		if r.bits == 64 {
			// B×Imm in XMM1, then A + XMM1.
			e.put(bPhys{k: pkConst, a: x86X1, imm: r.imm})
			sa, sb := scratch(), scratch()
			e.put(bPhys{k: pkOp, sub: uint8(ir.OpMul), w: 64, a: x86X1, b: src[1], c: x86X1, imm: sa | sb<<16})
			p = bPhys{k: pkOp, sub: uint8(ir.OpAdd), w: 64, a: dst, b: src[0], c: x86X1}
		}
	}
	// A record's size is a byte: a longer expansion is followed by fillers
	// that count its remaining bytes and encode nothing.
	size := x.size(e, &p)
	p.sz = uint8(min(size, 255))
	e.put(p)
	for size -= 255; size > 0; size -= 255 {
		e.put(bPhys{k: pkOp, sub: i386Filler, sz: uint8(min(size, 255))})
	}
	for k := 0; k < n; k++ {
		e.freePoolSlot(pool[k])
	}
	if pair >= 0 {
		e.freePoolSlot(pair + 1)
		e.freePoolSlot(pair)
	}
	return nil
}

// i386PoolPair returns two adjacent free pool slots, the first of them.
func i386PoolPair(e *baselineEngine) int32 {
	a := &e.alloc
	for i, s := range a.poolFree {
		for j, t := range a.poolFree {
			if t == s+1 {
				lo, hi := min(i, j), max(i, j)
				a.poolFree = slices.Delete(a.poolFree, hi, hi+1)
				a.poolFree = slices.Delete(a.poolFree, lo, lo+1)
				return s
			}
		}
	}
	s := e.newSlot()
	e.newSlot()
	a.pool = append(a.pool, s, s+1)
	return s
}

// enterParams leaves each live parameter in its argument words.
func (i386BaselineArch) enterParams(e *baselineEngine) {
	a := &e.alloc
	word := 0
	for _, v := range e.params {
		w := word
		word += i386Words(e, v)
		if a.vstamp[v] != a.stamp || a.vs[v].next == noValue {
			continue
		}
		s := &a.vs[v]
		s.slot, s.fl = int32(i386Incoming+w), s.fl|vfStored
		if a.xid[v] >= 0 {
			s.fl |= vfHome
		}
		if b := e.recs[v].bits; b < 32 {
			// Callers need not extend narrow arguments.
			a.used |= regBit(x86EAX)
			e.put(bPhys{k: pkOp, sub: i386ParamNarrow, w: b, imm: uint64(4 + 4*w)})
		}
	}
}

// returnValue moves the result to EDX:EAX; a narrow result has EDX = 0.
func (i386BaselineArch) returnValue(e *baselineEngine, blk *bBlock) error {
	if blk.parts {
		return ErrBackendLimit
	}
	a := &e.alloc
	v := blk.result
	a.used |= regBit(x86EAX) | regBit(x86EDX)
	if e.valueBits(v) != 64 {
		e.load(x86EAX, v)
		e.put(bPhys{k: pkConst, a: x86EDX})
		return nil
	}
	s := &a.vs[v]
	switch {
	case s.reg >= 0 && regBank(s.reg) == bankFP:
		e.put(bPhys{k: pkOp, sub: i386XToPair, b: s.reg})
	case e.isConst(v) && e.recs[v].fl&rfSymbol == 0:
		e.put(bPhys{k: pkConst, a: x86EAX, imm: uint64(uint32(e.recs[v].imm))})
		e.put(bPhys{k: pkConst, a: x86EDX, imm: e.recs[v].imm >> 32})
	default:
		e.load(x86X0, v)
		e.put(bPhys{k: pkOp, sub: i386XToPair, b: x86X0})
	}
	return nil
}

// call lowers a C or Go call: arguments go to the call area, the callee to
// EAX, and the result comes in EAX, EDX:EAX or, for Go, the call area.
func (i386BaselineArch) call(e *baselineEngine, i uint32, r *bRec) error {
	a := &e.alloc
	if e.options.Stack != abi.StackPlatform {
		return ErrStackRequired
	}
	if r.op == ir.OpSyscall {
		return baselineUnsupported("386 system call")
	}
	effect, err := e.callEffect(r)
	if err != nil {
		return err
	}
	clobber := uint64(0)
	for _, list := range [2][]string{effect.ClobberedRegisters, effect.ClobberedFPRegisters} {
		for _, name := range list {
			if reg, ok := e.arch.register(name); ok {
				clobber |= regBit(reg)
			}
		}
	}
	a.callers |= clobber
	args := e.args[r.x : r.x+uint32(r.n)]
	if r.op == ir.OpCallGo {
		var widths [16]uint8
		n := 0
		for _, v := range args[1:] {
			if n < len(widths) {
				widths[n] = e.valueBits(v)
				n++
			}
		}
		if err := validate386GoSubwords(widths[:n]); err != nil {
			return err
		}
	}
	liveAfter := func(v uint32) bool {
		for j, x := range args {
			if x == v {
				return a.argNu[int(r.x)+j] != noValue
			}
		}
		return a.vs[v].next != noValue
	}
	// Values live after the call leave the clobbered registers.
	for m := ^a.free & e.m.allocatable & clobber; m != 0; m &= m - 1 {
		reg := int8(bits.TrailingZeros64(m))
		v := a.regVal[reg]
		if v == noValue || !liveAfter(v) {
			continue
		}
		if t := a.free & e.m.allocatable &^ clobber &^ a.pinned & bankMask(regBank(reg)); t != 0 {
			to := int8(bits.TrailingZeros64(t))
			e.put(bPhys{k: pkMove, a: to, b: reg})
			a.regVal[reg] = noValue
			a.free |= regBit(reg)
			e.bind(v, to)
			continue
		}
		e.evict(reg)
	}
	off := uint64(0)
	for _, v := range args[1:] {
		wide := e.valueBits(v) == 64
		s := &a.vs[v]
		w := uint8(32)
		if wide {
			w = 64
		}
		switch {
		case s.reg >= 0:
			e.put(bPhys{k: pkOp, sub: i386ArgStore, w: w, a: s.reg, imm: off})
		case e.isConst(v) && e.recs[v].fl&rfSymbol == 0:
			c := e.recs[v].imm
			e.put(bPhys{k: pkConst, a: x86EAX, imm: uint64(uint32(c))})
			e.put(bPhys{k: pkOp, sub: i386ArgStore, w: 32, a: x86EAX, imm: off})
			if wide {
				e.put(bPhys{k: pkConst, a: x86EAX, imm: c >> 32})
				e.put(bPhys{k: pkOp, sub: i386ArgStore, w: 32, a: x86EAX, imm: off + 4})
			}
		default:
			t := x86EAX
			if wide {
				t = x86X0
			}
			e.load(t, v)
			e.put(bPhys{k: pkOp, sub: i386ArgStore, w: w, a: t, imm: off})
		}
		off += 4 * uint64(i386Words(e, v))
	}
	e.load(x86EAX, args[0])
	e.put(bPhys{k: pkCall})
	a.hasCall, a.flags = true, true
	a.used |= clobber | i386Temps | regBit(x86X0) | regBit(x86X2)
	for m := clobber & e.m.allocatable; m != 0; m &= m - 1 {
		reg := int8(bits.TrailingZeros64(m))
		if a.regVal[reg] != noValue {
			e.unbind(reg)
		}
	}
	for j, v := range args {
		dup := false
		for q := 0; q < j; q++ {
			dup = dup || args[q] == v
		}
		if !dup {
			e.advance(v, a.argNu[int(r.x)+j])
		}
	}
	if a.vs[i].next == noValue {
		return nil
	}
	if r.op == ir.OpCallGo {
		e.put(bPhys{k: pkOp, sub: i386GoResult, imm: off})
	}
	if e.recs[i].bits == 64 {
		// Every XMM register is free after the call: no eviction
		// overwrites EDX:EAX.
		dst := e.def(i, bankFP, 0)
		e.put(bPhys{k: pkOp, sub: i386PairToX, a: dst})
	} else {
		dst := e.def(i, bankGP, regBit(x86EAX)|regBit(x86EDX))
		e.put(bPhys{k: pkMove, a: dst, b: x86EAX})
	}
	e.defined(i)
	return nil
}

// i386CallArea returns the bytes of the largest call's argument and result
// area.
func i386CallArea(e *baselineEngine) int {
	area := 0
	for k := range e.recs {
		r := &e.recs[k]
		if r.op != ir.OpCallC && r.op != ir.OpCallGo {
			continue
		}
		n := 0
		for _, v := range e.args[r.x+1 : r.x+uint32(r.n)] {
			n += 4 * i386Words(e, v)
		}
		if r.op == ir.OpCallGo {
			n += 8
		}
		area = max(area, n)
	}
	return area
}

// i386GoResultOffset returns the entry-SP offset of a Go entry's result.
func i386GoResultOffset(e *baselineEngine) int {
	off := 4
	for _, v := range e.params {
		off += 4 * i386Words(e, v)
	}
	return off
}

func (i386BaselineArch) longBranch(*bPhys, int, int) bool { return false }

func (i386BaselineArch) frame(e *baselineEngine, slots int, hasCall bool) (int, error) {
	if e.options.Stack != abi.StackPlatform {
		return 0, nil
	}
	// The pushes save the callee-saved registers, not the engine's slots.
	saved := bits.OnesCount64(e.alloc.used & e.m.saved)
	slots -= saved
	if slots == 0 && !hasCall && saved == 0 {
		return 0, nil
	}
	frame := target386.FrameBytesForCallArea(2*slots, hasCall, i386CallArea(e))
	if frame > e.options.StackBytesAvailable {
		return 0, ErrStackRequired
	}
	return frame, nil
}

// i386EmitState holds the reusable 386 encoders.
type i386EmitState struct {
	enc   *i386Enc
	sizer *i386Enc
}

func i386State(e *baselineEngine) *i386EmitState {
	if e.emit.i386 == nil {
		e.emit.i386 = &i386EmitState{}
	}
	return e.emit.i386
}

// newI386Enc returns a byte encoder for stack practice stack.
func newI386Enc(e *baselineEngine) *i386Enc {
	b := target386.NewByteAssembler(e.options.Stack)
	return &i386Enc{a: b, b: b, stack: e.options.Stack, flavor: e.options.ABI}
}

func (i386BaselineArch) encoder(e *baselineEngine, text bool) baselineEncoder {
	if text {
		t := target386.NewTextAssembler(e.options.Stack)
		return &i386Enc{a: t, t: t, stack: e.options.Stack, flavor: e.options.ABI}
	}
	s := i386State(e)
	if s.enc == nil || s.enc.stack != e.options.Stack {
		s.enc = newI386Enc(e)
	}
	s.enc.reset(e.options.ABI)
	return s.enc
}

// measurer returns the reset measuring encoder.
func (s *i386EmitState) measurer(e *baselineEngine) *i386Enc {
	if s.sizer == nil || s.sizer.stack != e.options.Stack {
		s.sizer = newI386Enc(e)
	}
	s.sizer.reset(e.options.ABI)
	return s.sizer
}

func (i386BaselineArch) size(e *baselineEngine, p *bPhys) int {
	platform := e.options.Stack == abi.StackPlatform
	switch p.k {
	case pkLabel:
		return 0
	case pkMove:
		switch {
		case p.a == p.b:
			return 0
		case p.a < fpBase && p.b < fpBase:
			return 2
		}
		return 4
	case pkConst:
		if p.a < fpBase {
			return 5
		}
		switch {
		case p.imm == 0:
			return 4
		case p.imm>>32 == 0:
			return 9
		}
		return 22
	case pkSpill, pkReload:
		if p.a < fpBase && e.m.saved&regBit(p.a) != 0 && p.imm < i386Incoming && int64(p.imm) >= int64(e.alloc.nslots) {
			p.sub = i386SaveMark
			return 0
		}
		if p.imm >= i386Incoming {
			break
		}
		n := 6
		if platform {
			n = 7
		}
		if p.a >= fpBase {
			n += 2
		}
		return n
	case pkJump:
		return 5
	case pkJumpIf, pkJumpIfLong:
		return 6
	case pkCall:
		return 2
	case pkFrameEnter:
		p.w = uint8(i386CallArea(e) / 4)
		return 10
	case pkFrameLeave:
		return 10
	case pkRet:
		if e.options.ABI != abi.Go {
			return 1
		}
		p.imm = uint64(i386GoResultOffset(e))
	}
	if p.k == pkOp || p.k == pkOpImm {
		if n := i386FastOpSize(p); n != 0 {
			return n
		}
	}
	m := i386State(e).measurer(e)
	m.measure = true
	if err := m.one(e, p, 0, nil); err != nil || m.a.Err() != nil {
		e.emit.sizeErr = true
		return 1
	}
	return m.a.AssembledSize()
}

func (i386BaselineArch) registerName(r int8) string {
	if r >= fpBase {
		return "XMM" + strconv.Itoa(int(r-fpBase))
	}
	return [...]string{"EAX", "ECX", "EDX", "EBX", "ESP", "EBP", "ESI", "EDI"}[r&7]
}

func (i386BaselineArch) register(name string) (int8, bool) {
	for r, n := range [...]string{"EAX", "ECX", "EDX", "EBX", "ESP", "EBP", "ESI", "EDI"} {
		if n == name {
			return int8(r), true
		}
	}
	if len(name) == 4 && name[:3] == "XMM" && name[3] >= '0' && name[3] <= '7' {
		return fpBase + int8(name[3]-'0'), true
	}
	return 0, false
}

func (i386BaselineArch) encode(e *baselineEngine, enc baselineEncoder, ps []bPhys, pc int, labels []int32) (int, error) {
	x := enc.(*i386Enc)
	for i := range ps {
		p := &ps[i]
		if err := x.one(e, p, pc, labels); err != nil {
			return pc, err
		}
		pc += int(p.sz)
	}
	return pc, nil
}

// i386FastOpSize sizes the common general-register operations as op
// encodes them, or returns 0.
func i386FastOpSize(p *bPhys) int {
	w, o := p.w, ir.Opcode(p.sub)
	if w > 32 || p.sub >= i386ParamNarrow {
		return 0
	}
	mov := func(d, s int8) int {
		if d == s {
			return 0
		}
		return 2
	}
	mask := 0
	if w < 32 {
		mask = 6
	}
	switch o {
	case ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpMul:
		if p.k == pkOpImm {
			n := mov(p.a, p.b) + 6
			if w < 32 && (o == ir.OpAdd || o == ir.OpSub) {
				n += mask
			}
			return n
		}
		code := 2
		if o == ir.OpMul {
			code = 3
		}
		switch {
		case p.a != p.c || p.a == p.b:
			return mov(p.a, p.b) + code + mask
		case commutative(o):
			return code + mask
		}
		return mov(x86EAX, p.b) + code + mask + 2
	case ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic:
		n := mov(p.a, p.b) + 3
		if w < 32 {
			n += 6
		}
		return n
	}
	return 0
}

// i386Asm is the method set the baseline uses from both 386 assemblers.
type i386Asm interface {
	SetPC(int)
	AssembledSize() int
	Err() error
	Constant(backend.Register, uint64)
	Symbol(backend.Register, string)
	SymbolInstruction(machine.Instruction)
	Move(backend.Register, backend.Register)
	Binary(op.Opcode, uint8, backend.Register, backend.Register, backend.Register)
	BinaryImmediate(op.Opcode, uint8, backend.Register, backend.Register, uint64)
	BinaryCarry(op.Opcode, backend.Register, backend.Register, backend.Register)
	MulWide(backend.Register)
	Narrow(backend.Register, backend.Register, uint8)
	SignExtend(backend.Register, backend.Register, uint8)
	Shift(op.Opcode, uint8, backend.Register, backend.Register, uint8)
	Scaled(backend.Register, backend.Register, backend.Register, uint64, uint64)
	SelectValue(backend.Register, backend.Register, backend.Register, backend.Register)
	Load8(backend.Register, backend.Register)
	Store8(backend.Register, backend.Register)
	LoadOffset(backend.Register, backend.Register, uint64)
	StoreOffset(backend.Register, backend.Register, uint64)
	NarrowMemory(machine.Instruction) error
	CallC()
	Spill(backend.Register, int)
	Reload(backend.Register, int)
	IncomingWord(backend.Register, int)
	FrameEnterArea(int, int)
	FrameLeave(int)
	Ret()
	CompareImmediate(backend.Register, int64)
	CompareRegister(backend.Register, backend.Register)
	Jump(int)
	JumpIf(op.BranchCondition, int)
	MulHigh(uint8, backend.Register, backend.Register, backend.Register)
	MulHighSigned(uint8, backend.Register, backend.Register, backend.Register)
	Divide(machine.Instruction)
	ShiftInstruction(machine.Instruction)
	ShiftByInstruction(machine.Instruction)
	Float(machine.Instruction)
	FloatUnary(op.Opcode, uint8, backend.Register, backend.Register)
	FloatBinary(op.Opcode, uint8, backend.Register, backend.Register, backend.Register)
	FloatMemory(machine.Instruction) error
	FloatConvert(machine.Instruction)
	FloatCompare(op.FloatCondition, uint8, backend.Register, backend.Register, backend.Register)
	Vector(machine.Instruction) error
}

var (
	_ i386Asm = (*target386.ByteAssembler)(nil)
	_ i386Asm = (*target386.TextAssembler)(nil)
)

// i386Enc is a 386 encoding pass: the assembler and what the records before
// the current one established.
type i386Enc struct {
	a      i386Asm
	b      *target386.ByteAssembler
	t      *target386.TextAssembler
	stack  abi.StackPractice
	flavor abi.Flavor
	// frame and callArea are the frame of the pkFrameEnter encoded.
	frame, callArea int
	// remap is set by a 64-bit comparison whose conditions the next
	// conditional branch maps.
	remap bool
	// measure: labels are being measured, not targeted.
	measure bool
	// labels of the current expansion, as offsets from its start.
	labels [16]int
	nlabel int
	base   int // the expansion's PC
	start  int // AssembledSize at the expansion's start
	inner  *i386Enc
}

func (x *i386Enc) reset(flavor abi.Flavor) {
	x.b.Reset()
	x.flavor = flavor
	x.frame, x.callArea, x.remap, x.measure = 0, 0, false, false
}

func (x *i386Enc) AssembledSize() int { return x.a.AssembledSize() }
func (x *i386Enc) Err() error         { return x.a.Err() }

// Bytes returns a copy: the assembler's buffer is reused.
func (x *i386Enc) Bytes() []byte                     { return slices.Clone(x.b.Bytes()) }
func (x *i386Enc) Relocations() []machine.Relocation { return x.b.Relocations() }
func (x *i386Enc) Text() string                      { return x.t.Text() }

func rx(r int8) backend.Register {
	if r >= fpBase {
		return backend.VectorRegister(uint8(r - fpBase))
	}
	return backend.Register(r)
}

const (
	gEAX = backend.Register(0)
	gECX = backend.Register(1)
	gEDX = backend.Register(2)
	gEBX = backend.Register(3)
	gESP = backend.Register(4)
	gESI = backend.Register(6)
	gEDI = backend.Register(7)
)

var (
	vX0 = backend.VectorRegister(0)
	vX1 = backend.VectorRegister(1)
	vX2 = backend.VectorRegister(2)
)

func isXMM(r int8) bool { return r >= fpBase }

// wslot is the first assembler word of engine slot s.
func wslot(s uint64) int { return 2 * int(s) }

func (x *i386Enc) fmove(w uint8, dst, src backend.Register) {
	x.a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: w, A: dst, B: src})
}

// toPair moves XMM register r's 64 bits to EDX:EAX.
func (x *i386Enc) toPair(r backend.Register) {
	x.a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: gEAX, HighA: gEDX, B: r})
}

// fromPair moves EDX:EAX to XMM register r.
func (x *i386Enc) fromPair(r backend.Register) {
	x.a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: r, B: gEAX, HighB: gEDX})
}

// gp returns a general register holding r's low word: r, or t loaded from
// XMM register r.
func (x *i386Enc) gp(r int8, t backend.Register) backend.Register {
	if isXMM(r) {
		x.fmove(32, t, rx(r))
		return t
	}
	return rx(r)
}

func (x *i386Enc) spillX(r backend.Register, s uint64) error {
	return x.a.FloatMemory(machine.Instruction{Op: machine.OpFloatSpill, Bits: 64, A: r, Slot: wslot(s)})
}

func (x *i386Enc) reloadX(r backend.Register, s uint64) error {
	return x.a.FloatMemory(machine.Instruction{Op: machine.OpFloatReload, Bits: 64, A: r, Slot: wslot(s)})
}

// Local labels of an expansion: the measuring pass records their offsets,
// the encoding pass targets them.
func (x *i386Enc) label() int {
	n := x.nlabel
	x.nlabel++
	return n
}

func (x *i386Enc) bind(n int) {
	if x.measure {
		x.labels[n] = x.a.AssembledSize() - x.start
	}
}

func (x *i386Enc) here() int { return x.base + x.a.AssembledSize() - x.start }

func (x *i386Enc) jumpIf(c op.BranchCondition, n int) {
	x.a.SetPC(x.here())
	x.a.JumpIf(c, x.base+x.labels[n])
}

func (x *i386Enc) jump(n int) {
	x.a.SetPC(x.here())
	x.a.Jump(x.base + x.labels[n])
}

// expand runs f, an expansion with local labels, at pc: on a measuring
// encoder directly, otherwise after measuring it on an inner encoder.
func (x *i386Enc) expand(pc int, f func(x *i386Enc) error) error {
	if x.measure {
		x.base, x.start, x.nlabel = pc, x.a.AssembledSize(), 0
		return f(x)
	}
	if x.inner == nil || x.inner.stack != x.stack {
		b := target386.NewByteAssembler(x.stack)
		x.inner = &i386Enc{a: b, b: b, stack: x.stack}
	}
	m := x.inner
	m.reset(x.flavor)
	m.frame, m.callArea, m.measure = x.frame, x.callArea, true
	m.base, m.start, m.nlabel = 0, 0, 0
	if err := f(m); err != nil {
		return err
	}
	x.labels = m.labels
	x.base, x.start, x.nlabel = pc, x.a.AssembledSize(), 0
	return f(x)
}

// incomingOffset returns the entry-SP offset of incoming slot s.
func incomingOffset(s uint64) int { return 4 + 4*int(s-i386Incoming) }

func (x *i386Enc) one(e *baselineEngine, p *bPhys, pc int, labels []int32) error {
	a := x.a
	switch p.k {
	case pkLabel:
	case pkMove:
		switch {
		case p.a == p.b:
		case !isXMM(p.a) && !isXMM(p.b):
			a.Move(rx(p.a), rx(p.b))
		case isXMM(p.a) && isXMM(p.b):
			x.fmove(64, rx(p.a), rx(p.b))
		default:
			x.fmove(32, rx(p.a), rx(p.b))
		}
	case pkConst:
		x.constant(p.a, p.imm)
	case pkSymbol:
		name := e.symbols[p.imm]
		if !isXMM(p.a) {
			a.Symbol(rx(p.a), name)
			return nil
		}
		a.SymbolInstruction(machine.Instruction{Op: machine.OpSymbol, Bits: 64, A: gEAX, HighA: gEDX, Symbol: name})
		x.fromPair(rx(p.a))
	case pkSpill:
		if p.sub == i386SaveMark {
			return nil
		}
		if p.imm >= i386Incoming {
			return fmt.Errorf("baseline 386: store to an argument word: %w", ErrInvalidFunction)
		}
		if isXMM(p.a) {
			return x.spillX(rx(p.a), p.imm)
		}
		a.Spill(rx(p.a), wslot(p.imm))
	case pkReload:
		if p.sub == i386SaveMark {
			return nil
		}
		if p.imm >= i386Incoming {
			x.incoming(p.a, incomingOffset(p.imm))
			return nil
		}
		if isXMM(p.a) {
			return x.reloadX(rx(p.a), p.imm)
		}
		a.Reload(rx(p.a), wslot(p.imm))
	case pkOp, pkOpImm:
		return x.op(e, p, pc)
	case pkCmp, pkCmpImm:
		x.compare(p)
	case pkJump:
		a.SetPC(pc)
		target := pc
		if labels != nil {
			target = int(labels[p.imm])
		}
		a.Jump(target)
	case pkJumpIf, pkJumpIfLong:
		a.SetPC(pc)
		target := pc
		if labels != nil {
			target = int(labels[p.imm])
		}
		c := op.BranchCondition(p.sub)
		if x.remap {
			// After SBB on (right, left), LE, G, BE and A are GE, L, AE and B.
			switch c {
			case op.BranchLessEqual:
				c = op.BranchGreaterEqual
			case op.BranchGreater:
				c = op.BranchLess
			case op.BranchBelowEqual:
				c = op.BranchAboveEqual
			case op.BranchAbove:
				c = op.BranchBelow
			}
			x.remap = false
		}
		a.JumpIf(c, target)
	case pkCall:
		a.CallC()
	case pkFrameEnter:
		x.frame, x.callArea = int(p.imm), 4*int(p.w)
		a.FrameEnterArea(x.frame, x.callArea)
	case pkFrameLeave:
		a.FrameLeave(int(p.imm))
	case pkRet:
		if x.flavor == abi.Go {
			a.StoreOffset(gESP, gEAX, p.imm)
			a.StoreOffset(gESP, gEDX, p.imm+4)
		}
		a.Ret()
	default:
		return fmt.Errorf("baseline 386: physical kind %d: %w", p.k, ErrInvalidFunction)
	}
	return nil
}

// constant materializes imm in r: the low word for a general register, all
// 64 bits for an XMM register (through EAX, EDX and XMM2).
func (x *i386Enc) constant(r int8, imm uint64) {
	a := x.a
	if !isXMM(r) {
		a.Constant(rx(r), uint64(uint32(imm)))
		return
	}
	switch {
	case imm == 0:
		_ = a.Vector(machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVZero, A: rx(r)})
	case imm>>32 == 0:
		a.Constant(gEAX, imm)
		x.fmove(32, rx(r), gEAX)
	default:
		a.Constant(gEAX, uint64(uint32(imm)))
		a.Constant(gEDX, imm>>32)
		x.fromPair(rx(r))
	}
}

// incoming loads the argument word at entry-SP offset off to r; an XMM
// register gets two words.
func (x *i386Enc) incoming(r int8, off int) {
	a := x.a
	if !isXMM(r) {
		a.IncomingWord(rx(r), off)
		return
	}
	if x.stack == abi.StackPlatform {
		_ = a.FloatMemory(machine.Instruction{Op: machine.OpFloatReload, Bits: 64, A: rx(r), Slot: (x.frame + off - x.callArea) / 4})
		return
	}
	a.IncomingWord(gEAX, off)
	a.IncomingWord(gEDX, off+4)
	x.fromPair(rx(r))
}

// compare sets the flags for a predicate comparison. A 64-bit one compares
// the low words and subtracts the high words with borrow, which sets the
// flags of the less-than family; LE, G, BE and A swap the operands, and the
// next branch maps its condition. Equality ORs the words of the difference.
func (x *i386Enc) compare(p *bPhys) {
	a := x.a
	c := op.BranchCondition(p.sub)
	if p.w != 64 {
		left := x.gp(p.b, gEAX)
		if p.k == pkCmpImm {
			a.CompareImmediate(left, int64(p.imm))
			return
		}
		a.CompareRegister(left, x.gp(p.c, gECX))
		return
	}
	equality := c == op.BranchEqual || c == op.BranchNotEqual
	swap := c == op.BranchLessEqual || c == op.BranchGreater || c == op.BranchBelowEqual || c == op.BranchAbove
	x.remap = swap
	if p.k == pkCmpImm {
		lo, hi := uint64(uint32(p.imm)), p.imm>>32
		x.toPair(rx(p.b))
		switch {
		case equality:
			if lo != 0 {
				a.BinaryImmediate(op.OpXor, 32, gEAX, gEAX, lo)
			}
			if hi != 0 {
				a.BinaryImmediate(op.OpXor, 32, gEDX, gEDX, hi)
			}
			a.Binary(op.OpOr, 32, gEAX, gEAX, gEDX)
		case swap:
			a.Constant(gECX, lo)
			a.CompareRegister(gECX, gEAX)
			a.Constant(gECX, hi)
			a.BinaryCarry(op.OpSub, gECX, gECX, gEDX)
		default:
			a.CompareImmediate(gEAX, int64(lo))
			a.Constant(gECX, hi)
			a.BinaryCarry(op.OpSub, gEDX, gEDX, gECX)
		}
		return
	}
	left, right := rx(p.b), rx(p.c)
	if equality {
		_ = a.Vector(machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpXor, Bits: 64, A: vX2, B: left, C: right})
		x.toPair(vX2)
		a.Binary(op.OpOr, 32, gEAX, gEAX, gEDX)
		return
	}
	if swap {
		left, right = right, left
	}
	x.toPair(left)
	x.fmove(32, gECX, right)
	a.CompareRegister(gEAX, gECX)
	// MOVD and PSHUFD keep the flags.
	a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: gECX, HighA: gECX, B: right})
	a.BinaryCarry(op.OpSub, gEDX, gEDX, gECX)
}

var i386ShiftBy = [256]op.Opcode{
	ir.OpShiftLeftBy: op.OpShiftLeft, ir.OpShiftRightBy: op.OpShiftRight, ir.OpShiftRightArithmeticBy: op.OpShiftRightArithmetic,
	ir.OpRotateLeftBy: op.OpRotateLeft, ir.OpRotateRightBy: op.OpRotateRight,
}

func commutative(o ir.Opcode) bool {
	return o == ir.OpAdd || o == ir.OpMul || o == ir.OpAnd || o == ir.OpOr || o == ir.OpXor
}

// op encodes one operation record.
func (x *i386Enc) op(e *baselineEngine, p *bPhys, pc int) error {
	a := x.a
	d, s0, s1, s2 := rx(p.a), rx(p.b), rx(p.c), rx(p.d)
	w := p.w
	switch p.sub {
	case i386ParamNarrow:
		a.IncomingWord(gEAX, int(p.imm))
		a.Narrow(gEAX, gEAX, w)
		if x.stack == abi.StackPlatform {
			// A disp32 store, whose size does not depend on the frame.
			a.Spill(gEAX, (x.frame+int(p.imm)-x.callArea)/4)
		} else {
			a.StoreOffset(gESP, gEAX, p.imm)
		}
		return nil
	case i386ArgStore:
		switch {
		case !isXMM(p.a):
			a.StoreOffset(gESP, d, p.imm)
		case w == 64:
			return a.FloatMemory(machine.Instruction{Op: machine.OpFloatSpill, Bits: 64, A: d, Slot: (int(p.imm) - x.callArea) / 4})
		default:
			x.fmove(32, gEAX, d)
			a.StoreOffset(gESP, gEAX, p.imm)
		}
		return nil
	case i386GoResult:
		a.LoadOffset(gEAX, gESP, p.imm)
		a.LoadOffset(gEDX, gESP, p.imm+4)
		return nil
	case i386PairToX:
		x.fromPair(d)
		return nil
	case i386Filler:
		return nil
	case i386XToPair:
		x.toPair(s0)
		return nil
	}
	o := ir.Opcode(p.sub)
	switch o {
	case ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpMul:
		if w == 64 {
			if o == ir.OpMul {
				return x.mul64(p)
			}
			return a.Vector(machine.Instruction{Op: machine.OpVector, Arithmetic: o, Bits: 64, A: d, B: s0, C: s1})
		}
		if p.k == pkOpImm {
			a.BinaryImmediate(o, w, d, s0, uint64(uint32(p.imm)))
			if w < 32 && (o == ir.OpAdd || o == ir.OpSub) {
				a.Narrow(d, d, w)
			}
			return nil
		}
		switch {
		case d != s1 || d == s0:
			a.Binary(o, w, d, s0, s1)
		case commutative(o):
			a.Binary(o, w, d, s1, s0)
		default:
			a.Binary(o, w, gEAX, s0, s1)
			a.Move(d, gEAX)
		}
	case ir.OpAddScaled:
		scale := uint64(uint32(p.imm))
		switch {
		case scale == 0:
			a.Move(d, s0)
		case scale == 1 || scale == 2 || scale == 4 || scale == 8:
			a.Scaled(d, s0, s1, scale, 0)
			if w < 32 {
				a.Narrow(d, d, w)
			}
		default:
			a.Constant(gEAX, scale)
			a.Binary(op.OpMul, 32, gEAX, gEAX, s1)
			a.Binary(op.OpAdd, w, d, s0, gEAX)
		}
	case ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight:
		count := uint8(p.imm)
		switch {
		case w == 64:
			x.toPair(s0)
			a.ShiftInstruction(machine.Instruction{Op: machine.OpShift, Arithmetic: o, Bits: 64, A: gEAX, HighA: gEDX, B: gEAX, HighB: gEDX, C: gECX, Imm: uint64(count)})
			x.fromPair(d)
		case w == 32 || o == ir.OpShiftLeft || o == ir.OpShiftRight || o == ir.OpShiftRightArithmetic:
			a.Shift(o, w, d, s0, count)
		default:
			k := count % w
			if o == ir.OpRotateRight {
				k = (w - k) % w
			}
			if k == 0 {
				a.Move(d, s0)
				return nil
			}
			a.Shift(op.OpShiftLeft, 32, gEAX, s0, k)
			a.Shift(op.OpShiftRight, 32, gECX, s0, w-k)
			a.Binary(op.OpOr, w, d, gEAX, gECX)
		}
	case ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy:
		save := wslot(p.imm)
		a.Spill(gEBX, save)
		if w == 64 {
			x.toPair(s0)
		} else {
			a.Move(gEAX, s0)
		}
		if isXMM(p.c) {
			a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: gECX, HighA: gEBX, B: s1})
		} else {
			a.Move(gECX, s1)
			a.Constant(gEBX, 0)
		}
		a.ShiftByInstruction(machine.Instruction{Op: machine.OpShiftBy, Arithmetic: i386ShiftBy[o], Bits: w})
		a.Reload(gEBX, save)
		if w == 64 {
			x.fromPair(d)
		} else {
			a.Move(d, gEAX)
		}
	case ir.OpCastUnsigned:
		from := uint8(p.d)
		switch {
		case w == 64 && isXMM(p.b):
			x.fmove(64, d, s0)
		case w == 64:
			x.fmove(32, d, s0)
		case isXMM(p.b):
			x.fmove(32, d, s0)
			if w < 32 {
				a.Narrow(d, d, w)
			}
		case w >= from || w == 32:
			a.Move(d, s0)
		default:
			a.Narrow(d, s0, w)
		}
	case ir.OpSignExtend:
		k := uint8(p.imm)
		g := x.gp(p.b, gEAX)
		if w == 64 {
			a.SignExtend(gEAX, g, k)
			a.Move(gEDX, gEAX)
			a.Shift(op.OpShiftRightArithmetic, 32, gEDX, gEDX, 31)
			x.fromPair(d)
			return nil
		}
		a.SignExtend(d, g, k)
		if w < 32 {
			a.Narrow(d, d, w)
		}
	case ir.OpLeadingZeros, ir.OpTrailingZeros:
		return x.expand(pc, func(x *i386Enc) error { x.countZeros(o, w, p.a, p.b); return nil })
	case ir.OpSelect:
		cond := s0
		if isXMM(p.b) {
			x.toPair(s0)
			a.Binary(op.OpOr, 32, gEAX, gEAX, gEDX)
			cond = gEAX
		}
		if w == 64 {
			return x.expand(pc, func(x *i386Enc) error {
				f, end := x.label(), x.label()
				x.a.CompareImmediate(cond, 0)
				x.jumpIf(op.BranchEqual, f)
				x.fmove(64, d, s1)
				x.jump(end)
				x.bind(f)
				x.fmove(64, d, s2)
				x.bind(end)
				return nil
			})
		}
		if d == s1 && d != s2 || d == cond {
			a.SelectValue(gECX, cond, s1, s2)
			a.Move(d, gECX)
		} else {
			a.SelectValue(d, cond, s1, s2)
		}
	case ir.OpMulHigh, ir.OpMulHighSigned:
		switch {
		case w == 64:
			return x.mulHigh64(p, o == ir.OpMulHighSigned)
		case w == 32 && o == ir.OpMulHigh:
			a.MulHigh(32, d, s0, s1)
		case w == 32:
			a.MulHighSigned(32, d, s0, s1)
		case o == ir.OpMulHigh:
			a.Binary(op.OpMul, 32, gEAX, s0, s1)
			a.Shift(op.OpShiftRight, 32, d, gEAX, w)
		default:
			a.SignExtend(gEAX, s0, w)
			a.SignExtend(gECX, s1, w)
			a.Binary(op.OpMul, 32, gEAX, gEAX, gECX)
			a.Shift(op.OpShiftRightArithmetic, 32, gEAX, gEAX, w)
			a.Narrow(d, gEAX, w)
		}
	case ir.OpCarryAdd, ir.OpBorrowSub:
		arith := op.OpAdd
		if o == ir.OpBorrowSub {
			arith = op.OpSub
		}
		switch {
		case w == 64:
			if err := x.spillX(s1, p.imm); err != nil {
				return err
			}
			x.toPair(s0)
			a.Reload(gECX, wslot(p.imm))
			a.Binary(arith, 32, gEAX, gEAX, gECX)
			a.Reload(gECX, wslot(p.imm)+1)
			a.BinaryCarry(arith, gEDX, gEDX, gECX)
			a.Constant(gEAX, 0)
			a.BinaryCarry(op.OpAdd, gEAX, gEAX, gEAX)
			x.fmove(32, d, gEAX)
		case w < 32 && o == ir.OpCarryAdd:
			a.Binary(op.OpAdd, 32, gEAX, s0, s1)
			a.Shift(op.OpShiftRight, 32, d, gEAX, w)
		default:
			a.Binary(arith, 32, gEAX, s0, s1)
			a.Constant(gECX, 0)
			a.BinaryCarry(op.OpAdd, gECX, gECX, gECX)
			a.Move(d, gECX)
		}
	case ir.OpAddWithCarry, ir.OpSubWithBorrow:
		arith := op.OpAdd
		if o == ir.OpSubWithBorrow {
			arith = op.OpSub
		}
		if w == 64 {
			if err := x.spillX(s1, p.imm); err != nil {
				return err
			}
			x.fmove(32, gECX, s2)
			a.Shift(op.OpShiftRight, 32, gECX, gECX, 1) // CF = C & 1
			x.toPair(s0)
			a.Reload(gECX, wslot(p.imm))
			a.BinaryCarry(arith, gEAX, gEAX, gECX)
			a.Reload(gECX, wslot(p.imm)+1)
			a.BinaryCarry(arith, gEDX, gEDX, gECX)
			x.fromPair(d)
			return nil
		}
		a.Move(gECX, s2)
		a.Shift(op.OpShiftRight, 32, gECX, gECX, 1)
		a.BinaryCarry(arith, gEAX, s0, s1)
		if w < 32 {
			a.Narrow(d, gEAX, w)
		} else {
			a.Move(d, gEAX)
		}
	case ir.OpDiv, ir.OpDivSigned, ir.OpRem, ir.OpRemSigned:
		if w != 64 {
			a.Divide(machine.Instruction{Op: machine.OpDivide, Arithmetic: o, Bits: w, A: d, B: s0, C: s1, Imm: p.imm})
			return nil
		}
		semantics := p.imm & 0xff
		base := wslot(p.imm >> 8 & 0xffff)
		saveA, saveB := wslot(p.imm>>24&0xffff), wslot(p.imm>>40&0xffff)
		if err := a.FloatMemory(machine.Instruction{Op: machine.OpFloatSpill, Bits: 64, A: s0, Slot: base}); err != nil {
			return err
		}
		if err := a.FloatMemory(machine.Instruction{Op: machine.OpFloatSpill, Bits: 64, A: s1, Slot: base + 2}); err != nil {
			return err
		}
		a.Spill(gEBX, saveA)
		a.Spill(gESI, saveA+1)
		a.Spill(gEDI, saveB)
		a.Divide(machine.Instruction{Op: machine.OpDivide, Arithmetic: o, Bits: 64, A: gEAX, HighA: gEDX, Imm: semantics, Slot: base})
		a.Reload(gEBX, saveA)
		a.Reload(gESI, saveA+1)
		a.Reload(gEDI, saveB)
		x.fromPair(d)
	case ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv:
		a.FloatBinary(o, w, d, s0, s1)
	case ir.OpFSqrt, ir.OpFConvert:
		a.FloatUnary(o, w, d, s0)
	case ir.OpFProductError:
		return x.productError(p)
	case ir.OpFFromSigned, ir.OpFFromUnsigned:
		if p.d == 64 {
			x.toPair(s0)
			a.FloatConvert(machine.Instruction{Op: machine.OpIntToFloat, Arithmetic: o, Bits: w, A: d, B: gEAX, HighB: gEDX, Imm: 64, Slot: wslot(p.imm)})
			return nil
		}
		a.FloatConvert(machine.Instruction{Op: machine.OpIntToFloat, Arithmetic: o, Bits: w, A: d, B: x.gp(p.b, gECX), Imm: 32})
	case ir.OpFToSigned, ir.OpFToUnsigned:
		if w == 64 {
			a.FloatConvert(machine.Instruction{Op: machine.OpFloatToInt, Arithmetic: o, Bits: 64, A: gEAX, HighA: gEDX, B: s0, Imm: p.imm, Disp: uint64(p.d)})
			x.fromPair(d)
			return nil
		}
		a.FloatConvert(machine.Instruction{Op: machine.OpFloatToInt, Arithmetic: o, Bits: 32, A: d, B: s0, Imm: p.imm, Disp: uint64(p.d)})
	case ir.OpFCompare:
		if w == 64 {
			a.FloatCompare(op.FloatCondition(p.imm), uint8(p.d), gEAX, s0, s1)
			x.fmove(32, d, gEAX)
			return nil
		}
		a.FloatCompare(op.FloatCondition(p.imm), uint8(p.d), d, s0, s1)
	case ir.OpLoad8:
		addr := x.gp(p.b, gECX)
		if isXMM(p.a) {
			a.Load8(gEAX, addr)
			x.fmove(32, d, gEAX)
			return nil
		}
		a.Load8(d, addr)
	case ir.OpLoad16, ir.OpLoad32:
		addr := x.gp(p.b, gECX)
		mop := machine.OpLoad16
		if o == ir.OpLoad32 {
			mop = machine.OpLoad32
		}
		t := d
		bigEndian := op.ByteOrder(p.imm) == op.ByteOrderBigEndian
		if isXMM(p.a) || bigEndian {
			t = gEAX
		}
		if err := a.NarrowMemory(machine.Instruction{Op: mop, A: t, B: addr}); err != nil {
			return err
		}
		if bigEndian {
			if o == ir.OpLoad32 {
				x.swap32(gEAX)
			} else {
				a.Shift(op.OpShiftLeft, 32, gEDX, gEAX, 8)
				a.Shift(op.OpShiftRight, 32, gEAX, gEAX, 8)
				a.Binary(op.OpOr, 16, gEAX, gEAX, gEDX)
			}
		}
		switch {
		case isXMM(p.a):
			x.fmove(32, d, gEAX)
		case t != d:
			a.Move(d, t)
		}
	case ir.OpLoad64:
		addr := x.gp(p.b, gECX)
		a.LoadOffset(gEAX, addr, 0)
		a.LoadOffset(gEDX, addr, 4)
		x.fromPair(d)
	case ir.OpStore8:
		addr := x.gp(p.b, gECX)
		v := x.gp(p.c, gEAX)
		if v != gEAX && v > gEBX {
			a.Move(gEAX, v)
			v = gEAX
		}
		a.Store8(addr, v)
	case ir.OpStore16, ir.OpStore32:
		addr := x.gp(p.b, gECX)
		v := x.gp(p.c, gEAX)
		if op.ByteOrder(p.imm) == op.ByteOrderBigEndian {
			if o == ir.OpStore32 {
				x.swap32(v)
			} else {
				a.Shift(op.OpShiftLeft, 32, gEDX, v, 8)
				a.Shift(op.OpShiftRight, 32, gEAX, v, 8)
				a.BinaryImmediate(op.OpAnd, 32, gEAX, gEAX, 0xff)
				a.Binary(op.OpOr, 32, gEAX, gEAX, gEDX)
			}
			v = gEAX
		}
		mop := machine.OpStore16
		if o == ir.OpStore32 {
			mop = machine.OpStore32
		}
		return a.NarrowMemory(machine.Instruction{Op: mop, A: addr, B: v})
	case ir.OpStore64:
		addr := x.gp(p.b, gECX)
		x.toPair(s1)
		a.StoreOffset(addr, gEAX, 0)
		a.StoreOffset(addr, gEDX, 4)
	default:
		return fmt.Errorf("baseline 386: %v: %w", o, ErrUnsupported)
	}
	return nil
}

// swap32 reverses the bytes of v into EAX, through EDX.
func (x *i386Enc) swap32(v backend.Register) {
	a := x.a
	a.Shift(op.OpRotateRight, 32, gEDX, v, 8)
	a.BinaryImmediate(op.OpAnd, 32, gEDX, gEDX, 0xff00ff00)
	a.Shift(op.OpRotateLeft, 32, gEAX, v, 8)
	a.BinaryImmediate(op.OpAnd, 32, gEAX, gEAX, 0x00ff00ff)
	a.Binary(op.OpOr, 32, gEAX, gEAX, gEDX)
}

// mul64 multiplies the pairs in their slots: the cross products' low words
// add to the high word of the low product.
func (x *i386Enc) mul64(p *bPhys) error {
	a := x.a
	sa, sb := p.imm&0xffff, p.imm>>16&0xffff
	if err := x.spillX(rx(p.b), sa); err != nil {
		return err
	}
	if err := x.spillX(rx(p.c), sb); err != nil {
		return err
	}
	a0, a1, b0, b1 := wslot(sa), wslot(sa)+1, wslot(sb), wslot(sb)+1
	a.Reload(gECX, a1)
	a.Reload(gEDX, b0)
	a.Binary(op.OpMul, 32, gECX, gECX, gEDX)
	a.Reload(gEAX, a0)
	a.Reload(gEDX, b1)
	a.Binary(op.OpMul, 32, gEDX, gEDX, gEAX)
	a.Binary(op.OpAdd, 32, gECX, gECX, gEDX)
	a.Reload(gEDX, b0)
	a.MulWide(gEDX)
	a.Binary(op.OpAdd, 32, gEDX, gEDX, gECX)
	x.fromPair(rx(p.a))
	return nil
}

// mulHigh64 sums the four 32×32 products by columns in scratch words; the
// signed form subtracts each operand where the other is negative.
func (x *i386Enc) mulHigh64(p *bPhys, signed bool) error {
	a := x.a
	sa, sb, st, su := p.imm&0xffff, p.imm>>16&0xffff, p.imm>>32&0xffff, p.imm>>48&0xffff
	if err := x.spillX(rx(p.b), sa); err != nil {
		return err
	}
	if err := x.spillX(rx(p.c), sb); err != nil {
		return err
	}
	a0, a1, b0, b1 := wslot(sa), wslot(sa)+1, wslot(sb), wslot(sb)+1
	t0, t1, t2 := wslot(st), wslot(st)+1, wslot(su)
	a.Reload(gEAX, a0)
	a.Reload(gECX, b0)
	a.MulWide(gECX)
	a.Spill(gEDX, t0) // column 1: high word of a0*b0
	a.Reload(gEAX, a0)
	a.Reload(gECX, b1)
	a.MulWide(gECX)
	a.Reload(gECX, t0)
	a.Binary(op.OpAdd, 32, gECX, gECX, gEAX)
	a.Spill(gECX, t0)
	a.Constant(gEAX, 0)
	a.BinaryCarry(op.OpAdd, gEDX, gEDX, gEAX)
	a.Spill(gEDX, t1) // column 2
	a.Reload(gEAX, a1)
	a.Reload(gECX, b0)
	a.MulWide(gECX)
	a.Reload(gECX, t0)
	a.Binary(op.OpAdd, 32, gECX, gECX, gEAX)
	a.Constant(gEAX, 0)
	a.BinaryCarry(op.OpAdd, gEDX, gEDX, gEAX)
	a.Reload(gECX, t1)
	a.Binary(op.OpAdd, 32, gECX, gECX, gEDX)
	a.Spill(gECX, t1)
	a.Constant(gEAX, 0)
	a.BinaryCarry(op.OpAdd, gEAX, gEAX, gEAX)
	a.Spill(gEAX, t2) // column 3
	a.Reload(gEAX, a1)
	a.Reload(gECX, b1)
	a.MulWide(gECX)
	a.Reload(gECX, t1)
	a.Binary(op.OpAdd, 32, gEAX, gEAX, gECX)
	a.Reload(gECX, t2)
	a.BinaryCarry(op.OpAdd, gEDX, gEDX, gECX)
	if signed {
		a.Spill(gEAX, t0)
		a.Spill(gEDX, t1)
		for _, k := range [2][3]int{{a1, b0, b1}, {b1, a0, a1}} {
			a.Reload(gECX, k[0])
			a.Shift(op.OpShiftRightArithmetic, 32, gECX, gECX, 31)
			a.Reload(gEAX, k[1])
			a.Binary(op.OpAnd, 32, gEAX, gEAX, gECX)
			a.Reload(gEDX, k[2])
			a.Binary(op.OpAnd, 32, gEDX, gEDX, gECX)
			a.Reload(gECX, t0)
			a.Binary(op.OpSub, 32, gECX, gECX, gEAX)
			a.Spill(gECX, t0)
			a.Reload(gECX, t1)
			a.BinaryCarry(op.OpSub, gECX, gECX, gEDX)
			a.Spill(gECX, t1)
		}
		a.Reload(gEAX, t0)
		a.Reload(gEDX, t1)
	}
	x.fromPair(rx(p.a))
	return nil
}

// productError computes A×B − C with Dekker's split; ahi and alo wait in
// two scratch slots.
func (x *i386Enc) productError(p *bPhys) error {
	a := x.a
	d, sa, sb, sc := rx(p.a), rx(p.b), rx(p.c), rx(p.d)
	shi, slo := p.imm&0xffff, p.imm>>16&0xffff
	const split = 0x41a0000002000000 // 2^27 + 1
	a.Constant(gEAX, split&0xffffffff)
	a.Constant(gEDX, split>>32)
	x.fromPair(vX0)
	fb := func(o op.Opcode, d, l, r backend.Register) { a.FloatBinary(o, 64, d, l, r) }
	half := func(v backend.Register) {
		fb(op.OpFMul, vX1, v, vX0)
		fb(op.OpFSub, vX2, vX1, v)
		fb(op.OpFSub, vX1, vX1, vX2) // high half
		fb(op.OpFSub, vX2, v, vX1)   // low half
	}
	half(sa)
	if err := x.spillX(vX1, shi); err != nil {
		return err
	}
	if err := x.spillX(vX2, slo); err != nil {
		return err
	}
	half(sb) // XMM1 = bhi, XMM2 = blo
	if err := x.reloadX(vX0, shi); err != nil {
		return err
	}
	fb(op.OpFMul, vX0, vX0, vX1)
	fb(op.OpFSub, vX0, vX0, sc)
	// A, B and C are read: D may alias any of them from here.
	for _, t := range [3][2]uint64{{shi, 2}, {slo, 1}, {slo, 2}} {
		if err := x.reloadX(d, t[0]); err != nil {
			return err
		}
		fb(op.OpFMul, d, d, backend.VectorRegister(uint8(t[1])))
		fb(op.OpFAdd, vX0, vX0, d)
	}
	x.fmove(64, d, vX0)
	return nil
}

// countZeros counts leading or trailing zeros of w-bit s into d, by binary
// search in ECX.
func (x *i386Enc) countZeros(o ir.Opcode, w uint8, dst, src int8) {
	a := x.a
	lead := o == ir.OpLeadingZeros
	a.Constant(gECX, 0)
	reg := gEAX
	zero := uint64(w)
	if w == 64 {
		x.toPair(rx(src))
		skip := x.label()
		if lead {
			a.CompareImmediate(gEDX, 0)
			x.jumpIf(op.BranchNotEqual, skip)
			a.BinaryImmediate(op.OpAdd, 32, gECX, gECX, 32)
			a.Move(gEDX, gEAX)
			reg = gEDX
		} else {
			a.CompareImmediate(gEAX, 0)
			x.jumpIf(op.BranchNotEqual, skip)
			a.BinaryImmediate(op.OpAdd, 32, gECX, gECX, 32)
			a.Move(gEAX, gEDX)
		}
		x.bind(skip)
		zero = 32
	} else {
		a.Move(gEAX, rx(src))
	}
	isZero, end := x.label(), x.label()
	a.CompareImmediate(reg, 0)
	x.jumpIf(op.BranchEqual, isZero)
	for _, n := range [...]uint8{16, 8, 4, 2, 1} {
		next := x.label()
		if lead {
			a.CompareImmediate(reg, int64(uint32(0xffffffff)>>n))
			x.jumpIf(op.BranchAbove, next)
		} else {
			a.BinaryImmediate(op.OpAnd, 32, gEDX, reg, uint64(1)<<n-1)
			x.jumpIf(op.BranchNotEqual, next)
		}
		a.BinaryImmediate(op.OpAdd, 32, gECX, gECX, uint64(n))
		if n != 1 {
			if lead {
				a.Shift(op.OpShiftLeft, 32, reg, reg, n)
			} else {
				a.Shift(op.OpShiftRight, 32, reg, reg, n)
			}
		}
		x.bind(next)
	}
	x.jump(end)
	x.bind(isZero)
	if lead && w < 32 {
		// clz32 of the zero-extended value counts 32 - w extra zeros.
		zero = 32
	}
	a.BinaryImmediate(op.OpAdd, 32, gECX, gECX, zero)
	x.bind(end)
	if lead && w < 32 {
		a.BinaryImmediate(op.OpSub, 32, gECX, gECX, uint64(32-w))
	}
	if w == 64 {
		x.fmove(32, rx(dst), gECX)
	} else {
		a.Move(rx(dst), gECX)
	}
}
