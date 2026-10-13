package compile

import (
	"encoding/binary"
	"fmt"
	"math/bits"
	"strconv"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	targetppc64 "github.com/moriyoshi/nyanjit/backend/target/ppc64"
	"github.com/moriyoshi/nyanjit/ir"
)

// ppc64BaselineArch is the baseline engine's ppc64le part. It builds
// targetppc64 concrete instructions and encodes them with the target's
// ByteAssembler or TextAssembler. The few long expansions the target already
// implements (division, register-count shifts, the frame) come from
// targetppc64.Emitter, which expands one operation into concrete
// instructions.
//
// Register model:
//   - R0 is never allocated: as a base or ISEL source it reads as zero. Only
//     a system call number goes there, and calls and system calls leave it
//     zero, as the Go ABI expects.
//   - R1 is the stack pointer, R2 the TOC, R13 the thread pointer, R29 the
//     workspace pointer without a platform stack, R30 Go's g, R31 Go's
//     assembler temporary; none is allocated.
//   - R11 (cycle) and R12 (transit, call target) are the engine's scratch
//     registers and the target expansions' temporaries.
//   - F0, F1 and F2 are FP scratch: F0 for results and conversions, F1 and
//     F2 for binary32 operands.
//
// FPRs hold binary32 values in double format, so the bits of a binary32
// value differ from its FPR image. The baseline therefore keeps binary32
// values in general registers (baselineModel.banks) and converts them into
// F0-F2 around each operation; binary64 values and integers have the same
// image in both banks.
//
// Every compare, record form and fcmpu writes CR0, and CR0 is assumed dead
// across records: each branch is preceded by its own compare.
type ppc64BaselineArch struct{}

const (
	ppcCycle   int8 = 11
	ppcTransit int8 = 12
	ppcF0      int8 = fpBase
)

var (
	ppc64SavedGPC  = []int8{14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28}
	ppc64SavedGPGo = []int8{18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28}
	ppc64SavedFP   = func() (s []int8) {
		for r := int8(14); r < 32; r++ {
			s = append(s, fpBase+r)
		}
		return
	}()
	ppc64ParamsC   = []int8{3, 4, 5, 6, 7, 8, 9, 10}
	ppc64ParamsGo  = []int8{3, 4, 5, 6, 7, 8, 9, 10, 14, 15, 16, 17}
	ppc64Models    [2]baselineModel // by Go entry
	ppc64ModelOnce sync.Once
)

// model returns the ppc64le register model. A C entry preserves R14-R28
// and F14-F31 it writes. Go's ABI preserves no general register, so a Go
// entry pins Vars to R18-R28 without saving them; F14-F31 are saved in both
// conventions.
func (ppc64BaselineArch) model(o compileOptions) baselineModel {
	ppc64ModelOnce.Do(func() {
		for goEntry := range 2 {
			m := baselineModel{
				calleeSavedGP: ppc64SavedGPC, calleeSavedFP: ppc64SavedFP,
				params: ppc64ParamsC, results: [2]int8{3, 4},
				transit: ppcTransit, cycle: ppcCycle, scratchFP: ppcF0,
				callTarget: ppcTransit, banks: ppc64Banks,
			}
			if goEntry == 1 {
				m.params, m.calleeSavedGP = ppc64ParamsGo, ppc64SavedGPGo
			}
			for r := int8(3); r <= 28; r++ {
				if r <= 10 || r >= 14 {
					m.allocatable |= regBit(r)
				}
			}
			if goEntry == 0 {
				for _, r := range m.calleeSavedGP {
					m.saved |= regBit(r)
				}
			}
			for r := int8(3); r < 32; r++ {
				m.allocatable |= regBit(fpBase + r)
			}
			for _, r := range m.calleeSavedFP {
				m.saved |= regBit(r)
			}
			ppc64Models[goEntry] = m
		}
	})
	goEntry := 0
	if o.ABI == abi.Go {
		goEntry = 1
	}
	m := ppc64Models[goEntry]
	// Every spill slot stays within a D-form displacement, so spills and
	// reloads need no address temporary.
	m.spillLimit = maxSpillSlots
	if o.Stack == abi.StackPlatform {
		m.spillLimit = min(maxSpillSlots, (32764-ppc64SpillOffset)/8+1)
	}
	return m
}

// ppc64SpillOffset is targetppc64.Emitter.SpillOffset: spill slots start
// above the linkage area and the outgoing argument homes.
const ppc64SpillOffset = 128

// ppc64FloatWidths returns the floating-point width of r's FP operands and
// of its FP result, or 0 where the operand or result is an integer.
func ppc64FloatWidths(e *baselineEngine, r *bRec) (in, out uint8) {
	switch r.op {
	case ir.OpFConvert:
		return 96 - r.bits, r.bits
	case ir.OpFFromSigned, ir.OpFFromUnsigned:
		return 0, r.bits
	case ir.OpFToSigned, ir.OpFToUnsigned:
		return e.valueBits(r.a), 0
	case ir.OpFCompare:
		return r.bits, 0
	}
	return r.bits, r.bits
}

// ppc64Banks keeps binary32 operands and results in general registers.
func ppc64Banks(e *baselineEngine, r *bRec) {
	in, out := ppc64FloatWidths(e, r)
	if in == 32 {
		r.fpu = 0
	}
	if out == 32 {
		r.fl &^= rfDefFP
	}
}

func (ppc64BaselineArch) foldable(r *bRec, k int, imm uint64) bool {
	if k != 1 {
		return false
	}
	switch r.op {
	case ir.OpAdd:
		return int64(imm) >= -32768 && int64(imm) <= 32767
	case ir.OpSub:
		return int64(imm) >= -32767 && int64(imm) <= 32768
	case ir.OpAnd, ir.OpOr, ir.OpXor:
		return imm <= 0xffff
	}
	return false
}

func (x ppc64BaselineArch) lower(e *baselineEngine, r *bRec, dst int8, src [3]int8) error {
	switch r.op {
	case ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpMul, ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic:
		// The hot operations, written in place with their size.
		p := e.slot()
		p.k, p.sub, p.w, p.a, p.b, p.c, p.imm = pkOp, uint8(r.op), r.bits, dst, src[0], src[1], r.imm
		if src[1] < 0 && r.op != ir.OpShiftLeft && r.op != ir.OpShiftRight && r.op != ir.OpShiftRightArithmetic {
			p.k, p.imm = pkOpImm, e.recs[r.b].imm
		}
		if n := ppc64FastOpSize(p); n >= 0 {
			p.sz = uint8(n)
		} else {
			p.sz = uint8(x.size(e, p))
		}
		return nil
	}
	p := bPhys{k: pkOp, sub: uint8(r.op), w: r.bits, a: dst, b: src[0], c: src[1], d: src[2], imm: r.imm}
	switch r.op {
	case ir.OpCastUnsigned, ir.OpFFromSigned, ir.OpFFromUnsigned, ir.OpFToSigned, ir.OpFToUnsigned, ir.OpFCompare:
		p.d = int8(e.valueBits(r.a))
	case ir.OpLoad16, ir.OpLoad32, ir.OpStore16, ir.OpStore32:
		p.imm = uint64(r.ord)
	case ir.OpFProductError:
		if r.bits != 64 {
			return fmt.Errorf("baseline ppc64le: %d-bit OpFProductError: %w", r.bits, ErrInvalidFunction)
		}
	}
	if baselineShapes[r.op].fpUse != 0 || baselineShapes[r.op].defFP {
		if in, out := ppc64FloatWidths(e, r); in == 32 || out == 32 {
			e.alloc.used |= regBit(fpBase+1) | regBit(fpBase+2)
		}
	}
	e.put(p)
	return nil
}

// ppc64FastOpSize sizes the common operations without expanding them, or
// returns -1.
func ppc64FastOpSize(p *bPhys) int {
	narrow := 0
	if p.w < 64 {
		narrow = 4
	}
	switch ir.Opcode(p.sub) {
	case ir.OpAdd, ir.OpSub:
		return 4 + narrow
	case ir.OpMul:
		if p.k == pkOp {
			return 4 + narrow
		}
		return -1
	case ir.OpAnd, ir.OpOr, ir.OpXor:
		return 4
	case ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic:
		if p.imm == 0 {
			if p.a == p.b {
				return 0
			}
			return 4
		}
		if ir.Opcode(p.sub) == ir.OpShiftRightArithmetic {
			return 4 + narrow
		}
		return 4
	}
	return -1
}

// ppcConstWords counts the instructions ppcConstant emits for v.
func ppcConstWords(v uint64) int {
	switch s := int64(v); {
	case s >= -32768 && s <= 32767:
		return 1
	case s >= -1<<31 && s < 1<<31:
		if uint16(v) != 0 {
			return 2
		}
		return 1
	case v>>32 == 0:
		if uint16(v) != 0 {
			return 3
		}
		return 2
	}
	n := 1 // sldi
	if h := int64(int32(uint32(v >> 32))); h >= -32768 && h <= 32767 {
		n++
	} else {
		n++
		if uint16(v>>32) != 0 {
			n++
		}
	}
	if uint16(v>>16) != 0 {
		n++
	}
	if uint16(v) != 0 {
		n++
	}
	return n
}

func (x ppc64BaselineArch) size(e *baselineEngine, p *bPhys) int {
	switch p.k {
	case pkLabel:
		return 0
	case pkMove:
		if p.a == p.b {
			return 0
		}
		return 4
	case pkConst:
		n := 4 * ppcConstWords(p.imm)
		if p.a >= fpBase {
			n += 4
		}
		return n
	case pkSymbol:
		if p.a >= fpBase {
			return 24
		}
		return 20
	case pkSpill, pkReload, pkCmp, pkJump, pkJumpIf, pkRet:
		return 4
	case pkCmpImm:
		if int64(p.imm) >= 0 {
			return 4
		}
		return 8
	case pkJumpIfLong:
		return 8
	case pkCall:
		return 24
	case pkSyscall:
		return 16
	case pkOp, pkOpImm:
		if n := ppc64FastOpSize(p); n >= 0 {
			return n
		}
	}
	// Measure the rest by expanding them without encoding.
	var o ppcOut
	if err := ppc64EncodeOne(e, &o, unsafe.Slice(p, 1), 0, 0, nil); err != nil || o.err != nil {
		e.emit.sizeErr = true
		return 4
	}
	return o.pc
}

// longBranch reports whether bc at pc cannot reach target: its displacement
// is a signed 16-bit byte offset.
func (ppc64BaselineArch) longBranch(p *bPhys, pc, target int) bool {
	d := target - pc
	return d < -32768 || d > 32764
}

func (ppc64BaselineArch) frame(e *baselineEngine, slots int, hasCall bool) (int, error) {
	if e.options.Stack != abi.StackPlatform {
		return 0, nil
	}
	if slots == 0 && !hasCall {
		return 0, nil
	}
	frame := ppc64EmitterFor(&e.options).FrameBytes(slots, hasCall)
	if frame > e.options.StackBytesAvailable {
		return 0, ErrStackRequired
	}
	return frame, nil
}

// ppc64Emitters caches the target's expansion emitters by convention, stack
// practice and POWER level.
var ppc64Emitters [2][2][3]atomic.Pointer[targetppc64.Emitter]

func ppc64EmitterFor(o *compileOptions) *targetppc64.Emitter {
	g, s, l := 0, 0, 0
	if o.ABI == abi.Go {
		g = 1
	}
	if o.Stack == abi.StackPlatform {
		s = 1
	}
	level := ppcLevel(*o)
	switch level {
	case abi.PPC64Power9:
		l = 1
	case abi.PPC64Power10:
		l = 2
	}
	slot := &ppc64Emitters[g][s][l]
	if em := slot.Load(); em != nil {
		return em
	}
	profile, err := ppc64Profile(*o)
	if err != nil {
		profile, _ = abi.NewPPC64Profile("ppc64le", abi.C)
	}
	em, err := targetppc64.NewEmitterForCPU(profile, o.Stack, false, level)
	if err != nil {
		panic(fmt.Sprintf("baseline ppc64le: emitter: %v", err))
	}
	slot.Store(em)
	return em
}

// ppcOut receives concrete instructions: it encodes them, renders them, or
// only counts them when it has no assembler.
type ppcOut struct {
	asm  *targetppc64.ByteAssembler
	text *targetppc64.TextAssembler
	pc   int
	err  error
}

func (o *ppcOut) fail(err error) {
	if o.err == nil {
		o.err = err
	}
}

func (o *ppcOut) emit(ins targetppc64.Instruction) {
	switch {
	case o.asm != nil:
		if err := o.asm.Visit(ins); err != nil {
			o.fail(err)
		}
	case o.text != nil:
		ins.PC = o.pc
		if err := o.text.Visit(ins); err != nil {
			o.fail(err)
		}
	}
	o.pc += 4
}

// word emits an instruction word the target's Instruction does not
// describe.
func (o *ppcOut) word(w uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], w)
	switch {
	case o.asm != nil:
		if err := o.asm.Data(b[:]); err != nil {
			o.fail(err)
		}
	case o.text != nil:
		if err := o.text.Data(b[:]); err != nil {
			o.fail(err)
		}
	}
	o.pc += 4
}

// expand emits the target emitter's expansion of ins.
func (o *ppcOut) expand(e *baselineEngine, ins machine.Instruction) {
	em := ppc64EmitterFor(&e.options)
	if o.asm == nil && o.text == nil {
		n := em.Size(ins)
		if n == 0 {
			o.fail(fmt.Errorf("baseline ppc64le: operation %d does not expand: %w", ins.Op, ErrBackendLimit))
		}
		o.pc += n
		return
	}
	seq, err := em.Expand(ins, o.pc)
	if err != nil {
		o.fail(err)
		return
	}
	for _, native := range seq {
		if o.asm != nil {
			if err := o.asm.Visit(native); err != nil {
				o.fail(err)
			}
		} else if err := o.text.Visit(native); err != nil {
			o.fail(err)
		}
	}
	o.pc += 4 * len(seq)
}

// ppc64Encoder is the baseline's encoder: a ByteAssembler for code, or a
// TextAssembler for a listing.
type ppc64Encoder struct{ ppcOut }

func (o *ppc64Encoder) AssembledSize() int {
	if o.asm != nil {
		return o.asm.AssembledSize()
	}
	return o.text.AssembledSize()
}
func (o *ppc64Encoder) Err() error                        { return o.err }
func (o *ppc64Encoder) Bytes() []byte                     { return o.asm.Bytes() }
func (o *ppc64Encoder) Relocations() []machine.Relocation { return o.asm.Relocations() }
func (o *ppc64Encoder) Text() string                      { return o.text.Text() }

func (ppc64BaselineArch) encoder(e *baselineEngine, text bool) baselineEncoder {
	if text {
		return &ppc64Encoder{ppcOut{text: targetppc64.NewTextAssembler()}}
	}
	enc, _ := e.emit.arch.(*ppc64Encoder)
	if enc == nil {
		enc = &ppc64Encoder{ppcOut{asm: targetppc64.NewByteAssembler()}}
		e.emit.arch = enc
	} else {
		enc.asm.Reset()
		enc.pc, enc.err = 0, nil
	}
	return enc
}

func (ppc64BaselineArch) encode(e *baselineEngine, enc baselineEncoder, ps []bPhys, pc int, labels []int32) (int, error) {
	o := &enc.(*ppc64Encoder).ppcOut
	for i := range ps {
		p := &ps[i]
		o.pc = pc
		if err := ppc64EncodeOne(e, o, ps, i, pc, labels); err != nil {
			return pc, err
		}
		if o.err != nil {
			return pc, o.err
		}
		pc += int(p.sz)
		if o.pc != pc {
			return pc, fmt.Errorf("baseline ppc64le: record kind %d op %d encoded %d bytes, sized %d: %w", p.k, p.sub, o.pc-pc+int(p.sz), p.sz, ErrInvalidFunction)
		}
	}
	return pc, nil
}

func (ppc64BaselineArch) registerName(r int8) string {
	if r >= fpBase {
		return "F" + strconv.Itoa(int(r-fpBase))
	}
	return "R" + strconv.Itoa(int(r))
}

func (ppc64BaselineArch) register(name string) (int8, bool) {
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

// adjustReport replaces the engine's arm64 register conventions: ppc64le
// has no link GPR (LR is a special register) and its workspace pointer R29
// is never allocated. It names the special registers the code writes and
// the POWER level it was encoded for.
func (ppc64BaselineArch) adjustReport(u *baselineUnit, r *abi.EmissionReport) {
	f := &u.facts
	r.PPC64CPU = ppcLevel(u.options)
	r.ClobberedRegisters = r.ClobberedRegisters[:0]
	for m := (f.used | f.callers) &^ u.m.saved & bankMaskGP; m != 0; m &= m - 1 {
		r.ClobberedRegisters = append(r.ClobberedRegisters, "R"+strconv.Itoa(bits.TrailingZeros64(m)))
	}
	if len(r.ClobberedRegisters) == 0 {
		r.ClobberedRegisters = nil
	}
	var lr, ctr, carry, fpscr, goCall bool
	var cr uint8
	for i := range u.phys {
		p := &u.phys[i]
		switch p.k {
		case pkCmp, pkCmpImm:
			cr |= 1
		case pkCall:
			lr, ctr, carry, fpscr = true, true, true, true
			cr |= 1<<0 | 1<<1 | 1<<5 | 1<<6 | 1<<7
			goCall = goCall || f.hasGoCall
		case pkSyscall:
			cr |= 1
			carry = true
		case pkOp, pkOpImm:
			switch o := ir.Opcode(p.sub); o {
			case ir.OpAnd:
				if p.k == pkOpImm {
					cr |= 1
				}
			case ir.OpSelect, ir.OpDiv, ir.OpDivSigned, ir.OpRem, ir.OpRemSigned,
				ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy:
				cr |= 1
			case ir.OpShiftRightArithmetic:
				carry = carry || p.w == 64 && p.imm != 0
			case ir.OpCarryAdd, ir.OpBorrowSub, ir.OpAddWithCarry, ir.OpSubWithBorrow, ir.OpTrailingZeros:
				carry = true
			case ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv, ir.OpFMA, ir.OpFSqrt, ir.OpFConvert, ir.OpFProductError,
				ir.OpFFromSigned, ir.OpFFromUnsigned:
				fpscr = true
			case ir.OpFToSigned, ir.OpFToUnsigned, ir.OpFCompare:
				fpscr = true
				cr |= 1
			}
		}
	}
	if goCall {
		cr |= 1<<2 | 1<<3 | 1<<4
	}
	var special []string
	if lr {
		special = append(special, "LR")
	}
	if ctr {
		special = append(special, "CTR")
	}
	for f := range 8 {
		if cr&(1<<f) != 0 {
			special = append(special, "CR"+strconv.Itoa(f))
		}
	}
	if carry {
		special = append(special, "XER")
	}
	if fpscr {
		special = append(special, "FPSCR")
	}
	r.ClobberedSpecialRegisters = special
	r.FlagsClobbered = cr != 0 || carry
}

// Concrete instruction helpers. Registers are engine registers; FP
// registers are numbered from fpBase.

func gr(r int8) backend.Register { return backend.Register(r) }
func fr(r int8) backend.Register { return backend.Register(r - fpBase) }

type ppcI = targetppc64.Instruction

func ppcMove(o *ppcOut, d, s int8) {
	if d == s {
		return
	}
	switch {
	case d < fpBase && s < fpBase:
		o.emit(ppcI{Op: targetppc64.Or, A: gr(d), B: gr(s), C: gr(s)})
	case d >= fpBase && s >= fpBase:
		o.emit(ppcI{Op: targetppc64.Fmr, A: fr(d), B: fr(s)})
	case d >= fpBase:
		o.emit(ppcI{Op: targetppc64.Mtvsrd, A: fr(d), B: gr(s)})
	default:
		o.emit(ppcI{Op: targetppc64.Mfvsrd, A: gr(d), B: fr(s)})
	}
}

// ppcNarrow clears the bits of s above w into d.
func ppcNarrow(o *ppcOut, d, s backend.Register, w uint8) {
	if w >= 64 {
		if d != s {
			o.emit(ppcI{Op: targetppc64.Or, A: d, B: s, C: s})
		}
		return
	}
	o.emit(ppcI{Op: targetppc64.Rldicl, A: d, B: s, Mask: 64 - w})
}

// ppcConstant materializes v in general register d; d may be R0.
func ppcConstant(o *ppcOut, d backend.Register, v uint64) {
	s := int64(v)
	lo, mid := int64(uint16(v)), int64(uint16(v>>16))
	switch {
	case s >= -32768 && s <= 32767:
		o.emit(ppcI{Op: targetppc64.Addi, A: d, Imm: s})
		return
	case s >= -1<<31 && s < 1<<31 || v>>32 == 0:
		o.emit(ppcI{Op: targetppc64.Addis, A: d, Imm: int64(int16(v >> 16))})
		if lo != 0 {
			o.emit(ppcI{Op: targetppc64.Ori, A: d, B: d, Imm: lo})
		}
		if s >= 1<<31 {
			o.emit(ppcI{Op: targetppc64.Rldicl, A: d, B: d, Mask: 32})
		}
		return
	}
	hi := uint32(v >> 32)
	if h := int64(int32(hi)); h >= -32768 && h <= 32767 {
		o.emit(ppcI{Op: targetppc64.Addi, A: d, Imm: h})
	} else {
		o.emit(ppcI{Op: targetppc64.Addis, A: d, Imm: int64(int16(hi >> 16))})
		if uint16(hi) != 0 {
			o.emit(ppcI{Op: targetppc64.Ori, A: d, B: d, Imm: int64(uint16(hi))})
		}
	}
	o.emit(ppcI{Op: targetppc64.Rldicr, A: d, B: d, Shift: 32, Mask: 31})
	if mid != 0 {
		o.emit(ppcI{Op: targetppc64.Oris, A: d, B: d, Imm: mid})
	}
	if lo != 0 {
		o.emit(ppcI{Op: targetppc64.Ori, A: d, B: d, Imm: lo})
	}
}

// ppcSlot returns the base register and displacement of spill slot s.
func ppcSlot(e *baselineEngine, s uint64) (backend.Register, int64) {
	if e.options.Stack == abi.StackPlatform {
		return 1, ppc64SpillOffset + int64(s)*8
	}
	return 29, int64(s) * 8
}

// ppcSingleIn converts the binary32 bits in general register s to double
// format in FPR f. The non-signaling conversion keeps a signaling NaN.
func ppcSingleIn(o *ppcOut, f backend.Register, s int8) {
	o.emit(ppcI{Op: targetppc64.Mtvsrd, A: f, B: gr(s)})
	o.emit(ppcI{Op: targetppc64.Xxsldwi, A: f, B: f, C: f, Shift: 1})
	o.emit(ppcI{Op: targetppc64.Xscvspdpn, A: f, B: f})
}

// ppcSingleOut converts double-format FPR f to binary32 bits, zero-extended
// in general register d, through VSR0. XSCVDPSPN leaves the bits in word 0,
// which POWER8 must rotate to word 1 for MFVSRWZ.
func ppcSingleOut(o *ppcOut, power9 bool, d int8, f backend.Register) {
	o.emit(ppcI{Op: targetppc64.Xscvdpspn, A: 0, B: f})
	if !power9 {
		o.emit(ppcI{Op: targetppc64.Xxsldwi, A: 0, B: 0, C: 0, Shift: 3})
	}
	o.emit(ppcI{Op: targetppc64.Mfvsrwz, A: gr(d), B: 0})
}

// ppcBranchBits are bc's BO and BI for a condition on CR0.
func ppcBranchBits(c op.BranchCondition) (bo, bi uint8) {
	switch c {
	case op.BranchEqual:
		return 12, 2
	case op.BranchNotEqual:
		return 4, 2
	case op.BranchLess, op.BranchBelow:
		return 12, 0
	case op.BranchGreaterEqual, op.BranchAboveEqual:
		return 4, 0
	case op.BranchLessEqual, op.BranchBelowEqual:
		return 4, 1
	default: // BranchGreater, BranchAbove
		return 12, 1
	}
}

func ppcUnsigned(c op.BranchCondition) bool { return c >= op.BranchBelow }

// ppcFloatBits gives, per op.FloatCondition, the CR0 bits after fcmpu (0 LT,
// 1 GT, 2 EQ, 3 FU) whose OR, complemented when not is set, is the
// condition, as targetppc64 computes OpFloatCompare.
var ppcFloatBits = [...]struct {
	a, b uint8
	not  bool
}{
	op.FloatEqual:                 {2, 2, false},
	op.FloatNotEqual:              {2, 2, true},
	op.FloatLess:                  {0, 0, false},
	op.FloatUnorderedGreaterEqual: {0, 0, true},
	op.FloatLessEqual:             {0, 2, false},
	op.FloatUnorderedGreater:      {0, 2, true},
	op.FloatGreater:               {1, 1, false},
	op.FloatUnorderedLessEqual:    {1, 1, true},
	op.FloatGreaterEqual:          {1, 2, false},
	op.FloatUnorderedLess:         {1, 2, true},
	op.FloatOrdered:               {3, 3, true},
	op.FloatUnordered:             {3, 3, false},
	op.FloatOrderedNotEqual:       {0, 1, false},
	op.FloatUnorderedEqual:        {0, 1, true},
}

// X-form words for the count-zeros instructions, which the target's
// Instruction does not describe: cntlzd, cntlzw, and POWER9's cnttzd and
// cnttzw.
func ppcCountWord(xo uint32, ra, rs backend.Register) uint32 {
	return 31<<26 | uint32(rs)<<21 | uint32(ra)<<16 | xo<<1
}

const (
	ppcXOCntlzd = 58
	ppcXOCntlzw = 26
	ppcXOCnttzd = 570
	ppcXOCnttzw = 538
)

func ppc64EncodeOne(e *baselineEngine, o *ppcOut, ps []bPhys, i int, pc int, labels []int32) error {
	p := &ps[i]
	switch p.k {
	case pkLabel:
	case pkMove:
		ppcMove(o, p.a, p.b)
	case pkConst:
		if p.a >= fpBase {
			ppcConstant(o, gr(ppcTransit), p.imm)
			ppcMove(o, p.a, ppcTransit)
		} else {
			ppcConstant(o, gr(p.a), p.imm)
		}
	case pkSymbol:
		d := p.a
		if d >= fpBase {
			d = ppcTransit
		}
		g := gr(d)
		o.emit(ppcI{Op: targetppc64.Addis, A: g, Symbol: e.symbols[p.imm]})
		o.emit(ppcI{Op: targetppc64.Ori, A: g, B: g})
		o.emit(ppcI{Op: targetppc64.Rldicr, A: g, B: g, Shift: 32, Mask: 31})
		o.emit(ppcI{Op: targetppc64.Oris, A: g, B: g})
		o.emit(ppcI{Op: targetppc64.Ori, A: g, B: g})
		ppcMove(o, p.a, d)
	case pkSpill, pkReload:
		base, off := ppcSlot(e, p.imm)
		var opc targetppc64.Opcode
		reg := gr(p.a)
		switch {
		case p.a >= fpBase && p.k == pkSpill:
			opc, reg = targetppc64.Stfd, fr(p.a)
		case p.a >= fpBase:
			opc, reg = targetppc64.Lfd, fr(p.a)
		case p.k == pkSpill:
			opc = targetppc64.Std
		default:
			opc = targetppc64.Ld
		}
		o.emit(ppcI{Op: opc, A: reg, B: base, Imm: off})
	case pkOp, pkOpImm:
		return ppc64EncodeOp(e, o, p)
	case pkCmp, pkCmpImm:
		// CR0 is compared signed or unsigned as the branch that reads it
		// needs; the engine emits the branch right after its compare.
		unsigned := false
		if i+1 < len(ps) && (ps[i+1].k == pkJumpIf || ps[i+1].k == pkJumpIfLong) {
			unsigned = ppcUnsigned(op.BranchCondition(ps[i+1].sub))
		}
		b := gr(p.b)
		switch {
		case p.k == pkCmp:
			opc := targetppc64.Cmp
			if unsigned {
				opc = targetppc64.Cmpl
			}
			o.emit(ppcI{Op: opc, B: b, C: gr(p.c), Bits: 64})
		case int64(p.imm) >= 0:
			opc := targetppc64.Cmpi
			if unsigned {
				opc = targetppc64.Cmpli
			}
			o.emit(ppcI{Op: opc, B: b, Imm: int64(p.imm), Bits: 64})
		default:
			opc := targetppc64.Cmp
			if unsigned {
				opc = targetppc64.Cmpl
			}
			o.emit(ppcI{Op: targetppc64.Addi, A: gr(ppcCycle), Imm: int64(p.imm)})
			o.emit(ppcI{Op: opc, B: b, C: gr(ppcCycle), Bits: 64})
		}
	case pkJump:
		target := pc
		if labels != nil {
			target = int(labels[p.imm])
		}
		if d := target - pc; d < -(1<<25) || d > 1<<25-4 {
			return &LimitError{Kind: LimitBranchRange, Needed: d / 4, Limit: 1 << 23, Arch: "ppc64le", Detail: "baseline branch"}
		}
		o.emit(ppcI{Op: targetppc64.B, Imm: int64(target - pc)})
	case pkJumpIf, pkJumpIfLong:
		target := pc
		if labels != nil {
			target = int(labels[p.imm])
		}
		cond := op.BranchCondition(p.sub)
		if p.k == pkJumpIf {
			bo, bi := ppcBranchBits(cond)
			o.emit(ppcI{Op: targetppc64.Bc, BO: bo, BI: bi, Imm: int64(target - pc)})
			return nil
		}
		if d := target - pc - 4; d < -(1<<25) || d > 1<<25-4 {
			return &LimitError{Kind: LimitBranchRange, Needed: d / 4, Limit: 1 << 23, Arch: "ppc64le", Detail: "baseline branch"}
		}
		bo, bi := ppcBranchBits(cond ^ 1)
		o.emit(ppcI{Op: targetppc64.Bc, BO: bo, BI: bi, Imm: 8})
		o.emit(ppcI{Op: targetppc64.B, Imm: int64(target - pc - 4)})
	case pkCall:
		// One sequence serves C and Go callees: the TOC is saved and
		// restored around the call, and R0 is zero on both sides of it.
		o.emit(ppcI{Op: targetppc64.Std, A: 2, B: 1, Imm: 24})
		o.emit(ppcI{Op: targetppc64.Xor, A: 0, B: 0, C: 0})
		o.emit(ppcI{Op: targetppc64.Mtctr, A: gr(ppcTransit)})
		o.emit(ppcI{Op: targetppc64.Bctrl})
		o.emit(ppcI{Op: targetppc64.Ld, A: 2, B: 1, Imm: 24})
		o.emit(ppcI{Op: targetppc64.Xor, A: 0, B: 0, C: 0})
	case pkSyscall:
		// Linux returns a positive errno with CR0.SO set; negate it as the
		// other targets report errors.
		o.emit(ppcI{Op: targetppc64.Sc})
		o.emit(ppcI{Op: targetppc64.Bc, BO: 4, BI: 3, Imm: 8})
		o.emit(ppcI{Op: targetppc64.Neg, A: 3, B: 3})
		o.emit(ppcI{Op: targetppc64.Xor, A: 0, B: 0, C: 0})
	case pkFrameEnter:
		o.expand(e, machine.Instruction{Op: machine.OpFrameEnter, Imm: p.imm})
	case pkFrameLeave:
		o.expand(e, machine.Instruction{Op: machine.OpFrameLeave, Imm: p.imm})
	case pkRet:
		o.emit(ppcI{Op: targetppc64.Blr})
	default:
		return fmt.Errorf("baseline ppc64le: physical kind %d: %w", p.k, ErrInvalidFunction)
	}
	return nil
}

// ppc64EncodeOp encodes one IR operation with its registers assigned. Values
// narrower than 64 bits stay zero-extended, so results that may carry out
// are narrowed again. Every expansion reads its operands before it writes
// the destination, which may alias any of them; R11 and R12 hold
// intermediates.
func ppc64EncodeOp(e *baselineEngine, o *ppcOut, p *bPhys) error {
	d, s0, s1, s2 := gr(p.a), gr(p.b), gr(p.c), gr(p.d)
	w := p.w
	opc := ir.Opcode(p.sub)
	const r11, r12 = backend.Register(11), backend.Register(12)
	emit := o.emit
	switch opc {
	case ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor:
		if p.k == pkOpImm {
			imm := int64(p.imm)
			switch opc {
			case ir.OpAdd:
				emit(ppcI{Op: targetppc64.Addi, A: d, B: s0, Imm: imm})
			case ir.OpSub:
				emit(ppcI{Op: targetppc64.Addi, A: d, B: s0, Imm: -imm})
			case ir.OpAnd:
				emit(ppcI{Op: targetppc64.Andi, A: d, B: s0, Imm: imm})
			case ir.OpOr:
				emit(ppcI{Op: targetppc64.Ori, A: d, B: s0, Imm: imm})
			case ir.OpXor:
				emit(ppcI{Op: targetppc64.Xori, A: d, B: s0, Imm: imm})
			default:
				return fmt.Errorf("baseline ppc64le: %v immediate: %w", opc, ErrInvalidFunction)
			}
		} else {
			var native targetppc64.Opcode
			switch opc {
			case ir.OpAdd:
				native = targetppc64.Add
			case ir.OpSub:
				native = targetppc64.Subf
			case ir.OpMul:
				native = targetppc64.Mulld
			case ir.OpAnd:
				native = targetppc64.And
			case ir.OpOr:
				native = targetppc64.Or
			default:
				native = targetppc64.Xor
			}
			emit(ppcI{Op: native, A: d, B: s0, C: s1})
		}
		if w < 64 && (opc == ir.OpAdd || opc == ir.OpSub || opc == ir.OpMul) {
			ppcNarrow(o, d, d, w)
		}
	case ir.OpAddScaled:
		scale := p.imm
		switch {
		case scale == 0:
			ppcNarrow(o, d, s0, 64)
			return nil
		case scale == 1:
			emit(ppcI{Op: targetppc64.Add, A: d, B: s0, C: s1})
		case scale&(scale-1) == 0:
			k := uint8(bits.TrailingZeros64(scale))
			emit(ppcI{Op: targetppc64.Rldicr, A: r11, B: s1, Shift: k, Mask: 63 - k})
			emit(ppcI{Op: targetppc64.Add, A: d, B: s0, C: r11})
		default:
			ppcConstant(o, r11, scale)
			emit(ppcI{Op: targetppc64.Mulld, A: r11, B: s1, C: r11})
			emit(ppcI{Op: targetppc64.Add, A: d, B: s0, C: r11})
		}
		if w < 64 {
			ppcNarrow(o, d, d, w)
		}
	case ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic:
		k := uint8(p.imm)
		if k == 0 {
			ppcNarrow(o, d, s0, 64)
			return nil
		}
		switch {
		case opc == ir.OpShiftLeft && w == 64:
			emit(ppcI{Op: targetppc64.Rldicr, A: d, B: s0, Shift: k, Mask: 63 - k})
		case opc == ir.OpShiftLeft:
			// Rotating the low word and keeping bits k..w-1 shifts and
			// narrows at once.
			emit(ppcI{Op: targetppc64.Rlwinm, A: d, B: s0, Shift: k, Mask: 32 - w, MaskEnd: 31 - k})
		case opc == ir.OpShiftRight:
			emit(ppcI{Op: targetppc64.Rldicl, A: d, B: s0, Shift: 64 - k, Mask: k})
		case w == 64:
			emit(ppcI{Op: targetppc64.Sradi, A: d, B: s0, Shift: k})
		default:
			// Bits k..k+w-1 of the sign-extended value are the result.
			ppcSignExtend(o, r11, s0, w)
			emit(ppcI{Op: targetppc64.Rldicl, A: d, B: r11, Shift: 64 - k, Mask: 64 - w})
		}
	case ir.OpRotateLeft, ir.OpRotateRight:
		k := uint8(p.imm) % w
		if opc == ir.OpRotateRight {
			k = (w - k) % w
		}
		switch {
		case k == 0:
			ppcNarrow(o, d, s0, 64)
		case w == 64:
			emit(ppcI{Op: targetppc64.Rldicl, A: d, B: s0, Shift: k})
		case w == 32:
			emit(ppcI{Op: targetppc64.Rlwinm, A: d, B: s0, Shift: k, MaskEnd: 31})
		default:
			emit(ppcI{Op: targetppc64.Rldicr, A: r11, B: s0, Shift: k, Mask: 63 - k})
			emit(ppcI{Op: targetppc64.Rldicl, A: r12, B: s0, Shift: 64 - (w - k), Mask: w - k})
			emit(ppcI{Op: targetppc64.Or, A: d, B: r11, C: r12})
			ppcNarrow(o, d, d, w)
		}
	case ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy:
		o.expand(e, machine.Instruction{Op: machine.OpShiftBy, Arithmetic: ppcShiftBy[opc], Bits: w, A: d, B: s0, C: s1})
	case ir.OpCastUnsigned:
		if w >= uint8(p.d) {
			ppcNarrow(o, d, s0, 64)
		} else {
			ppcNarrow(o, d, s0, w)
		}
	case ir.OpSignExtend:
		ppcSignExtend(o, d, s0, uint8(p.imm))
		if w < 64 {
			ppcNarrow(o, d, d, w)
		}
	case ir.OpLeadingZeros:
		switch w {
		case 64:
			o.word(ppcCountWord(ppcXOCntlzd, d, s0))
		default:
			o.word(ppcCountWord(ppcXOCntlzw, d, s0))
			if w < 32 {
				emit(ppcI{Op: targetppc64.Addi, A: d, B: d, Imm: -int64(32 - w)})
			}
		}
	case ir.OpTrailingZeros:
		// A bit at position w bounds the count of a zero value.
		x := s0
		switch w {
		case 8:
			emit(ppcI{Op: targetppc64.Ori, A: r12, B: s0, Imm: 0x100})
			x = r12
		case 16:
			emit(ppcI{Op: targetppc64.Oris, A: r12, B: s0, Imm: 1})
			x = r12
		}
		if ppcLevel(e.options) >= abi.PPC64Power9 {
			if w == 64 {
				o.word(ppcCountWord(ppcXOCnttzd, d, x))
			} else {
				o.word(ppcCountWord(ppcXOCnttzw, d, x))
			}
			return nil
		}
		if w == 32 {
			emit(ppcI{Op: targetppc64.Addi, A: r12, Imm: 1})
			emit(ppcI{Op: targetppc64.Rldicr, A: r12, B: r12, Shift: 32, Mask: 31})
			emit(ppcI{Op: targetppc64.Or, A: r12, B: s0, C: r12})
			x = r12
		}
		// (x-1) ^ (x & (x-1)) has exactly the trailing-zero bits of x set.
		emit(ppcI{Op: targetppc64.Addi, A: r11, B: x, Imm: -1})
		emit(ppcI{Op: targetppc64.And, A: r12, B: x, C: r11})
		emit(ppcI{Op: targetppc64.Xor, A: r11, B: r11, C: r12})
		o.word(ppcCountWord(ppcXOCntlzd, r11, r11))
		emit(ppcI{Op: targetppc64.Subfic, A: d, B: r11, Imm: 64})
	case ir.OpMulHigh:
		switch w {
		case 64:
			emit(ppcI{Op: targetppc64.Mulhdu, A: d, B: s0, C: s1})
		case 32:
			// The upper word of mulhwu is undefined in 64-bit mode.
			emit(ppcI{Op: targetppc64.Mulhwu, A: d, B: s0, C: s1})
			ppcNarrow(o, d, d, 32)
		default:
			emit(ppcI{Op: targetppc64.Mulld, A: d, B: s0, C: s1})
			emit(ppcI{Op: targetppc64.Rldicl, A: d, B: d, Shift: 64 - w, Mask: w})
		}
	case ir.OpMulHighSigned:
		switch w {
		case 64:
			emit(ppcI{Op: targetppc64.Mulhd, A: d, B: s0, C: s1})
		case 32:
			emit(ppcI{Op: targetppc64.Mulhw, A: d, B: s0, C: s1})
			ppcNarrow(o, d, d, 32)
		default:
			ppcSignExtend(o, r11, s0, w)
			ppcSignExtend(o, r12, s1, w)
			emit(ppcI{Op: targetppc64.Mulld, A: d, B: r11, C: r12})
			emit(ppcI{Op: targetppc64.Rldicl, A: d, B: d, Shift: 64 - w, Mask: 64 - w})
		}
	case ir.OpCarryAdd:
		if w == 64 {
			emit(ppcI{Op: targetppc64.Addc, A: r12, B: s0, C: s1})
			emit(ppcI{Op: targetppc64.Addi, A: r11})
			emit(ppcI{Op: targetppc64.Addze, A: d, B: r11})
		} else {
			emit(ppcI{Op: targetppc64.Add, A: r12, B: s0, C: s1})
			emit(ppcI{Op: targetppc64.Rldicl, A: d, B: r12, Shift: 64 - w, Mask: w})
		}
	case ir.OpBorrowSub:
		// Zero-extended operands borrow at any width exactly when they do
		// in 64 bits. CA is the complement of the borrow.
		emit(ppcI{Op: targetppc64.Subfc, A: r12, B: s0, C: s1})
		emit(ppcI{Op: targetppc64.Addi, A: r11})
		emit(ppcI{Op: targetppc64.Subfze, A: d, B: r11})
		emit(ppcI{Op: targetppc64.Neg, A: d, B: d})
	case ir.OpAddWithCarry, ir.OpSubWithBorrow:
		emit(ppcI{Op: targetppc64.Rldicl, A: r11, B: s2, Mask: 63})
		if opc == ir.OpAddWithCarry {
			emit(ppcI{Op: targetppc64.Addic, A: r11, B: r11, Imm: -1})
			emit(ppcI{Op: targetppc64.Adde, A: d, B: s0, C: s1})
		} else {
			emit(ppcI{Op: targetppc64.Subfic, A: r11, B: r11})
			emit(ppcI{Op: targetppc64.Subfe, A: d, B: s0, C: s1})
		}
		if w < 64 {
			ppcNarrow(o, d, d, w)
		}
	case ir.OpSelect:
		emit(ppcI{Op: targetppc64.Cmpli, B: s0, Bits: 64})
		emit(ppcI{Op: targetppc64.Isel, A: d, B: s2, C: s1, BI: 2})
	case ir.OpDiv, ir.OpDivSigned, ir.OpRem, ir.OpRemSigned:
		o.expand(e, machine.Instruction{Op: machine.OpDivide, Arithmetic: opc, Bits: w, A: d, B: s0, C: s1, Imm: p.imm})
	case ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv, ir.OpFMA, ir.OpFSqrt, ir.OpFProductError:
		var native targetppc64.Opcode
		switch opc {
		case ir.OpFAdd:
			native = targetppc64.Fadd
		case ir.OpFSub:
			native = targetppc64.Fsub
		case ir.OpFMul:
			native = targetppc64.Fmul
		case ir.OpFDiv:
			native = targetppc64.Fdiv
		case ir.OpFSqrt:
			native = targetppc64.Fsqrt
		default:
			native = targetppc64.Fmadd
		}
		if w == 64 {
			ins := ppcI{Op: native, A: fr(p.a), B: fr(p.b), C: fr(p.c), D: fr(p.d), Bits: 64}
			switch opc {
			case ir.OpFSqrt:
				ins.C, ins.D = 0, 0
			case ir.OpFProductError:
				// A*B - C as A*B + (-C).
				emit(ppcI{Op: targetppc64.Fneg, A: 0, B: fr(p.d)})
				ins.D = 0
			case ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv:
				ins.D = 0
			}
			emit(ins)
			return nil
		}
		// binary32 operands arrive as bits in general registers.
		ppcSingleIn(o, 1, p.b)
		ins := ppcI{Op: native, A: 0, B: 1, Bits: 32}
		if opc != ir.OpFSqrt {
			ppcSingleIn(o, 2, p.c)
			ins.C = 2
		}
		if opc == ir.OpFMA {
			ppcSingleIn(o, 0, p.d)
		}
		emit(ins)
		ppcSingleOut(o, ppcLevel(e.options) >= abi.PPC64Power9, p.a, 0)
	case ir.OpFConvert:
		if w == 64 {
			ppcSingleIn(o, fr(p.a), p.b)
			return nil
		}
		emit(ppcI{Op: targetppc64.Frsp, A: 0, B: fr(p.b)})
		ppcSingleOut(o, ppcLevel(e.options) >= abi.PPC64Power9, p.a, 0)
	case ir.OpFFromSigned, ir.OpFFromUnsigned:
		move, convert := targetppc64.Mtvsrd, targetppc64.Fcfid
		switch {
		case p.d == 32 && opc == ir.OpFFromSigned:
			move = targetppc64.Mtvsrwa
		case p.d == 32:
			// A zero-extended word converts exactly as signed.
			move = targetppc64.Mtvsrwz
		case opc == ir.OpFFromUnsigned:
			convert = targetppc64.Fcfidu
		}
		f := backend.Register(0)
		if w == 64 {
			f = fr(p.a)
		}
		emit(ppcI{Op: move, A: f, B: s0})
		emit(ppcI{Op: convert, A: f, B: f, Bits: w})
		if w == 32 {
			ppcSingleOut(o, ppcLevel(e.options) >= abi.PPC64Power9, p.a, 0)
		}
	case ir.OpFToSigned, ir.OpFToUnsigned:
		f := backend.Register(1)
		if p.d == 64 {
			f = fr(p.b)
		} else {
			ppcSingleIn(o, 1, p.b)
		}
		signed := opc == ir.OpFToSigned
		convert := targetppc64.Fctiwuz
		switch {
		case signed && w == 64:
			convert = targetppc64.Fctidz
		case w == 64:
			convert = targetppc64.Fctiduz
		case signed:
			convert = targetppc64.Fctiwz
		}
		// The conversions saturate; the signed ones give MIN for NaN,
		// which saturating semantics replace with 0.
		guard := signed && op.ConversionSemantics(p.imm) == op.ConversionSaturate
		if guard {
			emit(ppcI{Op: targetppc64.Fcmpu, B: f, C: f})
		}
		emit(ppcI{Op: convert, A: 0, B: f})
		if w == 64 {
			emit(ppcI{Op: targetppc64.Mfvsrd, A: d, B: 0})
		} else {
			emit(ppcI{Op: targetppc64.Mfvsrwz, A: d, B: 0})
		}
		if guard {
			emit(ppcI{Op: targetppc64.Isel, A: d, B: 0, C: d, BI: 3})
		}
	case ir.OpFCompare:
		if p.imm >= uint64(len(ppcFloatBits)) {
			return fmt.Errorf("baseline ppc64le: float condition %d: %w", p.imm, ErrInvalidFunction)
		}
		fa, fb := backend.Register(1), backend.Register(2)
		if p.d == 64 {
			fa, fb = fr(p.b), fr(p.c)
		} else {
			ppcSingleIn(o, 1, p.b)
			ppcSingleIn(o, 2, p.c)
		}
		c := ppcFloatBits[p.imm]
		emit(ppcI{Op: targetppc64.Fcmpu, B: fa, C: fb})
		if c.not {
			emit(ppcI{Op: targetppc64.Crnor, A: backend.Register(c.a), B: backend.Register(c.a), C: backend.Register(c.b)})
		} else if c.a != c.b {
			emit(ppcI{Op: targetppc64.Cror, A: backend.Register(c.a), B: backend.Register(c.a), C: backend.Register(c.b)})
		}
		emit(ppcI{Op: targetppc64.Mfcr, A: d})
		emit(ppcI{Op: targetppc64.Rlwinm, A: d, B: d, Shift: c.a + 1, Mask: 31, MaskEnd: 31})
	case ir.OpLoad64:
		emit(ppcI{Op: targetppc64.Ld, A: d, B: s0})
	case ir.OpLoad8:
		emit(ppcI{Op: targetppc64.Lbz, A: d, B: s0})
	case ir.OpStore64:
		emit(ppcI{Op: targetppc64.Std, A: s1, B: s0})
	case ir.OpStore8:
		emit(ppcI{Op: targetppc64.Stb, A: s1, B: s0})
	case ir.OpLoad16, ir.OpLoad32:
		n := int64(2)
		if opc == ir.OpLoad32 {
			n = 4
		}
		if op.ByteOrder(p.imm) == op.ByteOrderBigEndian {
			// Bytes in address order, most significant first.
			emit(ppcI{Op: targetppc64.Lbz, A: r12, B: s0})
			for k := int64(1); k < n; k++ {
				emit(ppcI{Op: targetppc64.Lbz, A: r11, B: s0, Imm: k})
				emit(ppcI{Op: targetppc64.Rldicr, A: r12, B: r12, Shift: 8, Mask: 55})
				emit(ppcI{Op: targetppc64.Or, A: r12, B: r12, C: r11})
			}
			ppcNarrow(o, d, r12, 64)
			return nil
		}
		native := targetppc64.Lhz
		if n == 4 {
			native = targetppc64.Lwz
		}
		emit(ppcI{Op: native, A: d, B: s0})
	case ir.OpStore16, ir.OpStore32:
		n := uint8(2)
		if opc == ir.OpStore32 {
			n = 4
		}
		if op.ByteOrder(p.imm) == op.ByteOrderBigEndian {
			for k := uint8(0); k < n; k++ {
				if shift := 8 * (n - 1 - k); shift != 0 {
					emit(ppcI{Op: targetppc64.Rldicl, A: r11, B: s1, Shift: 64 - shift, Mask: shift})
					emit(ppcI{Op: targetppc64.Stb, A: r11, B: s0, Imm: int64(k)})
				} else {
					emit(ppcI{Op: targetppc64.Stb, A: s1, B: s0, Imm: int64(k)})
				}
			}
			return nil
		}
		native := targetppc64.Sth
		if n == 4 {
			native = targetppc64.Stw
		}
		emit(ppcI{Op: native, A: s1, B: s0})
	default:
		return fmt.Errorf("baseline ppc64le: %v: %w", opc, ErrUnsupported)
	}
	return nil
}

var ppcShiftBy = [256]op.Opcode{
	ir.OpShiftLeftBy: op.OpShiftLeft, ir.OpShiftRightBy: op.OpShiftRight, ir.OpShiftRightArithmeticBy: op.OpShiftRightArithmetic,
	ir.OpRotateLeftBy: op.OpRotateLeft, ir.OpRotateRightBy: op.OpRotateRight,
}

// ppcSignExtend sign-extends the low from bits of s into d.
func ppcSignExtend(o *ppcOut, d, s backend.Register, from uint8) {
	switch from {
	case 8:
		o.emit(ppcI{Op: targetppc64.Extsb, A: d, B: s})
	case 16:
		o.emit(ppcI{Op: targetppc64.Extsh, A: d, B: s})
	case 32:
		o.emit(ppcI{Op: targetppc64.Extsw, A: d, B: s})
	default:
		ppcNarrow(o, d, s, 64)
	}
}
