package i386

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// CanAtomic reports whether the target encodes atomic operation at width.
func (Policy) CanAtomic(operation op.Opcode, width uint8) bool {
	return operation >= op.OpAtomicLoad && operation <= op.OpAtomicCompareSwap &&
		(width == 8 || width == 16 || width == 32 || width == 64)
}

// Locked instructions, XCHG with memory included, are full barriers on x86,
// so every form below is sequentially consistent. Up to 32 bits the
// expansions go through EDX, whose DL view exists for any operand, and
// CMPXCHG's fixed EAX (atomicTemporaries). A 64-bit access reads its
// operands from slots and uses CMPXCHG8B, which fixes EDX:EAX and ECX:EBX,
// or an SSE2 MOVQ, which is atomic for an 8-aligned address.

const (
	eax backend.Register = 0
	ecx backend.Register = 1
	edx backend.Register = 2
	ebx backend.Register = 3
	esi backend.Register = 6
)

// atomicTemporaries lists the fixed registers a 32-bit or narrower OpAtomic
// expansion writes besides A. The compiler keeps A and the operands out of
// them. The 64-bit form clobbers atomic64Clobbers instead.
func atomicTemporaries(operation op.Opcode) []backend.Register {
	switch operation {
	case op.OpAtomicStore, op.OpAtomicAdd, op.OpAtomicSwap:
		return []backend.Register{edx}
	case op.OpAtomicAnd, op.OpAtomicOr, op.OpAtomicXor, op.OpAtomicCompareSwap:
		return []backend.Register{eax, edx}
	}
	return nil
}

// atomic64Clobbers are the GP registers a 64-bit OpAtomic writes. Loads and
// stores also write XMM0.
var atomic64Clobbers = []backend.Register{eax, ecx, edx, ebx, esi}

// validAtomic checks an OpAtomic. Up to 32 bits: GP operands and none in a
// temporary; every form writes A only in its last instruction, so A may
// equal an input. At 64 bits: A=EAX and HighA=EDX (unless a store) and a
// slot base; B, C and D are not read.
func validAtomic(ins machine.Instruction) bool {
	if ins.Op != machine.OpAtomic || ins.Arithmetic < op.OpAtomicLoad || ins.Arithmetic > op.OpAtomicCompareSwap ||
		ins.Imm > 0xff || !op.MemoryOrder(ins.Imm).Valid() || ins.ByteOrder != op.ByteOrderNative {
		return false
	}
	switch ins.Bits {
	case 8, 16, 32:
	case 64:
		return ins.Slot >= 0 && ins.Slot <= 1<<24 && (ins.Arithmetic == op.OpAtomicStore || ins.A == eax && ins.HighA == edx)
	default:
		return false
	}
	inputs := []backend.Register{ins.B, ins.C, ins.D}
	switch ins.Arithmetic {
	case op.OpAtomicLoad:
		inputs = inputs[:1]
	case op.OpAtomicCompareSwap:
	default:
		inputs = inputs[:2]
	}
	used := inputs
	if ins.Arithmetic != op.OpAtomicStore {
		used = append([]backend.Register{ins.A}, inputs...)
	}
	for _, r := range used {
		if r >= 8 {
			return false
		}
		for _, t := range atomicTemporaries(ins.Arithmetic) {
			if r == t {
				return false
			}
		}
	}
	return true
}

// atomicMemory appends `opcode` with a ModRM naming [base] and reg.
func atomicMemory(code []byte, reg, base backend.Register, opcode ...byte) []byte {
	code = append(code, opcode...)
	mode := byte(0)
	if base == 5 {
		mode = 1
	}
	code = append(code, modRM(mode, byte(reg), byte(base)))
	if base == 4 {
		code = append(code, 0x24)
	}
	if base == 5 {
		code = append(code, 0)
	}
	return code
}

var atomicPointers = map[uint8]string{8: "byte", 16: "word", 32: "dword", 64: "qword"}

func atomicRegisterName(r backend.Register, w uint8) string {
	switch w {
	case 8:
		return [...]string{"AL", "CL", "DL", "BL"}[r]
	case 16:
		return name16(r)
	}
	return name(r)
}

// atomicPlan expands a valid OpAtomic (see FORMS.md).
func atomicPlan(ins machine.Instruction, frame slotFrame, text bool) []expansionStep {
	x := &expansion{text: text, frame: frame}
	if ins.Bits == 64 {
		atomic64(x, ins)
	} else {
		atomic32(x, ins)
	}
	return x.resolve()
}

func atomic32(x *expansion, ins machine.Instruction) {
	w, a, b, c, d := ins.Bits, ins.A, ins.B, ins.C, ins.D
	var size []byte
	if w == 16 {
		size = []byte{0x66}
	}
	locked := append([]byte{0xf0}, size...)
	// rm is the r/m, reg opcode of XCHG, XADD and CMPXCHG; the 8-bit form is
	// one less.
	rm := func(opcode ...byte) []byte {
		opcode = append([]byte(nil), opcode...)
		if w == 8 {
			opcode[len(opcode)-1]--
		}
		return opcode
	}
	mem := "[" + name(b) + "]"
	ptr := atomicPointers[w]
	load := func(r backend.Register) {
		switch w {
		case 8:
			x.emit(atomicMemory(nil, r, b, 0x0f, 0xb6), "movzx %s, byte ptr %s", name(r), mem)
		case 16:
			x.emit(atomicMemory(nil, r, b, 0x0f, 0xb7), "movzx %s, word ptr %s", name(r), mem)
		default:
			x.emit(atomicMemory(nil, r, b, 0x8b), "mov %s, dword ptr %s", name(r), mem)
		}
	}
	extend := func(from backend.Register) {
		switch w {
		case 8:
			x.emit([]byte{0x0f, 0xb6, modRM(3, byte(a), byte(from))}, "movzx %s, %s", name(a), atomicRegisterName(from, 8))
		case 16:
			x.emit([]byte{0x0f, 0xb7, modRM(3, byte(a), byte(from))}, "movzx %s, %s", name(a), name16(from))
		default:
			x.mov(a, from)
		}
	}
	switch ins.Arithmetic {
	case op.OpAtomicLoad:
		load(a)
	case op.OpAtomicStore:
		x.mov(edx, c)
		x.emit(atomicMemory(append([]byte(nil), size...), edx, b, rm(0x87)...), "xchg %s ptr %s, %s", ptr, mem, atomicRegisterName(edx, w))
	case op.OpAtomicAdd:
		x.mov(edx, c)
		x.emit(atomicMemory(locked, edx, b, rm(0x0f, 0xc1)...), "lock xadd %s ptr %s, %s", ptr, mem, atomicRegisterName(edx, w))
		extend(edx)
	case op.OpAtomicSwap:
		x.mov(edx, c)
		x.emit(atomicMemory(append([]byte(nil), size...), edx, b, rm(0x87)...), "xchg %s ptr %s, %s", ptr, mem, atomicRegisterName(edx, w))
		extend(edx)
	case op.OpAtomicAnd, op.OpAtomicOr, op.OpAtomicXor:
		opcode := map[op.Opcode]byte{op.OpAtomicAnd: aluAnd, op.OpAtomicOr: aluOr, op.OpAtomicXor: aluXor}[ins.Arithmetic]
		load(eax)
		retry := x.label()
		x.bind(retry)
		x.mov(edx, eax)
		x.alu(opcode, edx, c)
		x.emit(atomicMemory(locked, edx, b, rm(0x0f, 0xb1)...), "lock cmpxchg %s ptr %s, %s", ptr, mem, atomicRegisterName(edx, w))
		x.branch(ccNotEqual, retry)
		extend(eax)
	case op.OpAtomicCompareSwap:
		x.mov(eax, c)
		x.mov(edx, d)
		x.emit(atomicMemory(locked, edx, b, rm(0x0f, 0xb1)...), "lock cmpxchg %s ptr %s, %s", ptr, mem, atomicRegisterName(edx, w))
		extend(eax)
	}
}

// atomic64 expands a 64-bit OpAtomic. Slot holds the address, Slot+1 and
// Slot+2 the operand (low, high) and Slot+3 and Slot+4 the CAS replacement.
// The old value is left in EDX:EAX.
func atomic64(x *expansion, ins machine.Instruction) {
	s := ins.Slot
	x.load(esi, s)
	switch ins.Arithmetic {
	case op.OpAtomicLoad:
		x.emit([]byte{0xf3, 0x0f, 0x7e, modRM(0, 0, byte(esi))}, "movq xmm0, qword ptr [ESI]")
		x.sse(0x66, 0x7e, 0, byte(eax), "movd EAX, xmm0")
		x.emit([]byte{0x66, 0x0f, 0x73, modRM(3, 2, 0), 32}, "psrlq xmm0, 32")
		x.sse(0x66, 0x7e, 0, byte(edx), "movd EDX, xmm0")
		return
	case op.OpAtomicStore:
		x.sseMemory(0xf3, 0x7e, 0, s+1, "movq")
		x.emit([]byte{0x66, 0x0f, 0xd6, modRM(0, 0, byte(esi))}, "movq qword ptr [ESI], xmm0")
		x.emit([]byte{0x0f, 0xae, 0xf0}, "mfence")
		return
	case op.OpAtomicCompareSwap:
		x.load(eax, s+1)
		x.load(edx, s+2)
		x.load(ebx, s+3)
		x.load(ecx, s+4)
		x.emit([]byte{0xf0, 0x0f, 0xc7, modRM(0, 1, byte(esi))}, "lock cmpxchg8b qword ptr [ESI]")
		return
	}
	// A torn first read only costs a retry: CMPXCHG8B reloads EDX:EAX.
	x.emit([]byte{0x8b, modRM(0, byte(eax), byte(esi))}, "mov EAX, dword ptr [ESI]")
	x.emit([]byte{0x8b, modRM(1, byte(edx), byte(esi)), 4}, "mov EDX, dword ptr [ESI + 4]")
	if ins.Arithmetic == op.OpAtomicSwap {
		x.load(ebx, s+1)
		x.load(ecx, s+2)
	}
	retry := x.label()
	x.bind(retry)
	if ins.Arithmetic != op.OpAtomicSwap {
		// `op r32, r/m32` forms; Add carries into the high word.
		forms := map[op.Opcode][2]byte{
			op.OpAtomicAdd: {0x03, 0x13}, op.OpAtomicAnd: {0x23, 0x23}, op.OpAtomicOr: {0x0b, 0x0b}, op.OpAtomicXor: {0x33, 0x33},
		}[ins.Arithmetic]
		low, high := forms[0], forms[1]
		x.mov(ebx, eax)
		x.mov(ecx, edx)
		m, text := x.memory(byte(ebx), s+1)
		x.emit(append([]byte{low}, m...), "%s EBX, dword ptr %s", aluMnemonics[low-2], text)
		m, text = x.memory(byte(ecx), s+2)
		x.emit(append([]byte{high}, m...), "%s ECX, dword ptr %s", aluMnemonics[high-2], text)
	}
	x.emit([]byte{0xf0, 0x0f, 0xc7, modRM(0, 1, byte(esi))}, "lock cmpxchg8b qword ptr [ESI]")
	x.branch(ccNotEqual, retry)
}

// atomicWritesFlags reports whether an OpAtomic expansion writes flags:
// MOV, XCHG and SSE2 moves do not.
func atomicWritesFlags(ins machine.Instruction) bool {
	switch ins.Arithmetic {
	case op.OpAtomicLoad, op.OpAtomicStore:
		return false
	case op.OpAtomicSwap:
		return ins.Bits == 64
	}
	return true
}

// Atomic implements OpAtomic.
func (e *ByteAssembler) Atomic(ins machine.Instruction) error {
	if !e.require(validAtomic(ins)) {
		return e.err
	}
	emitExpansion(e, atomicPlan(ins, e.slotFrame(), false))
	return nil
}

func (e *TextAssembler) Atomic(ins machine.Instruction) error {
	if !e.require(validAtomic(ins)) {
		return e.err
	}
	recordExpansion(e, atomicPlan(ins, e.slotFrame(), true))
	return nil
}
