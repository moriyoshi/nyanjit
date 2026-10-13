package i386

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// validDivide checks an OpDivide. Up to 32 bits it divides EDX:EAX, so the
// divisor C must be neither EAX nor EDX; B is read before either is written
// and A after both are read, so they may be any GP register. A 64-bit
// division reads its words from four slots and leaves its result in EDX:EAX.
func validDivide(ins machine.Instruction) bool {
	switch ins.Arithmetic {
	case op.OpDiv, op.OpDivSigned, op.OpRem, op.OpRemSigned:
	default:
		return false
	}
	if !op.DivisionSemantics(ins.Imm).Valid() || !validInstructionRegisters(ins) {
		return false
	}
	switch ins.Bits {
	case 8, 16, 32:
		return ins.C != 0 && ins.C != 2
	case 64:
		return ins.A == 0 && ins.HighA == 2 && ins.Slot >= 0
	}
	return false
}

// dividePlan expands OpDivide. Checked semantics test for a zero divisor
// and, when signed, for MIN / -1, which DIV and IDIV would trap on.
func dividePlan(ins machine.Instruction, frame slotFrame, text bool) []expansionStep {
	x := &expansion{text: text, frame: frame}
	if ins.Bits == 64 {
		divide64(x, ins)
	} else {
		divide32(x, ins)
	}
	return x.resolve()
}

func divide32(x *expansion, ins machine.Instruction) {
	signed := ins.Arithmetic == op.OpDivSigned || ins.Arithmetic == op.OpRemSigned
	remainder := ins.Arithmetic == op.OpRem || ins.Arithmetic == op.OpRemSigned
	semantics := op.DivisionSemantics(ins.Imm)
	checked := semantics != op.DivisionUnchecked
	mask := uint32(1)<<ins.Bits - 1
	if ins.Bits == 32 {
		mask = 0xffffffff
	}
	a, b, c := ins.A, ins.B, ins.C
	zero, overflow, end := x.label(), x.label(), x.label()
	x.mov(0, b)
	if checked {
		x.alu(aluTest, c, c)
		x.branch(ccEqual, zero)
		if signed {
			// Only a -1 divisor (zero-extended) and the Bits-wide MIN
			// dividend overflow.
			divide := x.label()
			x.immediate(groupCmp, c, mask)
			x.branch(ccNotEqual, divide)
			x.immediate(groupCmp, 0, mask>>1+1)
			x.branch(ccEqual, overflow)
			x.bind(divide)
		}
	}
	switch {
	case !signed:
		// Narrow operands are zero-extended, so a 32-bit DIV gives the
		// narrow quotient and remainder.
		x.alu(aluXor, 2, 2)
		x.unary(unaryDiv, c)
		x.mov(a, divideResult(remainder))
	case ins.Bits == 32:
		x.emit([]byte{0x99}, "cdq")
		x.unary(unaryIDiv, c)
		x.mov(a, divideResult(remainder))
	case ins.Bits == 16:
		// IDIV r/m16 reads C's low 16 bits as signed: no extension needed.
		x.emit([]byte{0x66, 0x99}, "cwd")
		x.emit([]byte{0x66, 0xf7, modRM(3, unaryIDiv, byte(c))}, "idiv %s", name16(c))
		if remainder {
			x.emit([]byte{0x0f, 0xb7, modRM(3, byte(a), 2)}, "movzx %s, DX", name(a))
		} else {
			x.emit([]byte{0x0f, 0xb7, modRM(3, byte(a), 0)}, "movzx %s, AX", name(a))
		}
	default:
		// IDIV r/m8 divides AX. C may be ESI or EDI, which have no byte
		// register, so the divisor goes through DL.
		x.emit([]byte{0x66, 0x98}, "cbw")
		x.mov(2, c)
		x.emit([]byte{0xf6, modRM(3, unaryIDiv, 2)}, "idiv DL")
		if remainder {
			x.emit([]byte{0x0f, 0xb6, modRM(3, byte(a), 4)}, "movzx %s, AH", name(a))
		} else {
			x.emit([]byte{0x0f, 0xb6, modRM(3, byte(a), 0)}, "movzx %s, AL", name(a))
		}
	}
	if !checked {
		return
	}
	x.branch(ccAlways, end)
	// EAX still holds the dividend on both special paths.
	x.bind(zero)
	switch {
	case remainder:
		x.mov(a, 0)
	case semantics == op.DivisionAllOnes:
		x.emit(append32([]byte{0xb8 | byte(a)}, mask), "mov %s, 0x%x", name(a), mask)
	default:
		x.alu(aluXor, a, a)
	}
	if signed {
		x.branch(ccAlways, end)
		x.bind(overflow)
		if remainder {
			x.alu(aluXor, a, a)
		} else {
			x.mov(a, 0) // MIN / -1 = MIN, the dividend
		}
	}
	x.bind(end)
}

// divide64 divides the pair in slots 0-1 (low, high) by the pair in slots
// 2-3 and leaves the quotient or remainder in EDX:EAX. It uses EAX, EBX,
// ECX, EDX, ESI and EDI and overwrites the slots. A signed division divides
// the magnitudes and then negates, which also gives MIN / -1 = MIN and
// MIN % -1 = 0 without a separate test.
func divide64(x *expansion, ins machine.Instruction) {
	signed := ins.Arithmetic == op.OpDivSigned || ins.Arithmetic == op.OpRemSigned
	remainder := ins.Arithmetic == op.OpRem || ins.Arithmetic == op.OpRemSigned
	semantics := op.DivisionSemantics(ins.Imm)
	checked := semantics != op.DivisionUnchecked
	const eax, ecx, edx, ebx, esi, edi = backend.Register(0), backend.Register(1), backend.Register(2), backend.Register(3), backend.Register(6), backend.Register(7)
	s := ins.Slot
	zero, large, join, end := x.label(), x.label(), x.label(), x.label()
	x.load(eax, s)
	x.load(edx, s+1)
	x.load(ebx, s+2)
	x.load(esi, s+3)
	if checked {
		x.mov(ecx, ebx)
		x.alu(aluOr, ecx, esi)
		x.branch(ccEqual, zero)
	}
	if signed {
		// Slot 3 keeps the sign of the result; slots 0-1 the magnitude
		// of the dividend, which the large case reads back.
		if remainder {
			x.store(s+3, edx)
		} else {
			x.mov(ecx, edx)
			x.alu(aluXor, ecx, esi)
			x.store(s+3, ecx)
		}
		positive := x.label()
		x.alu(aluTest, edx, edx)
		x.branch(ccNotSign, positive)
		x.negate64(eax, edx)
		x.store(s, eax)
		x.store(s+1, edx)
		x.bind(positive)
		positive = x.label()
		x.alu(aluTest, esi, esi)
		x.branch(ccNotSign, positive)
		x.negate64(ebx, esi)
		x.bind(positive)
	}
	x.alu(aluTest, esi, esi)
	x.branch(ccNotEqual, large)
	// A 32-bit divisor: two DIVs, high word first, each with a remainder
	// below the divisor in EDX, so neither overflows.
	x.mov(ecx, eax)
	x.mov(eax, edx)
	x.alu(aluXor, edx, edx)
	x.unary(unaryDiv, ebx)
	x.mov(edi, eax)
	x.mov(eax, ecx)
	x.unary(unaryDiv, ebx)
	if remainder {
		x.mov(eax, edx)
		x.alu(aluXor, edx, edx)
	} else {
		x.mov(edx, edi)
	}
	x.branch(ccAlways, join)
	// A divisor of 2^32 or more: the quotient fits in 32 bits. Estimate it
	// from the normalized top divisor word (Hacker's Delight, divDU): the
	// estimate is the quotient or one less, and one compare corrects it.
	x.bind(large)
	x.emit([]byte{0x0f, 0xbd, modRM(3, byte(ecx), byte(esi))}, "bsr ECX, ESI")
	x.immediate(groupXor, ecx, 31) // n, the leading zeros of the high word
	x.mov(edi, esi)
	x.double(true, edi, ebx, -1) // the top 32 bits of v << n
	x.double(false, eax, edx, 1)
	x.shift(shiftRight, edx, 1) // u >> 1 keeps the DIV from overflowing
	x.unary(unaryDiv, edi)
	x.immediate(groupXor, ecx, 31)
	x.shiftCL(shiftRight, eax) // q0 = q1 >> (31 - n)
	x.immediate(groupSub, eax, 1)
	x.immediate(groupAdc, eax, 0) // q0 - 1 unless q0 is 0
	x.mov(edi, eax)
	x.unary(unaryMul, ebx)
	x.mov(ecx, edi)
	x.emit([]byte{0x0f, 0xaf, modRM(3, byte(ecx), byte(esi))}, "imul ECX, ESI")
	x.alu(aluAdd, edx, ecx) // EDX:EAX = q0 * v, below 2^64
	x.load(ecx, s)
	x.alu(aluSub, ecx, eax)
	x.load(eax, s+1)
	x.alu(aluSbb, eax, edx) // EAX:ECX = r = u - q0 * v, below 2v
	done := x.label()
	x.alu(aluCmp, ecx, ebx)
	x.mov(edx, eax)
	x.alu(aluSbb, edx, esi)
	x.branch(ccBelow, done)
	if remainder {
		x.alu(aluSub, ecx, ebx)
		x.alu(aluSbb, eax, esi)
	} else {
		x.emit([]byte{0x40 | byte(edi)}, "inc EDI")
	}
	x.bind(done)
	if remainder {
		x.mov(edx, eax)
		x.mov(eax, ecx)
	} else {
		x.mov(eax, edi)
		x.alu(aluXor, edx, edx)
	}
	x.bind(join)
	if signed {
		m, text := x.memory(groupCmp, s+3)
		x.emit(append(append([]byte{0x83}, m...), 0), "cmp dword ptr %s, 0", text)
		x.branch(ccNotSign, end)
		x.negate64(eax, edx)
	}
	if checked {
		x.branch(ccAlways, end)
		// EDX:EAX still holds the dividend: x % 0 = x.
		x.bind(zero)
		if !remainder {
			if semantics == op.DivisionAllOnes {
				x.immediate(groupOr, eax, 0xffffffff)
				x.mov(edx, eax)
			} else {
				x.alu(aluXor, eax, eax)
				x.alu(aluXor, edx, edx)
			}
		}
	}
	x.bind(end)
}

func name16(r backend.Register) string {
	return [...]string{"AX", "CX", "DX", "BX", "SP", "BP", "SI", "DI"}[r]
}

func (e *ByteAssembler) Divide(ins machine.Instruction) {
	if !e.require(validDivide(ins)) {
		return
	}
	emitExpansion(e, dividePlan(ins, e.slotFrame(), false))
}
func (e *TextAssembler) Divide(ins machine.Instruction) {
	if !e.require(validDivide(ins)) {
		return
	}
	recordExpansion(e, dividePlan(ins, e.slotFrame(), true))
}

// divideResult is the register DIV and IDIV leave the quotient (EAX) or
// the remainder (EDX) in.
func divideResult(remainder bool) backend.Register {
	if remainder {
		return 2
	}
	return 0
}
