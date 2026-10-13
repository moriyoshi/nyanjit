package arm64

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func fpMoveToVector(width uint8, vector, integer uint32) uint32 {
	base := uint32(0x1e270000) // FMOV Sd, Wn
	if width == 64 {
		base = 0x9e670000 // FMOV Dd, Xn
	}
	return base | integer<<5 | vector
}

func fpMoveToInteger(width uint8, integer, vector uint32) uint32 {
	base := uint32(0x1e260000) // FMOV Wd, Sn
	if width == 64 {
		base = 0x9e660000 // FMOV Xd, Dn
	}
	return base | vector<<5 | integer
}

func fpArithmeticWord(operation op.Opcode, width uint8) uint32 {
	var base uint32
	switch operation {
	case op.OpFAdd:
		base = 0x1e202800
	case op.OpFSub:
		base = 0x1e203800
	case op.OpFMul:
		base = 0x1e200800
	case op.OpFDiv:
		base = 0x1e201800
	default:
		return 0
	}
	if width == 64 {
		base |= 0x00400000
	}
	return base
}

func floatPlan(ins machine.Instruction, text bool) concretePlan {
	if !validFloat(&ins) {
		return concretePlan{}
	}

	formatText := func(f string, args ...any) string {
		if !text {
			return ""
		}
		return fmt.Sprintf(f, args...)
	}
	var out concretePlan
	emit := func(w uint32) { out.parts[out.n].word = w; out.n++ }
	fpr := func(w uint8) string {
		if w == 64 {
			return "D"
		}
		return "S"
	}
	gpr := func(w uint8) string {
		if w == 64 {
			return "X"
		}
		return "W"
	}
	input := func(r backend.Register, t uint32, w uint8) uint32 {
		if backend.IsVectorRegister(r) {
			return uint32(backend.VectorNumber(r))
		}
		emit(fpMoveToVector(w, t, uint32(r)))
		if text {
			out.parts[out.n-1].text = formatText("fmov %s%d, %s%d", fpr(w), t, gpr(w), r)
		}
		return t
	}
	w := ins.Bits
	d := uint32(16)
	if backend.IsVectorRegister(ins.A) {
		d = uint32(backend.VectorNumber(ins.A))
	}
	if ins.Op == machine.OpFloatMove {
		if backend.IsVectorRegister(ins.A) && backend.IsVectorRegister(ins.B) {
			s := uint32(backend.VectorNumber(ins.B))
			base := uint32(0x1e204000)
			if w == 64 {
				base |= 0x400000
			}
			if d != s {
				emit(base | s<<5 | d)
				if text {
					out.parts[out.n-1].text = formatText("fmov %s%d, %s%d", fpr(w), d, fpr(w), s)
				}
			}
		} else if backend.IsVectorRegister(ins.A) {
			input(ins.B, d, w)
		} else {
			emit(fpMoveToInteger(w, uint32(ins.A), uint32(backend.VectorNumber(ins.B))))
			if text {
				out.parts[out.n-1].text = formatText("fmov %s%d, %s%d", gpr(w), ins.A, fpr(w), backend.VectorNumber(ins.B))
			}
		}
		return out
	}
	if ins.Op == machine.OpFloatBits {
		src := input(ins.B, 16, w)
		base, name := uint32(0x1e214000), "fneg"
		if ins.Arithmetic == op.OpAnd {
			base, name = 0x1e20c000, "fabs"
		}
		if w == 64 {
			base |= 0x400000
		}
		emit(base | src<<5 | d)
		if text {
			out.parts[out.n-1].text = formatText("%s %s%d, %s%d", name, fpr(w), d, fpr(w), src)
		}
	} else if ins.Op == machine.OpFloatUnary {
		sw, word, name := w, uint32(0x1e21c000), "fsqrt"
		if w == 64 {
			word |= 0x400000
		}
		if ins.Arithmetic == op.OpFConvert {
			sw = 96 - w
			name = "fcvt"
			word = 0x1e22c000
			if w == 32 {
				word = 0x1e624000
			}
		}
		s := input(ins.B, 16, sw)
		emit(word | s<<5 | d)
		if text {
			out.parts[out.n-1].text = formatText("%s %s%d, %s%d", name, fpr(w), d, fpr(sw), s)
		}
	} else {
		a, b := input(ins.B, 16, w), input(ins.C, 17, w)
		if ins.Op == machine.OpFloatFMA {
			c := input(ins.D, 18, w)
			base := uint32(0x1f000000)
			if w == 64 {
				base |= 0x400000
			}
			emit(base | b<<16 | c<<10 | a<<5 | d)
			if text {
				out.parts[out.n-1].text = formatText("fmadd %s%d, %s%d, %s%d, %s%d", fpr(w), d, fpr(w), a, fpr(w), b, fpr(w), c)
			}
		} else {
			emit(fpArithmeticWord(ins.Arithmetic, w) | b<<16 | a<<5 | d)
			if text {
				out.parts[out.n-1].text = formatText("%s %s%d, %s%d, %s%d", fpArithmeticName(ins.Arithmetic), fpr(w), d, fpr(w), a, fpr(w), b)
			}
		}
	}
	if !backend.IsVectorRegister(ins.A) {
		emit(fpMoveToInteger(w, uint32(ins.A), d))
		if text {
			out.parts[out.n-1].text = formatText("fmov %s%d, %s%d", gpr(w), ins.A, fpr(w), d)
		}
	}
	return out
}
func FloatSize(ins machine.Instruction) int {
	if !validFloat(&ins) {
		return 0
	}
	if ins.Op == machine.OpFloatMove {
		if ins.A == ins.B {
			return 0
		}
		return 4
	}
	// Each GP input/output needs one FMOV around the single FP operation.
	// The byte/text plan uses the same register-bank distinction.
	size := 4
	if !backend.IsVectorRegister(ins.A) {
		size += 4
	}
	if !backend.IsVectorRegister(ins.B) {
		size += 4
	}
	if ins.Op == machine.OpFloatBinary || ins.Op == machine.OpFloatFMA {
		if !backend.IsVectorRegister(ins.C) {
			size += 4
		}
	}
	if ins.Op == machine.OpFloatFMA && !backend.IsVectorRegister(ins.D) {
		size += 4
	}
	return size
}
func (e *ByteAssembler) Float(ins machine.Instruction) {
	if !e.require((validInstructionRegisters(&ins))) {
		return
	}

	if !e.require(validFloat(&ins)) {
		return
	}

	p := floatPlan(ins, false)
	for _, i := range p.parts[:p.n] {
		e.words = append(e.words, i.word)
	}
}
func (e *TextAssembler) Float(ins machine.Instruction) {
	if !e.valid(func(enc *ByteAssembler) { enc.Float(ins) }) {
		return
	}
	p := floatPlan(ins, true)
	for _, i := range p.parts[:p.n] {
		e.line("%s", i.text)
	}
}

func (e *ByteAssembler) FloatUnary(op op.Opcode, w uint8, d, a backend.Register) {
	if !e.require((validFloatRegisters(d, a))) {
		return
	}

	e.Float(machine.Instruction{Op: machine.OpFloatUnary, Arithmetic: op, Bits: w, A: d, B: a})
}

func (e *ByteAssembler) FloatBinary(op op.Opcode, w uint8, d, a, b backend.Register) {
	if !e.require((validFloatRegisters(d, a, b))) {
		return
	}

	e.Float(machine.Instruction{Op: machine.OpFloatBinary, Arithmetic: op, Bits: w, A: d, B: a, C: b})
}

func (e *ByteAssembler) FloatFMA(w uint8, d, a, b, c backend.Register) {
	if !e.require((validFloatRegisters(d, a, b, c))) {
		return
	}

	e.Float(machine.Instruction{Op: machine.OpFloatFMA, Bits: w, A: d, B: a, C: b, D: c})
}

func (e *TextAssembler) FloatUnary(op op.Opcode, w uint8, d, a backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.FloatUnary(op, w, d, a) }) {
		return
	}
	e.Float(machine.Instruction{Op: machine.OpFloatUnary, Arithmetic: op, Bits: w, A: d, B: a})
}

func (e *TextAssembler) FloatBinary(op op.Opcode, w uint8, d, a, b backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.FloatBinary(op, w, d, a, b) }) {
		return
	}
	e.Float(machine.Instruction{Op: machine.OpFloatBinary, Arithmetic: op, Bits: w, A: d, B: a, C: b})
}

func (e *TextAssembler) FloatFMA(w uint8, d, a, b, c backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.FloatFMA(w, d, a, b, c) }) {
		return
	}
	e.Float(machine.Instruction{Op: machine.OpFloatFMA, Bits: w, A: d, B: a, C: b, D: c})
}

func validFloat(ins *machine.Instruction) bool {
	if !validInstructionRegisters(ins) || ins.Bits != 32 && ins.Bits != 64 {
		return false
	}
	switch ins.Op {
	case machine.OpFloatBinary:
		switch ins.Arithmetic {
		case op.OpFAdd, op.OpFSub, op.OpFMul, op.OpFDiv:
			return true
		}
		return false
	case machine.OpFloatUnary:
		return ins.Arithmetic == op.OpFSqrt || ins.Arithmetic == op.OpFConvert
	case machine.OpFloatBits:
		return ins.Arithmetic == op.OpAnd || ins.Arithmetic == op.OpXor
	case machine.OpFloatMove, machine.OpFloatFMA:
		return true
	}
	return false
}

func fpArithmeticName(operation op.Opcode) string {
	switch operation {
	case op.OpFAdd:
		return "fadd"
	case op.OpFSub:
		return "fsub"
	case op.OpFMul:
		return "fmul"
	case op.OpFDiv:
		return "fdiv"
	}
	return ""
}
