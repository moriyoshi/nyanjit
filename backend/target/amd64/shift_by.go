package amd64

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/op"
)

type shiftByInstruction struct {
	code []byte
	text string
}

// RCX (and R11 for shifts) must be freed by the allocator. R10 may be the
// workspace base, and is never used as an untracked expansion temporary.
func shiftByPlan(op op.Opcode, width uint8, dst, src, count backend.Register) []shiftByInstruction {
	return shiftByCPUPlan(op, width, dst, src, count, abi.ResolvedCPU{})
}
func shiftByCPUPlan(operation op.Opcode, width uint8, dst, src, count backend.Register, profile abi.ResolvedCPU) []shiftByInstruction {
	if bmiShift(operation, width, profile) {
		return bmiShiftPlan(operation, width, dst, src, count)
	}
	var out []shiftByInstruction
	emit := func(code []byte, format string, args ...any) {
		out = append(out, shiftByInstruction{code, fmt.Sprintf(format, args...)})
	}
	move := func(d, s backend.Register) {
		if d != s {
			emit(appendMovReg(nil, uint8(d), uint8(s)), "mov %s, %s", amd64Name(d), amd64Name(s))
		}
	}
	narrow := func(d backend.Register) {
		b := NewByteAssembler(0, 0)
		b.Narrow(d, d, width)
		name := amd64RegisterNames8[d]
		if width == 16 {
			name = amd64RegisterNames16[d]
		}
		if width == 32 {
			emit(b.Bytes(), "mov %s, %s", amd64RegisterNames32[d], amd64RegisterNames32[d])
		} else {
			emit(b.Bytes(), "movzx %s, %s", amd64RegisterNames32[d], name)
		}
	}
	move(1, count)
	rotate := operation == op.OpRotateLeft || operation == op.OpRotateRight
	move(dst, src)
	if !rotate {
		if operation == op.OpShiftRightArithmetic {
			if width < 64 {
				b := NewByteAssembler(0, 0)
				b.SignExtend(dst, dst, width)
				name := amd64RegisterNames8[dst]
				if width == 16 {
					name = amd64RegisterNames16[dst]
				}
				if width == 32 {
					name = amd64RegisterNames32[dst]
				}
				emit(b.Bytes(), "movsx %s, %s", amd64Name(dst), name)
			}
			move(11, dst)
			b := NewByteAssembler(0, 0)
			b.Shift(op.OpShiftRightArithmetic, 64, 11, 11, 63)
			emit(b.Bytes(), "sar R11, 63")
		} else {
			emit([]byte{0x41, 0xbb, 0, 0, 0, 0}, "mov R11D, 0")
		}
	}
	dataWidth := width
	if !rotate {
		dataWidth = 64
	}
	prefix := []byte{}
	if dataWidth == 16 {
		prefix = append(prefix, 0x66)
	}
	rex := byte(0x40) | byte(dst>>3)
	if dataWidth == 64 {
		rex |= 8
	}
	prefix = append(prefix, rex)
	group, mnemonic := shiftByGroup(operation)
	opcode := byte(0xd3)
	if dataWidth == 8 {
		opcode = 0xd2
	}
	name := amd64Name(dst)
	if dataWidth == 8 {
		name = amd64RegisterNames8[dst]
	}
	if dataWidth == 16 {
		name = amd64RegisterNames16[dst]
	}
	if dataWidth == 32 {
		name = amd64RegisterNames32[dst]
	}
	emit(append(prefix, opcode, 0xc0|group<<3|byte(dst&7)), "%s %s, CL", mnemonic, name)
	if !rotate {
		emit([]byte{0x48, 0x83, 0xf9, width}, "cmp RCX, %d", width)
		emit([]byte{0x49 | byte(dst>>3)<<2, 0x0f, 0x43, 0xc0 | byte(dst&7)<<3 | 3}, "cmovae %s, R11", amd64Name(dst))
	}
	if width < 64 {
		narrow(dst)
	}
	return out
}
func (e *ByteAssembler) ShiftBy(op op.Opcode, width uint8, dst, src, count backend.Register) {
	if !e.require((validGeneralRegisters(dst, src, count)) && (validWidth(width)) && (validShift(op))) {
		return
	}

	for _, i := range shiftByCPUPlan(op, width, dst, src, count, e.profile) {
		e.code = append(e.code, i.code...)
	}
}
func (e *TextAssembler) ShiftBy(op op.Opcode, width uint8, dst, src, count backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.ShiftBy(op, width, dst, src, count) }) {
		return
	}
	for _, i := range shiftByCPUPlan(op, width, dst, src, count, e.profile) {
		e.line(len(i.code), "%s", i.text)
	}
}
func (Policy) CanShiftBy(width uint8) bool {
	return width == 8 || width == 16 || width == 32 || width == 64
}

func bmiShift(operation op.Opcode, width uint8, p abi.ResolvedCPU) bool {
	return p.Has("bmi2") && (operation == op.OpShiftLeft || operation == op.OpShiftRight || operation == op.OpShiftRightArithmetic && width == 64)
}
func vexBMI(dst, src, v backend.Register, pp, opcode byte) []byte {
	b2 := byte(0xe2)
	if dst >= 8 {
		b2 &^= 0x80
	}
	if src >= 8 {
		b2 &^= 0x20
	}
	return []byte{0xc4, b2, 0x80 | byte(^v&15)<<3 | pp, opcode, 0xc0 | byte(dst&7)<<3 | byte(src&7)}
}
func bmiShiftPlan(operation op.Opcode, width uint8, dst, src, count backend.Register) []shiftByInstruction {
	var out []shiftByInstruction
	emit := func(code []byte, text string) { out = append(out, shiftByInstruction{code, text}) }
	if operation == op.OpShiftRightArithmetic {
		if src != 11 {
			emit(appendMovReg(nil, 11, uint8(src)), "mov R11, "+amd64Name(src))
		}
		b := NewByteAssembler(0, 0)
		b.Shift(op.OpShiftRightArithmetic, 64, 11, 11, 63)
		emit(b.code, "sar R11, 63")
	} else {
		emit([]byte{0x41, 0xbb, 0, 0, 0, 0}, "mov R11D, 0")
	}
	emit([]byte{0x48 | byte(count>>3), 0x83, 0xf8 | byte(count&7), width}, fmt.Sprintf("cmp %s, %d", amd64Name(count), width))
	pp := byte(1)
	mnemonic := "shlx"
	if operation == op.OpShiftRight {
		pp = 3
		mnemonic = "shrx"
	}
	if operation == op.OpShiftRightArithmetic {
		pp = 2
		mnemonic = "sarx"
	}
	emit(vexBMI(dst, src, count, pp, 0xf7), fmt.Sprintf("%s %s, %s, %s", mnemonic, amd64Name(dst), amd64Name(src), amd64Name(count)))
	emit([]byte{0x49 | byte(dst>>3)<<2, 0x0f, 0x43, 0xc0 | byte(dst&7)<<3 | 3}, "cmovae "+amd64Name(dst)+", R11")
	if width < 64 {
		b := NewByteAssembler(0, 0)
		b.Narrow(dst, dst, width)
		name := amd64RegisterNames8[dst]
		if width == 16 {
			name = amd64RegisterNames16[dst]
		}
		if width == 32 {
			name = amd64RegisterNames32[dst]
		}
		mnemonic := "movzx"
		if width == 32 {
			mnemonic = "mov"
		}
		emit(b.code, fmt.Sprintf("%s %s, %s", mnemonic, amd64RegisterNames32[dst], name))
	}
	return out
}

// shiftByGroup is the ModRM reg field and mnemonic of a shift or rotate.
func shiftByGroup(operation op.Opcode) (byte, string) {
	switch operation {
	case op.OpRotateLeft:
		return 0, "rol"
	case op.OpRotateRight:
		return 1, "ror"
	case op.OpShiftLeft:
		return 4, "shl"
	case op.OpShiftRight:
		return 5, "shr"
	case op.OpShiftRightArithmetic:
		return 7, "sar"
	}
	return 0, ""
}
