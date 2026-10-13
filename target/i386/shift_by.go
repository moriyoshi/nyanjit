package i386

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"strings"
)

type shiftByInstruction struct {
	code   []byte
	text   string
	target string
}

// Fixed inputs: EAX:EDX = value, EBX:ECX = unsigned count. Outputs EAX:EDX.
// Private local branches implement >=width saturation and the 32-bit crossing.
func shiftByPlan(ins machine.Instruction) []shiftByInstruction {
	width, op := ins.Bits, ins.Arithmetic
	var out []shiftByInstruction
	label := func(s string) { out = append(out, shiftByInstruction{text: s + ":"}) }
	emit := func(code []byte, format string, args ...any) {
		out = append(out, shiftByInstruction{code: code, text: fmt.Sprintf(format, args...)})
	}
	branch := func(cc byte, target string) {
		out = append(out, shiftByInstruction{code: []byte{0x0f, cc, 0, 0, 0, 0}, text: fmt.Sprintf("j%s .shift_%s", map[byte]string{0x85: "ne", 0x83: "ae"}[cc], target), target: target})
	}
	jump := func(target string) {
		out = append(out, shiftByInstruction{code: []byte{0xe9, 0, 0, 0, 0}, text: "jmp .shift_" + target, target: target})
	}
	mov := func(d, s machine.Register) {
		if d != s {
			emit([]byte{0x89, modRM(3, byte(s), byte(d))}, "mov %s, %s", name(d), name(s))
		}
	}
	zero := func(d machine.Register) { emit([]byte{0xb8 | byte(d), 0, 0, 0, 0}, "mov %s, 0", name(d)) }
	immshift := func(d machine.Register, op ir.Opcode, c byte) {
		emit([]byte{0xc1, modRM(3, map[ir.Opcode]byte{ir.OpShiftLeft: 4, ir.OpShiftRight: 5, ir.OpShiftRightArithmetic: 7}[op], byte(d)), c}, "%s %s, %d", shiftName(op), name(d), c)
	}
	shift := func(d machine.Register, op ir.Opcode, w uint8) {
		code := []byte{}
		if w == 16 {
			code = append(code, 0x66)
		}
		opcode := byte(0xd3)
		if w == 8 {
			opcode = 0xd2
		}
		code = append(code, opcode, modRM(3, map[ir.Opcode]byte{ir.OpShiftLeft: 4, ir.OpShiftRight: 5, ir.OpShiftRightArithmetic: 7, ir.OpRotateLeft: 0, ir.OpRotateRight: 1}[op], byte(d)))
		n := name(d)
		if w == 8 {
			n = "AL"
		}
		if w == 16 {
			n = "AX"
		}
		emit(code, "%s %s, CL", shiftName(op), n)
	}
	double := func(d, s machine.Register, left bool) {
		opcode := byte(0xad)
		mnemonic := "shrd"
		if left {
			opcode = 0xa5
			mnemonic = "shld"
		}
		emit([]byte{0x0f, opcode, modRM(3, byte(s), byte(d))}, "%s %s, %s, CL", mnemonic, name(d), name(s))
	}
	compare := func(w uint8) { emit([]byte{0x83, 0xf9, w}, "cmp ECX, %d", w) }
	mask := func() {
		if width < 32 {
			v := uint32(1<<width) - 1
			emit(append32([]byte{0x81, 0xe0}, v), "and EAX, 0x%x", v)
		}
	}
	rotate := op == ir.OpRotateLeft || op == ir.OpRotateRight
	if rotate && width <= 32 {
		shift(0, op, width)
		mask()
	} else {
		if !rotate {
			emit([]byte{0x85, 0xdb}, "test EBX, EBX")
			branch(0x85, "overflow")
			compare(width)
			branch(0x83, "overflow")
		}
		if width <= 32 {
			if op == ir.OpShiftRightArithmetic && width < 32 {
				immshift(0, ir.OpShiftLeft, 32-width)
				immshift(0, ir.OpShiftRightArithmetic, 32-width)
			}
			shift(0, op, 32)
			mask()
			jump("end")
		} else {
			if rotate {
				emit([]byte{0x83, 0xe1, 63}, "and ECX, 63")
			}
			compare(32)
			branch(0x83, "large")
			if rotate {
				mov(3, 0)
				if op == ir.OpRotateLeft {
					double(0, 2, true)
					double(2, 3, true)
				} else {
					double(0, 2, false)
					double(2, 3, false)
				}
			} else if op == ir.OpShiftLeft {
				double(2, 0, true)
				shift(0, op, 32)
			} else {
				double(0, 2, false)
				shift(2, op, 32)
			}
			jump("end")
			label("large")
			if rotate {
				emit([]byte{0x92}, "xchg EAX, EDX")
				mov(3, 0)
				if op == ir.OpRotateLeft {
					double(0, 2, true)
					double(2, 3, true)
				} else {
					double(0, 2, false)
					double(2, 3, false)
				}
			} else if op == ir.OpShiftLeft {
				mov(2, 0)
				shift(2, op, 32)
				zero(0)
			} else {
				mov(0, 2)
				shift(0, op, 32)
				if op == ir.OpShiftRightArithmetic {
					immshift(2, op, 31)
				} else {
					zero(2)
				}
			}
			jump("end")
		}
		if !rotate {
			label("overflow")
			if op == ir.OpShiftRightArithmetic {
				if width <= 32 {
					if width < 32 {
						immshift(0, ir.OpShiftLeft, 32-width)
					}
					immshift(0, op, 31)
					mask()
				} else {
					immshift(2, op, 31)
					mov(0, 2)
				}
			} else {
				zero(0)
				if width == 64 {
					zero(2)
				}
			}
		}
		label("end")
	}
	// Resolve private displacements without adding builder-visible labels.
	labels := map[string]int{}
	pc := 0
	for _, i := range out {
		if len(i.code) == 0 {
			labels[i.text[:len(i.text)-1]] = pc
		}
		pc += len(i.code)
	}
	pc = 0
	for n, i := range out {
		if i.target != "" {
			offset := labels[i.target] - (pc + len(i.code))
			start := len(i.code) - 4
			out[n].code = append(append([]byte{}, i.code[:start]...), byte(offset), byte(offset>>8), byte(offset>>16), byte(offset>>24))
		}
		pc += len(i.code)
	}
	return out
}
func (e *ByteAssembler) ShiftByInstruction(ins machine.Instruction) {
	for _, i := range shiftByPlan(ins) {
		e.code = append(e.code, i.code...)
	}
}
func (e *TextAssembler) ShiftByInstruction(ins machine.Instruction) {
	start := e.AssembledSize()
	for _, i := range shiftByPlan(ins) {
		if len(i.code) > 0 {
			if i.target != "" {
				e.record("%s_%x", i.text, start)
			} else {
				e.record("%s", i.text)
			}
		} else {
			e.record(".shift_%s_%x:", strings.TrimSuffix(i.text, ":"), start)
		}
		e.code = append(e.code, i.code...)
	}
}
