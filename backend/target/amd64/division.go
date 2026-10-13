package amd64

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// expansionStep is one instruction of a fixed expansion. A short branch's
// target is a label until resolve, and then the byte offset of the target
// within the expansion, so the listing can print an absolute address.
type expansionStep struct {
	code   []byte
	text   string
	target int // -1 for a step that does not branch
}

// expansion builds a self-contained sequence whose internal branches are all
// rel8. Its size never depends on the PC, as layout requires.
type expansion struct {
	steps  []expansionStep
	labels []int // step index each label is bound to
}

func (x *expansion) emit(code []byte, format string, args ...any) {
	x.steps = append(x.steps, expansionStep{code, fmt.Sprintf(format, args...), -1})
}
func (x *expansion) label() int {
	x.labels = append(x.labels, -1)
	return len(x.labels) - 1
}
func (x *expansion) bind(label int) { x.labels[label] = len(x.steps) }

// branch emits a short Jcc (0x70|cc) or JMP (0xEB) to label.
func (x *expansion) branch(opcode byte, mnemonic string, label int) {
	x.steps = append(x.steps, expansionStep{[]byte{opcode, 0}, mnemonic, label})
}

// resolve fills in every branch displacement. Expansions are far shorter
// than 128 bytes, so a displacement out of rel8 range is a plan bug.
func (x *expansion) resolve() []expansionStep {
	offsets := make([]int, len(x.steps)+1)
	for n, s := range x.steps {
		offsets[n+1] = offsets[n] + len(s.code)
	}
	for n := range x.steps {
		s := &x.steps[n]
		if s.target < 0 {
			continue
		}
		to := offsets[x.labels[s.target]]
		rel := to - offsets[n+1]
		if rel < -128 || rel > 127 {
			panic("amd64: expansion branch out of rel8 range")
		}
		s.code[len(s.code)-1] = byte(int8(rel))
		s.target = to
	}
	return x.steps
}

func expansionSize(steps []expansionStep) int {
	n := 0
	for _, s := range steps {
		n += len(s.code)
	}
	return n
}
func (e *ByteAssembler) expansion(steps []expansionStep) {
	for _, s := range steps {
		e.code = append(e.code, s.code...)
	}
}
func (e *TextAssembler) expansion(steps []expansionStep) {
	start := e.pc
	for _, s := range steps {
		if s.target >= 0 {
			e.line(len(s.code), "%s 0x%x", s.text, start+s.target)
		} else {
			e.line(len(s.code), "%s", s.text)
		}
	}
}

// gpRR encodes opcode with a register-direct ModRM. REX carries W and the
// extension bits, and byteRM forces it for an 8-bit SPL..DIL operand in rm.
// XMM numbers encode the same way.
func gpRR(wide bool, reg, rm uint8, byteRM bool, opcode ...byte) []byte {
	code := make([]byte, 0, 8)
	rex := byte(0x40) | reg>>3<<2 | rm>>3
	if wide {
		rex |= 8
	}
	if rex != 0x40 || byteRM && rm >= 4 && rm < 8 {
		code = append(code, rex)
	}
	code = append(code, opcode...)
	return append(code, 0xc0|(reg&7)<<3|rm&7)
}

func gpNames(wide bool) func(uint8) string {
	if wide {
		return func(r uint8) string { return amd64RegisterNames[r] }
	}
	return func(r uint8) string { return amd64RegisterNames32[r] }
}

func validDivide(i machine.Instruction) bool {
	switch i.Arithmetic {
	case op.OpDiv, op.OpDivSigned, op.OpRem, op.OpRemSigned:
	default:
		return false
	}
	// DIV reads RDX:RAX and writes both, so the divisor cannot live there.
	return validGeneralRegisters(i.A, i.B, i.C) && validWidth(i.Bits) && i.Imm <= 0xff && op.DivisionSemantics(i.Imm).Valid() &&
		i.C != 0 && i.C != 2
}

// dividePlan expands OpDivide around DIV or IDIV, which trap on a zero
// divisor and on signed MIN / -1. The checked semantics branch around the
// instruction for those divisors; every path writes A only at its end, so A
// may alias B or C. Narrow operands widen to 32 bits, which avoids the AH
// forms and cannot overflow: -128 / -1 = 128 truncates back to MIN.
func dividePlan(i machine.Instruction) []expansionStep {
	var x expansion
	w, a, b, c := i.Bits, uint8(i.A), uint8(i.B), uint8(i.C)
	signed := i.Arithmetic == op.OpDivSigned || i.Arithmetic == op.OpRemSigned
	remainder := i.Arithmetic == op.OpRem || i.Arithmetic == op.OpRemSigned
	semantics := op.DivisionSemantics(i.Imm)
	checked := semantics != op.DivisionUnchecked
	wide := w == 64
	name := gpNames(wide)
	zero, negate, done := x.label(), x.label(), x.label()
	// Only 32- and 64-bit IDIV can overflow.
	minusOne := checked && signed && w >= 32
	if checked {
		x.emit(gpRR(wide, c, c, false, 0x85), "test %s, %s", name(c), name(c))
		x.branch(0x74, "jz", zero)
		if minusOne {
			x.emit(append(gpRR(wide, 7, c, false, 0x83), 0xff), "cmp %s, -1", name(c))
			x.branch(0x74, "je", negate)
		}
	}
	if signed && w < 32 {
		extend, names := byte(0xbe), amd64RegisterNames8
		if w == 16 {
			extend, names = 0xbf, amd64RegisterNames16
		}
		// B goes first: R11 may be B, but C is never RAX.
		x.emit(gpRR(false, 0, b, w == 8, 0x0f, extend), "movsx EAX, %s", names[b])
		x.emit(gpRR(false, 11, c, w == 8, 0x0f, extend), "movsx R11D, %s", names[c])
		x.emit([]byte{0x99}, "cdq")
		x.emit(gpRR(false, 7, 11, false, 0xf7), "idiv R11D")
	} else {
		if b != 0 {
			x.emit(gpRR(wide, b, 0, false, 0x89), "mov %s, %s", name(0), name(b))
		}
		digit, mnemonic := byte(6), "div"
		switch {
		case signed && wide:
			x.emit([]byte{0x48, 0x99}, "cqo")
			digit, mnemonic = 7, "idiv"
		case signed:
			x.emit([]byte{0x99}, "cdq")
			digit, mnemonic = 7, "idiv"
		default:
			x.emit(gpRR(false, 2, 2, false, 0x31), "xor EDX, EDX")
		}
		x.emit(gpRR(wide, digit, c, false, 0xf7), "%s %s", mnemonic, name(c))
	}
	result := uint8(0)
	if remainder {
		result = 2
	}
	if signed && w < 32 {
		// The 32-bit quotient or remainder is sign-extended; keep Bits.
		extend, names := byte(0xb6), amd64RegisterNames8
		if w == 16 {
			extend, names = 0xb7, amd64RegisterNames16
		}
		x.emit(gpRR(false, a, result, false, 0x0f, extend), "movzx %s, %s", amd64RegisterNames32[a], names[result])
	} else if a != result {
		x.emit(gpRR(wide, result, a, false, 0x89), "mov %s, %s", name(a), name(result))
	}
	if checked {
		zeroEmpty := remainder && a == b // x % 0 = x is already in place
		if minusOne || !zeroEmpty {
			x.branch(0xeb, "jmp", done)
		}
		if minusOne {
			// x / -1 = -x wraps MIN to MIN, and x % -1 = 0, for every x.
			x.bind(negate)
			if remainder {
				x.emit(gpRR(false, a, a, false, 0x31), "xor %s, %s", amd64RegisterNames32[a], amd64RegisterNames32[a])
			} else {
				if a != b {
					x.emit(gpRR(wide, b, a, false, 0x89), "mov %s, %s", name(a), name(b))
				}
				x.emit(gpRR(wide, 3, a, false, 0xf7), "neg %s", name(a))
			}
			if !zeroEmpty {
				x.branch(0xeb, "jmp", done)
			}
		}
		x.bind(zero)
		switch {
		case remainder:
			if a != b {
				x.emit(gpRR(true, b, a, false, 0x89), "mov %s, %s", amd64Name(i.A), amd64Name(i.B))
			}
		case semantics == op.DivisionZero:
			x.emit(gpRR(false, a, a, false, 0x31), "xor %s, %s", amd64RegisterNames32[a], amd64RegisterNames32[a])
		default:
			ones := ^uint64(0) >> (64 - w)
			if wide {
				x.emit(appendMovImm64(nil, a, ones), "mov %s, -1", name(a))
			} else {
				x.emit(appendMovImm64(nil, a, ones), "mov %s, 0x%x", amd64RegisterNames32[a], ones)
			}
		}
	}
	x.bind(done)
	return x.resolve()
}

func (e *ByteAssembler) Divide(ins machine.Instruction) {
	if !e.require(ins.Op == machine.OpDivide && validDivide(ins)) {
		return
	}
	e.expansion(dividePlan(ins))
}
func (e *TextAssembler) Divide(ins machine.Instruction) {
	if !e.valid(func(enc *ByteAssembler) { enc.Divide(ins) }) {
		return
	}
	e.expansion(dividePlan(ins))
}
