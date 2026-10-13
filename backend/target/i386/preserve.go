package i386

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// zeroExtension is MOVZX dst, src8 or src16 (0F B6 /r or 0F B7 /r): the
// zero extension of an 8- or 16-bit value, which writes no flags.
type zeroExtension struct {
	dst, src backend.Register
	width    uint8
}

// extends reports whether MOVZX has a width-bit source in src: only AL, CL,
// DL and BL name a low byte, since codes 4-7 select AH, CH, DH and BH.
func extends(src backend.Register, width uint8) bool {
	return width == 16 || width == 8 && src < 4
}

func (z zeroExtension) append(code []byte) []byte {
	opcode := byte(0xb7)
	if z.width == 8 {
		opcode = 0xb6
	}
	return append(code, 0x0f, opcode, modRM(3, byte(z.dst), byte(z.src)))
}

func (z zeroExtension) text() string {
	if z.width == 8 {
		return "movzx " + name(z.dst) + ", " + byteNames[z.src]
	}
	return "movzx " + name(z.dst) + ", " + name16(z.src)
}

// The code before a preserved form's zero extension.
const (
	preserveNone      = iota
	preserveLEA       // leaForm; no flags
	preserveMove      // mov A, B; no flags
	preserveOperation // the 32-bit OpBinary or OpShift in body, with its flags
)

// preservedForm is the code an instruction marked PreserveFlags selects: a
// LEA, a move or the instruction's own 32-bit operation, then, for an 8- or
// 16-bit result, MOVZX where the flag-writing form masks with AND. Only the
// operation writes flags.
type preservedForm struct {
	kind   uint8
	lea    leaForm
	body   machine.Instruction
	extend bool
	zx     zeroExtension
}

// preservedFor returns the form ins selects with PreserveFlags: LEA for
// additions, immediate subtractions, increments and decrements; MOVZX for
// an AND with 0xff or 0xffff; and for an 8- or 16-bit OpBinary or immediate
// shift other than SAR, the operation then MOVZX, after a LEA instead for an
// addition into another register. An 8-bit result needs a byte register.
func preservedFor(ins machine.Instruction) (preservedForm, bool) {
	if !validInstructionRegisters(ins) {
		return preservedForm{}, false
	}
	switch ins.Op {
	case machine.OpBinary:
		if !validBinary(ins.Arithmetic, ins.Bits) {
			break
		}
		if ins.Bits != 8 && ins.Bits != 16 {
			lea, ok := leaFor(ins)
			return preservedForm{kind: preserveLEA, lea: lea}, ok
		}
		if !extends(ins.A, ins.Bits) {
			break
		}
		f := preservedForm{kind: preserveOperation, body: ins, extend: true, zx: zeroExtension{ins.A, ins.A, ins.Bits}}
		f.body.Bits, f.body.PreserveFlags = 32, false
		if lea, ok := leaFor(ins); ok && ins.A != ins.B {
			f.kind, f.lea = preserveLEA, lea
		}
		return f, true
	case machine.OpBinaryImmediate:
		if !validBinaryImmediate(ins.Arithmetic, ins.Bits, ins.Imm) {
			break
		}
		if ins.Arithmetic != op.OpAnd {
			lea, ok := leaFor(ins)
			return preservedForm{kind: preserveLEA, lea: lea}, ok
		}
		width := uint8(0)
		switch uint32(ins.Imm) {
		case 0xff:
			width = 8
		case 0xffff:
			width = 16
		}
		switch {
		case width == 0:
		case extends(ins.B, width):
			return preservedForm{extend: true, zx: zeroExtension{ins.A, ins.B, width}}, true
		case extends(ins.A, width):
			return preservedForm{kind: preserveMove, body: ins, extend: true, zx: zeroExtension{ins.A, ins.A, width}}, true
		}
	case machine.OpInc, machine.OpDec:
		lea, ok := leaFor(ins)
		return preservedForm{kind: preserveLEA, lea: lea}, ok
	case machine.OpShift:
		// Visit admits counts up to 255; SAR sign-extends instead of masking.
		if ins.Bits != 8 && ins.Bits != 16 || ins.Imm > 255 || ins.Arithmetic == op.OpShiftRightArithmetic || !extends(ins.A, ins.Bits) {
			break
		}
		f := preservedForm{kind: preserveOperation, body: ins, extend: true, zx: zeroExtension{ins.A, ins.A, ins.Bits}}
		f.body.Bits, f.body.PreserveFlags = 32, false
		return f, true
	}
	return preservedForm{}, false
}

func (f preservedForm) size() int {
	n := 0
	switch f.kind {
	case preserveLEA:
		n = f.lea.size()
	case preserveMove:
		n = 2
	case preserveOperation:
		n = 2 // op A, C
		if f.body.Op == machine.OpShift {
			n = 3 // C1 /n ib
		} else if f.body.Arithmetic == op.OpMul {
			n = 3 // IMUL r32, r/m32
		}
		if f.body.A != f.body.B {
			n += 2
		}
	}
	if f.extend {
		n += 3
	}
	return n
}

// modified reports the flags the form writes: those of its operation.
func (f preservedForm) modified() machine.FlagMask {
	if f.kind != preserveOperation {
		return 0
	}
	if f.body.Op == machine.OpShift {
		return shiftFlags(f.body)
	}
	return machine.FlagSign | machine.FlagZero | machine.FlagCarry
}

func (e *ByteAssembler) preserved(ins machine.Instruction) {
	f, ok := preservedFor(ins)
	if !e.require(ok) {
		return
	}
	switch f.kind {
	case preserveLEA:
		e.code = f.lea.append(e.code)
	case preserveMove:
		e.Move(f.body.A, f.body.B)
	case preserveOperation:
		if f.body.Op == machine.OpShift {
			e.Shift(f.body.Arithmetic, 32, f.body.A, f.body.B, uint8(f.body.Imm))
		} else {
			e.Binary(f.body.Arithmetic, 32, f.body.A, f.body.B, f.body.C)
		}
	}
	if f.extend {
		e.code = f.zx.append(e.code)
	}
}

func (e *TextAssembler) preserved(ins machine.Instruction) {
	f, ok := preservedFor(ins)
	if !e.require(ok) {
		return
	}
	switch f.kind {
	case preserveLEA:
		e.record("lea %s, %s", name(f.lea.dst), f.lea.text())
		e.code = f.lea.append(e.code)
	case preserveMove:
		e.Move(f.body.A, f.body.B)
	case preserveOperation:
		if f.body.Op == machine.OpShift {
			e.Shift(f.body.Arithmetic, 32, f.body.A, f.body.B, uint8(f.body.Imm))
		} else {
			e.Binary(f.body.Arithmetic, 32, f.body.A, f.body.B, f.body.C)
		}
	}
	if f.extend {
		e.record("%s", f.zx.text())
		e.code = f.zx.append(e.code)
	}
}
