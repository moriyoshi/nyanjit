package i386

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
)

// Condition codes of Jcc, as in the low nibble of its opcode.
const (
	ccBelow        byte = 0x2
	ccAboveEqual   byte = 0x3
	ccEqual        byte = 0x4
	ccNotEqual     byte = 0x5
	ccBelowEqual   byte = 0x6
	ccAbove        byte = 0x7
	ccSign         byte = 0x8
	ccNotSign      byte = 0x9
	ccParity       byte = 0xa
	ccNoParity     byte = 0xb
	ccLess         byte = 0xc
	ccGreaterEqual byte = 0xd
	ccAlways       byte = 0xff // JMP
)

var conditionMnemonics = map[byte]string{
	ccBelow: "jb", ccAboveEqual: "jae", ccEqual: "je", ccNotEqual: "jne", ccBelowEqual: "jbe", ccAbove: "ja", ccSign: "js", ccNotSign: "jns",
	ccParity: "jp", ccNoParity: "jnp", ccLess: "jl", ccGreaterEqual: "jge", ccAlways: "jmp",
}

// expansionStep is one instruction of a fixed expansion. A branch names a
// label until resolve, and then holds the byte offset of its target within
// the expansion, so the listing can print an absolute address.
type expansionStep struct {
	code   []byte
	text   string
	target int // -1 for a step that does not branch
	cc     byte
	long   bool
}

// expansion builds a self-contained sequence with private branches. Its
// branches are rel8 where the plan's own layout allows and rel32 otherwise,
// decided from the plan alone, so the size never depends on the PC.
type expansion struct {
	text   bool
	frame  slotFrame
	steps  []expansionStep
	labels []int // the step index each label is bound to
}

// slotFrame locates spill slots as Spill and Reload do: ESP-based under
// abi.StackPlatform and EBP-based (the caller's workspace) otherwise.
type slotFrame struct {
	stack    abi.StackPractice
	callArea int
}

func (e *ByteAssembler) slotFrame() slotFrame { return slotFrame{e.stack, e.callArea} }

func (x *expansion) emit(code []byte, format string, args ...any) {
	step := expansionStep{code: code, target: -1}
	if x.text {
		step.text = fmt.Sprintf(format, args...)
	}
	x.steps = append(x.steps, step)
}
func (x *expansion) label() int {
	x.labels = append(x.labels, -1)
	return len(x.labels) - 1
}
func (x *expansion) bind(label int) { x.labels[label] = len(x.steps) }

// branch emits a Jcc (or JMP for ccAlways) to label.
func (x *expansion) branch(cc byte, label int) {
	x.steps = append(x.steps, expansionStep{target: label, cc: cc})
}

func branchCode(cc byte, long bool) []byte {
	switch {
	case cc == ccAlways && long:
		return []byte{0xe9, 0, 0, 0, 0}
	case cc == ccAlways:
		return []byte{0xeb, 0}
	case long:
		return []byte{0x0f, 0x80 | cc, 0, 0, 0, 0}
	}
	return []byte{0x70 | cc, 0}
}

// resolve encodes every branch. Branches start short; one whose target is
// out of rel8 range becomes rel32, which can only lengthen others, so the
// loop ends.
func (x *expansion) resolve() []expansionStep {
	offsets := make([]int, len(x.steps)+1)
	for {
		for n, s := range x.steps {
			size := len(s.code)
			if s.target >= 0 {
				size = len(branchCode(s.cc, s.long))
			}
			offsets[n+1] = offsets[n] + size
		}
		changed := false
		for n := range x.steps {
			s := &x.steps[n]
			if s.target < 0 || s.long {
				continue
			}
			rel := offsets[x.labels[s.target]] - offsets[n+1]
			if rel < -128 || rel > 127 {
				s.long, changed = true, true
			}
		}
		if !changed {
			break
		}
	}
	for n := range x.steps {
		s := &x.steps[n]
		if s.target < 0 {
			continue
		}
		to := offsets[x.labels[s.target]]
		rel := to - offsets[n+1]
		s.code = branchCode(s.cc, s.long)
		if s.long {
			s.code = append32(s.code[:len(s.code)-4], uint32(int32(rel)))
		} else {
			s.code[1] = byte(int8(rel))
		}
		s.target = to
		if x.text {
			s.text = conditionMnemonics[s.cc]
		}
	}
	return x.steps
}

// The general-register forms the expansions use. reg and rm are register
// numbers; group selects the operation of an opcode-extension form.

var aluMnemonics = map[byte]string{0x01: "add", 0x09: "or", 0x11: "adc", 0x19: "sbb", 0x21: "and", 0x29: "sub", 0x31: "xor", 0x39: "cmp", 0x85: "test"}
var groupMnemonics = [8]string{"add", "or", "adc", "sbb", "and", "sub", "xor", "cmp"}
var shiftMnemonics = [8]string{"rol", "ror", "rcl", "rcr", "shl", "shr", "sal", "sar"}
var unaryMnemonics = [8]string{"test", "", "not", "neg", "mul", "imul", "div", "idiv"}

const (
	aluAdd  byte = 0x01
	aluOr   byte = 0x09
	aluAdc  byte = 0x11
	aluSbb  byte = 0x19
	aluAnd  byte = 0x21
	aluSub  byte = 0x29
	aluXor  byte = 0x31
	aluCmp  byte = 0x39
	aluTest byte = 0x85

	groupAdd byte = 0
	groupOr  byte = 1
	groupAdc byte = 2
	groupSbb byte = 3
	groupAnd byte = 4
	groupSub byte = 5
	groupXor byte = 6
	groupCmp byte = 7

	shiftLeft  byte = 4
	shiftRight byte = 5

	unaryNeg  byte = 3
	unaryMul  byte = 4
	unaryDiv  byte = 6
	unaryIDiv byte = 7
)

func (x *expansion) mov(d, s backend.Register) {
	if d != s {
		x.emit([]byte{0x89, modRM(3, byte(s), byte(d))}, "mov %s, %s", name(d), name(s))
	}
}

// alu emits `op d, s` for an "op r/m32, r32" opcode, test included.
func (x *expansion) alu(opcode byte, d, s backend.Register) {
	x.emit([]byte{opcode, modRM(3, byte(s), byte(d))}, "%s %s, %s", aluMnemonics[opcode], name(d), name(s))
}

// immediate emits a group-1 `op d, imm`, with an imm8 when it fits.
func (x *expansion) immediate(group byte, d backend.Register, imm uint32) {
	if int32(imm) >= -128 && int32(imm) <= 127 {
		x.emit([]byte{0x83, modRM(3, group, byte(d)), byte(imm)}, "%s %s, %d", groupMnemonics[group], name(d), int32(imm))
		return
	}
	x.emit(append32([]byte{0x81, modRM(3, group, byte(d))}, imm), "%s %s, 0x%x", groupMnemonics[group], name(d), imm)
}
func (x *expansion) testImmediate(d backend.Register, imm uint32) {
	x.emit(append32([]byte{0xf7, modRM(3, 0, byte(d))}, imm), "test %s, 0x%x", name(d), imm)
}
func (x *expansion) shift(group byte, d backend.Register, count byte) {
	if count == 1 {
		x.emit([]byte{0xd1, modRM(3, group, byte(d))}, "%s %s, 1", shiftMnemonics[group], name(d))
		return
	}
	x.emit([]byte{0xc1, modRM(3, group, byte(d)), count}, "%s %s, %d", shiftMnemonics[group], name(d), count)
}
func (x *expansion) shiftCL(group byte, d backend.Register) {
	x.emit([]byte{0xd3, modRM(3, group, byte(d))}, "%s %s, CL", shiftMnemonics[group], name(d))
}

// double emits shld (left) or shrd d, s by count, or by CL when count < 0.
func (x *expansion) double(left bool, d, s backend.Register, count int) {
	opcode, mnemonic := byte(0xac), "shrd"
	if left {
		opcode, mnemonic = 0xa4, "shld"
	}
	if count < 0 {
		x.emit([]byte{0x0f, opcode + 1, modRM(3, byte(s), byte(d))}, "%s %s, %s, CL", mnemonic, name(d), name(s))
		return
	}
	x.emit([]byte{0x0f, opcode, modRM(3, byte(s), byte(d)), byte(count)}, "%s %s, %s, %d", mnemonic, name(d), name(s), count)
}
func (x *expansion) unary(group byte, r backend.Register) {
	x.emit([]byte{0xf7, modRM(3, group, byte(r))}, "%s %s", unaryMnemonics[group], name(r))
}

// negate64 negates the pair high:low.
func (x *expansion) negate64(low, high backend.Register) {
	x.unary(unaryNeg, low)
	x.immediate(groupAdc, high, 0)
	x.unary(unaryNeg, high)
}

// memory returns the ModRM, SIB and disp32 bytes naming slot.
func (x *expansion) memory(reg byte, slot int) ([]byte, string) {
	base := backend.Register(5)
	if x.frame.stack == abi.StackPlatform {
		base = 4
	}
	disp := x.frame.callArea + slot*4
	code := []byte{modRM(2, reg, byte(base))}
	if base == 4 {
		code = append(code, 0x24)
	}
	text := ""
	if x.text {
		text = offset(base, uint64(disp))
	}
	return append32(code, uint32(disp)), text
}
func (x *expansion) load(r backend.Register, slot int) {
	m, text := x.memory(byte(r), slot)
	x.emit(append([]byte{0x8b}, m...), "mov %s, dword ptr %s", name(r), text)
}
func (x *expansion) store(slot int, r backend.Register) {
	m, text := x.memory(byte(r), slot)
	x.emit(append([]byte{0x89}, m...), "mov dword ptr %s, %s", text, name(r))
}
func (x *expansion) storeImmediate(slot int, imm uint32) {
	m, text := x.memory(0, slot)
	x.emit(append32(append([]byte{0xc7}, m...), imm), "mov dword ptr %s, 0x%x", text, imm)
}

// memoryImmediate emits a group-1 `op dword [slot], imm`.
func (x *expansion) memoryImmediate(group byte, slot int, imm uint32) {
	m, text := x.memory(group, slot)
	if int32(imm) >= -128 && int32(imm) <= 127 {
		x.emit(append(append([]byte{0x83}, m...), byte(imm)), "%s dword ptr %s, %d", groupMnemonics[group], text, int32(imm))
		return
	}
	x.emit(append32(append([]byte{0x81}, m...), imm), "%s dword ptr %s, 0x%x", groupMnemonics[group], text, imm)
}

// sse emits an SSE register form: [prefix] 0F opcode /r [imm8]. prefix 0
// means none.
func (x *expansion) sse(prefix, opcode, reg, rm byte, format string, args ...any) {
	code := []byte{0x0f, opcode, modRM(3, reg, rm)}
	if prefix != 0 {
		code = append([]byte{prefix}, code...)
	}
	x.emit(code, format, args...)
}

// sseMemory emits [prefix] 0F opcode with an XMM register and slot.
func (x *expansion) sseMemory(prefix, opcode, xmm byte, slot int, mnemonic string) {
	m, text := x.memory(xmm, slot)
	x.emit(append([]byte{prefix, 0x0f, opcode}, m...), "%s xmm%d, qword ptr %s", mnemonic, xmm, text)
}

func emitExpansion(e *ByteAssembler, steps []expansionStep) {
	for _, s := range steps {
		e.code = append(e.code, s.code...)
	}
}
func recordExpansion(e *TextAssembler, steps []expansionStep) {
	start := e.AssembledSize()
	for _, s := range steps {
		if s.target >= 0 {
			e.record("%s 0x%x", s.text, start+s.target)
		} else {
			e.record("%s", s.text)
		}
		e.code = append(e.code, s.code...)
	}
}
