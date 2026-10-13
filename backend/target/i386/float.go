package i386

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// HighA/HighB/HighC carry the second GPR word for binary64 operands. They
// are ignored for binary32 and XMM operands. XMM0-2 are expansion temporaries.
// floatPlan appends the code of a form validFloatForm accepts to p.
func floatPlan(ins machine.Instruction, p *planCode) {
	rr := func(prefix, opcode, reg, rm byte) {
		p.code = append(p.code, prefix, 0x0f, opcode, 0xc0|reg<<3|rm)
	}
	movd := func(to bool, fp uint8, gp backend.Register) {
		if to {
			rr(0x66, 0x6e, fp, byte(gp))
			if p.text {
				p.line("movd xmm%d, %s", fp, name(gp))
			}
		} else {
			rr(0x66, 0x7e, fp, byte(gp))
			if p.text {
				p.line("movd %s, xmm%d", name(gp), fp)
			}
		}
	}
	transferIn := func(width uint8, dst uint8, lo, hi backend.Register) {
		movd(true, dst, lo)
		if width == 64 {
			movd(true, 2, hi)
			rr(0x66, 0x62, dst, 2)
			if p.text {
				p.line("punpckldq xmm%d, xmm2", dst)
			}
		}
	}
	transferOut := func(width uint8, src uint8, lo, hi backend.Register) {
		movd(false, src, lo)
		if width == 64 {
			// Preserve src for FP-to-GP copies of still-live values.
			rr(0x66, 0x70, 2, src)
			p.code = append(p.code, 0x55)
			if p.text {
				p.line("pshufd xmm2, xmm%d, 0x55", src)
			}
			movd(false, 2, hi)
		}
	}
	input := func(reg, hi backend.Register, temp uint8, width uint8) uint8 {
		if backend.IsVectorRegister(reg) {
			return backend.VectorNumber(reg)
		}
		transferIn(width, temp, reg, hi)
		return temp
	}
	copyFP := func(dst, src uint8) {
		if dst != src {
			rr(0x66, 0x28, dst, src)
			if p.text {
				p.line("movapd xmm%d, xmm%d", dst, src)
			}
		}
	}
	width := ins.Bits
	dst := uint8(0)
	if backend.IsVectorRegister(ins.A) {
		dst = backend.VectorNumber(ins.A)
	}
	if ins.Op == machine.OpFloatMove {
		if backend.IsVectorRegister(ins.A) {
			if backend.IsVectorRegister(ins.B) {
				copyFP(dst, backend.VectorNumber(ins.B))
			} else {
				transferIn(width, dst, ins.B, ins.HighB)
			}
		} else {
			transferOut(width, backend.VectorNumber(ins.B), ins.A, ins.HighA)
		}
		return
	}
	prefix, suffix := byte(0xf3), "ss"
	if width == 64 {
		prefix, suffix = 0xf2, "sd"
	}
	switch ins.Op {
	case machine.OpFloatUnary:
		sw, opcode, mnemonic := width, byte(0x51), "sqrt"+suffix
		if ins.Arithmetic == op.OpFConvert {
			sw, opcode = 96-width, 0x5a
			prefix, mnemonic = 0xf3, "cvtss2sd"
			if width == 32 {
				prefix, mnemonic = 0xf2, "cvtsd2ss"
			}
		}
		src := input(ins.B, ins.HighB, 0, sw)
		rr(prefix, opcode, dst, src)
		if p.text {
			p.line("%s xmm%d, xmm%d", mnemonic, dst, src)
		}
	case machine.OpFloatBinary:
		left, right := input(ins.B, ins.HighB, 0, width), input(ins.C, ins.HighC, 1, width)
		if dst == right && dst != left {
			// Explicit homes can alias the right input despite the allocator's
			// left-only reuse rule. Preserve it before the destructive copy.
			temp := uint8(1)
			for temp == left || temp == right {
				temp = (temp + 1) % 3
			}
			copyFP(temp, right)
			right = temp
		}
		copyFP(dst, left)
		opcode, mnemonic := sseArithmetic(ins.Arithmetic)
		rr(prefix, opcode, dst, right)
		if p.text {
			p.line("%s%s xmm%d, xmm%d", mnemonic, suffix, dst, right)
		}
	case machine.OpFloatBits:
		src := input(ins.B, ins.HighB, 1, width)
		copyFP(dst, src)
		opcode, mnemonic := byte(0x57), "xorpd"
		if ins.Arithmetic == op.OpAnd {
			opcode, mnemonic = 0x54, "andpd"
		}
		rr(0x66, opcode, dst, 0)
		if p.text {
			p.line("%s xmm%d, xmm0", mnemonic, dst)
		}
	}
	if !backend.IsVectorRegister(ins.A) {
		transferOut(width, dst, ins.A, ins.HighA)
	}
}

func FloatSize(ins machine.Instruction) int {
	if !validFloatForm(ins) {
		return 0
	}
	var buffer [32]byte
	p := planCode{code: buffer[:0]}
	floatPlan(ins, &p)
	return len(p.code)
}
func (e *ByteAssembler) Float(ins machine.Instruction) {
	if !e.require(validFloatForm(ins)) {
		return
	}

	p := planCode{code: e.code}
	floatPlan(ins, &p)
	e.code = p.code
}
func (e *TextAssembler) Float(ins machine.Instruction) {
	if !e.require(validFloatForm(ins)) {
		return
	}

	p := planCode{text: true, code: e.code}
	floatPlan(ins, &p)
	e.code = p.code
	e.recordPlan(&p)
}

// FloatUnary and FloatBinary accept XMM operands or one-word GP operands.
// Use Float with explicit High* fields for GP-resident binary64 values.
func (e *ByteAssembler) FloatUnary(op op.Opcode, w uint8, d, a backend.Register) {
	if !e.require(validFloatRegisters(d) && validFloatRegisters(a)) {
		return
	}

	e.Float(machine.Instruction{Op: machine.OpFloatUnary, Arithmetic: op, Bits: w, A: d, B: a})
}
func (e *TextAssembler) FloatUnary(op op.Opcode, w uint8, d, a backend.Register) {
	if !e.require(validFloatRegisters(d) && validFloatRegisters(a)) {
		return
	}

	e.Float(machine.Instruction{Op: machine.OpFloatUnary, Arithmetic: op, Bits: w, A: d, B: a})
}
func (e *ByteAssembler) FloatBinary(op op.Opcode, w uint8, d, a, b backend.Register) {
	if !e.require(validFloatRegisters(d) && validFloatRegisters(a) && validFloatRegisters(b)) {
		return
	}

	e.Float(machine.Instruction{Op: machine.OpFloatBinary, Arithmetic: op, Bits: w, A: d, B: a, C: b})
}
func (e *TextAssembler) FloatBinary(op op.Opcode, w uint8, d, a, b backend.Register) {
	if !e.require(validFloatRegisters(d) && validFloatRegisters(a) && validFloatRegisters(b)) {
		return
	}

	e.Float(machine.Instruction{Op: machine.OpFloatBinary, Arithmetic: op, Bits: w, A: d, B: a, C: b})
}

// sseArithmetic is the SSE opcode and mnemonic stem of an FP operation.
func sseArithmetic(operation op.Opcode) (byte, string) {
	switch operation {
	case op.OpFAdd:
		return 0x58, "add"
	case op.OpFSub:
		return 0x5c, "sub"
	case op.OpFMul:
		return 0x59, "mul"
	case op.OpFDiv:
		return 0x5e, "div"
	}
	return 0, ""
}
